package main

import (
	"context"
	commonpb "npln.nintendo.net/npln-practice/proto/common"
	gspb "npln.nintendo.net/npln-practice/proto/gamesync/v1"
	mmpb "npln.nintendo.net/npln-practice/proto/matchmaking/v1"
	"testing"
)

func TestPortalPairing(t *testing.T) {
	for _, config := range []string{"BoxTrade", "NbrSingle", "RankBattle", "Competition"} {
		t.Run(config, func(t *testing.T) {
			r := newSessionRegistry()
			m := newMatchmaker(r)
			create := func(uid, mode, code string) *mmpb.MatchmakingTicket {
				t.Helper()
				ticket, err := m.CreateMatchmakingTicket(violetAuthenticatedContext(uid), &mmpb.CreateMatchmakingTicketRequest{MatchmakingTicket: &mmpb.MatchmakingTicket{MatchmakingConfig: mode, UserDefinitions: []*mmpb.UserDefinition{{User: "users/current", Attributes: &commonpb.MapValue{Fields: map[string]*commonpb.Value{"password": gamesyncStringValue(code)}}}}}})
				if err != nil {
					t.Fatal(err)
				}
				return ticket
			}
			complete := func(uid string, ticket *mmpb.MatchmakingTicket) *mmpb.MatchmakingTicket {
				t.Helper()
				result, ok := m.completeMatchmakingTicket(violetAuthenticatedContext(uid), ticket.Name, []string{"users/current"})
				if !ok {
					t.Fatal("completion failed")
				}
				return result
			}
			first := create("u-first", config, "22446688")
			if complete("u-first", first).State != mmpb.MatchmakingTicket_SEARCHING {
				t.Fatal("solo ticket succeeded")
			}
			other := create("u-other", config, "12345678")
			if complete("u-other", other).State != mmpb.MatchmakingTicket_SEARCHING {
				t.Fatal("different codes paired")
			}
			second := create("u-second", config, "22446688")
			b := complete("u-second", second)
			a := complete("u-first", first)
			if a.State != mmpb.MatchmakingTicket_SUCCEEDED || b.State != a.State || a.GameSession.Name != b.GameSession.Name || a.GameSession.MaxParticipantCount != 2 || len(a.GameSession.UserSessions) != 2 {
				t.Fatal("pair mismatch")
			}
			g := newGamesyncServer(r)
			for _, result := range []*mmpb.MatchmakingTicket{a, b} {
				if len(result.MatchedUserSessions) != 1 {
					t.Fatal("foreign identity returned")
				}
				own := result.MatchedUserSessions[0]
				if _, err := g.IssueToken(context.Background(), &gspb.IssueTokenRequest{UserSession: own.UserSession, MatchmakingIdToken: own.MatchmakingIdToken}); err != nil {
					t.Fatal(err)
				}
			}
			if err := g.initializeFixedData(a.GameSession); err != nil {
				t.Fatal(err)
			}
			if got := g.documents[a.GameSession.Name]["docs/__gs/f"].Fields.Fields["mcn"].GetStringValue(); got != config {
				t.Fatalf("mcn=%s", got)
			}
			if _, err := m.CancelMatchmakingTicket(violetAuthenticatedContext("u-other"), &mmpb.CancelMatchmakingTicketRequest{Name: other.Name}); err != nil {
				t.Fatal(err)
			}
			replacement := create("u-new", config, "12345678")
			if complete("u-new", replacement).State != mmpb.MatchmakingTicket_SEARCHING {
				t.Fatal("paired with canceled ticket")
			}
		})
	}
	if publicMatchPoolKey(&mmpb.MatchmakingTicket{MatchmakingConfig: "BoxTrade"}) == publicMatchPoolKey(&mmpb.MatchmakingTicket{MatchmakingConfig: "NbrSingle"}) {
		t.Fatal("modes share pool")
	}
}

func TestLinkBattleRetryUsesNewSession(t *testing.T) {
	m := newMatchmaker(newSessionRegistry())
	create := func(uid string) *mmpb.MatchmakingTicket {
		t.Helper()
		ticket, err := m.CreateMatchmakingTicket(violetAuthenticatedContext(uid),
			&mmpb.CreateMatchmakingTicketRequest{MatchmakingTicket: &mmpb.MatchmakingTicket{
				MatchmakingConfig: "NbrSingle",
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
	complete := func(uid string, ticket *mmpb.MatchmakingTicket) *mmpb.MatchmakingTicket {
		t.Helper()
		result, ok := m.completeMatchmakingTicket(violetAuthenticatedContext(uid), ticket.Name, nil)
		if !ok {
			t.Fatal("completion failed")
		}
		return result
	}

	first := create("u-first")
	if got := complete("u-first", first).State; got != mmpb.MatchmakingTicket_SEARCHING {
		t.Fatalf("first search state = %s", got)
	}
	second := create("u-second")
	previous := complete("u-second", second).GameSession.GetName()
	if previous == "" || complete("u-first", first).GameSession.GetName() != previous {
		t.Fatal("first pair did not share a session")
	}

	retry := create("u-first")
	if got := complete("u-first", retry).State; got != mmpb.MatchmakingTicket_SEARCHING {
		t.Fatalf("retry state = %s, want SEARCHING", got)
	}
	m.mu.Lock()
	retrySession := m.ticketSessions[lastResourceSegment(retry.Name)]
	m.mu.Unlock()
	if retrySession == nil || retrySession.gameSession.GetName() == previous {
		t.Fatal("retry reused the completed battle session")
	}
	peerRetry := create("u-second")
	result := complete("u-second", peerRetry)
	if result.State != mmpb.MatchmakingTicket_SUCCEEDED ||
		result.GameSession.GetName() != retrySession.gameSession.GetName() ||
		result.GameSession.GetCurrentParticipantCount() != 2 {
		t.Fatal("retry pair did not join a fresh two-player session")
	}
}
