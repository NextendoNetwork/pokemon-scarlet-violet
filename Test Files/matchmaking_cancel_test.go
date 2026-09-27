package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	commonpb "npln.nintendo.net/npln-practice/proto/common"
	mmpb "npln.nintendo.net/npln-practice/proto/matchmaking/v1"
)

type cancellingMatchmakingStream struct {
	grpc.ServerStream
	ctx  context.Context
	sent chan mmpb.MatchmakingTicket_State
}

func TestWaitingPairReportsOnlyStateChanges(t *testing.T) {
	t.Setenv("NPLN_MATCH_PHASE_DELAY", "5ms")
	m := newMatchmaker(newSessionRegistry())
	create := func(uid string) *mmpb.MatchmakingTicket {
		t.Helper()
		ticket, err := m.CreateMatchmakingTicket(violetAuthenticatedContext(uid),
			&mmpb.CreateMatchmakingTicketRequest{MatchmakingTicket: &mmpb.MatchmakingTicket{
				MatchmakingConfig: "BoxTrade",
				UserDefinitions: []*mmpb.UserDefinition{{
					User: "users/current",
					Attributes: &commonpb.MapValue{Fields: map[string]*commonpb.Value{
						"password": gamesyncStringValue("22446688"),
					}},
				}},
			}})
		if err != nil {
			t.Fatal(err)
		}
		return ticket
	}
	track := func(uid string, ticket *mmpb.MatchmakingTicket) (<-chan mmpb.MatchmakingTicket_State, <-chan error) {
		stream := &cancellingMatchmakingStream{ctx: violetAuthenticatedContext(uid), sent: make(chan mmpb.MatchmakingTicket_State, 8)}
		done := make(chan error, 1)
		go func() {
			done <- m.TrackMatchmakingTicket(&mmpb.TrackMatchmakingTicketRequest{Name: ticket.Name}, stream)
		}()
		return stream.sent, done
	}
	waitState := func(states <-chan mmpb.MatchmakingTicket_State, want mmpb.MatchmakingTicket_State) {
		t.Helper()
		select {
		case got := <-states:
			if got != want {
				t.Fatalf("tracking state = %s, want %s", got, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for %s", want)
		}
	}

	first := create("u-first")
	firstStates, firstDone := track("u-first", first)
	waitState(firstStates, mmpb.MatchmakingTicket_SEARCHING)
	select {
	case got := <-firstStates:
		t.Fatalf("duplicate waiting state: %s", got)
	case <-time.After(600 * time.Millisecond):
	}
	second := create("u-second")
	secondStates, secondDone := track("u-second", second)
	for _, states := range []<-chan mmpb.MatchmakingTicket_State{firstStates, secondStates} {
		if states == secondStates {
			waitState(states, mmpb.MatchmakingTicket_SEARCHING)
		}
		waitState(states, mmpb.MatchmakingTicket_PLACING)
		waitState(states, mmpb.MatchmakingTicket_SUCCEEDED)
	}
	for _, done := range []<-chan error{firstDone, secondDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("tracking pair: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("paired stream did not finish")
		}
	}
}

func (s *cancellingMatchmakingStream) Context() context.Context { return s.ctx }

func (s *cancellingMatchmakingStream) Send(ticket *mmpb.MatchmakingTicket) error {
	select {
	case s.sent <- ticket.GetState():
	default:
	}
	return nil
}

func TestCancelAfterTrackingStreamEnds(t *testing.T) {
	m := newMatchmaker(newSessionRegistry())
	ticket, err := m.CreateMatchmakingTicket(violetAuthenticatedContext("u-owner"),
		&mmpb.CreateMatchmakingTicketRequest{MatchmakingTicket: &mmpb.MatchmakingTicket{
			MatchmakingConfig: "BoxTrade",
			UserDefinitions:   []*mmpb.UserDefinition{{User: "users/current"}},
		}})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(violetAuthenticatedContext("u-owner"))
	defer cancel()
	stream := &cancellingMatchmakingStream{ctx: ctx, sent: make(chan mmpb.MatchmakingTicket_State, 8)}
	done := make(chan error, 1)
	go func() {
		done <- m.TrackMatchmakingTicket(&mmpb.TrackMatchmakingTicketRequest{Name: ticket.Name}, stream)
	}()
	select {
	case state := <-stream.sent:
		if state != mmpb.MatchmakingTicket_SEARCHING {
			t.Fatalf("first tracking state = %s, want SEARCHING", state)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("tracking stream did not start")
	}
	select {
	case state := <-stream.sent:
		t.Fatalf("duplicate tracking state while waiting: %s", state)
	case <-time.After(750 * time.Millisecond):
	}
	req := &mmpb.CancelMatchmakingTicketRequest{Name: ticket.Name}
	if _, err := m.CancelMatchmakingTicket(violetAuthenticatedContext("u-other"), req); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("other user's cancel = %v, want PermissionDenied", err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("tracking stream error = %v, want canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("tracking stream did not stop")
	}

	id := lastResourceSegment(ticket.Name)
	m.mu.Lock()
	_, stored := m.tickets[id]
	_, stillMatched := m.ticketSessions[id]
	m.mu.Unlock()
	if stored || stillMatched {
		t.Fatal("stream shutdown must release the waiting session and its ticket")
	}

	if _, err := m.CancelMatchmakingTicket(violetAuthenticatedContext("u-owner"), req); err != nil {
		t.Fatalf("owner cancel = %v", err)
	}
	if _, err := m.CancelMatchmakingTicket(violetAuthenticatedContext("u-owner"), req); err != nil {
		t.Fatalf("repeated cancel = %v", err)
	}
	m.mu.Lock()
	_, exists := m.tickets[id]
	m.mu.Unlock()
	if exists {
		t.Fatal("canceled ticket remains stored")
	}
}

func TestCancelDuringTrackingDoesNotSendFailed(t *testing.T) {
	m := newMatchmaker(newSessionRegistry())
	ticket, err := m.CreateMatchmakingTicket(violetAuthenticatedContext("u-owner"),
		&mmpb.CreateMatchmakingTicketRequest{MatchmakingTicket: &mmpb.MatchmakingTicket{
			MatchmakingConfig: "BoxTrade",
			UserDefinitions:   []*mmpb.UserDefinition{{User: "users/current"}},
		}})
	if err != nil {
		t.Fatal(err)
	}
	stream := &cancellingMatchmakingStream{
		ctx:  violetAuthenticatedContext("u-owner"),
		sent: make(chan mmpb.MatchmakingTicket_State, 8),
	}
	done := make(chan error, 1)
	go func() {
		done <- m.TrackMatchmakingTicket(&mmpb.TrackMatchmakingTicketRequest{Name: ticket.Name}, stream)
	}()
	select {
	case state := <-stream.sent:
		if state != mmpb.MatchmakingTicket_SEARCHING {
			t.Fatalf("first tracking state = %s, want SEARCHING", state)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("tracking stream did not start")
	}
	if _, err := m.CancelMatchmakingTicket(violetAuthenticatedContext("u-owner"),
		&mmpb.CancelMatchmakingTicketRequest{Name: ticket.Name}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("tracking after explicit cancel = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("tracking stream did not end after explicit cancel")
	}
	select {
	case state := <-stream.sent:
		t.Fatalf("unexpected state after cancel: %s", state)
	default:
	}
}
