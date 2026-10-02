package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/metadata"
	mmpb "npln.nintendo.net/npln-practice/proto/matchmaking/v1"
)

func scarletAuthenticatedContext(uid string) context.Context {
	user := nplnTenant + "/users/" + uid
	token := mintNplnAccessTokenForApp(1800000001, user, nplnTenant, nplnScarletAppID)
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		"authorization", "Bearer "+token,
		"uid", uid,
	))
}

func TestScarletRandomRaidWithoutHostRemainsSearching(t *testing.T) {
	m := newMatchmaker(newSessionRegistry())
	ctx, cancel := context.WithCancel(scarletAuthenticatedContext("scarlet-random-searcher"))
	defer cancel()
	ticket, err := m.CreateMatchmakingTicket(ctx, &mmpb.CreateMatchmakingTicketRequest{
		MatchmakingTicket: &mmpb.MatchmakingTicket{
			MatchmakingConfig: "RaidPublic",
			UserDefinitions:   []*mmpb.UserDefinition{{User: "users/current", Team: "participant"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	stream := &cancellingMatchmakingStream{
		ctx:  ctx,
		sent: make(chan mmpb.MatchmakingTicket_State, 4),
	}
	done := make(chan error, 1)
	go func() {
		done <- m.TrackMatchmakingTicket(&mmpb.TrackMatchmakingTicketRequest{Name: ticket.Name}, stream)
	}()
	select {
	case state := <-stream.sent:
		if state != mmpb.MatchmakingTicket_SEARCHING {
			t.Fatalf("first state = %s, want SEARCHING", state)
		}
	case err := <-done:
		t.Fatalf("search ended before first state: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("search did not start")
	}

	// The old server sent FAILED after ten seconds with no game session;
	// Scarlet then dereferenced a null pointer in its NPLN worker.
	select {
	case state := <-stream.sent:
		t.Fatalf("empty search sent terminal state %s", state)
	case err := <-done:
		t.Fatalf("empty search ended without client cancellation: %v", err)
	case <-time.After(11 * time.Second):
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("search after cancellation = %v, want context canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled search did not finish")
	}
	m.mu.Lock()
	_, exists := m.tickets[lastResourceSegment(ticket.Name)]
	m.mu.Unlock()
	if exists {
		t.Fatal("cancelled random raid ticket remains stored")
	}
}
