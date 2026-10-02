package main

import (
	"context"
	"testing"

	commonpb "npln.nintendo.net/npln-practice/proto/common"
	gspb "npln.nintendo.net/npln-practice/proto/gamesync/v1"
	mmpb "npln.nintendo.net/npln-practice/proto/matchmaking/v1"
)

func multiBattleTicket(t *testing.T, m *matchmakerServer, ctx context.Context, code string) *mmpb.MatchmakingTicket {
	t.Helper()
	ticket, err := m.CreateMatchmakingTicket(ctx, &mmpb.CreateMatchmakingTicketRequest{
		MatchmakingTicket: &mmpb.MatchmakingTicket{
			MatchmakingConfig: "tenants/current/matchmakingConfigs/NbrMulti",
			UserDefinitions: []*mmpb.UserDefinition{{
				User: "users/current",
				Attributes: &commonpb.MapValue{Fields: map[string]*commonpb.Value{
					"password": gamesyncStringValue(code),
				}},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return ticket
}

func multiBattleResult(t *testing.T, m *matchmakerServer, ctx context.Context, ticket *mmpb.MatchmakingTicket) *mmpb.MatchmakingTicket {
	t.Helper()
	result, ok := m.completeMatchmakingTicket(ctx, ticket.Name, []string{"users/current"})
	if !ok {
		t.Fatal("ticket did not resolve")
	}
	return result
}

func TestLinkMultiWaitsForFourPlayersAcrossScarletAndViolet(t *testing.T) {
	if err := validateVioletMatchmakingConfig("NbrMulti"); err != nil {
		t.Fatal(err)
	}
	if got := publicMatchCapacity("NbrMulti"); got != 4 {
		t.Fatalf("NbrMulti capacity = %d, want 4", got)
	}
	r := newSessionRegistry()
	m := newMatchmaker(r)
	g := newGamesyncServer(r)
	contexts := []context.Context{
		violetAuthenticatedContext("u-violet-1"),
		scarletAuthenticatedContext("u-scarlet-1"),
		violetAuthenticatedContext("u-violet-2"),
		scarletAuthenticatedContext("u-scarlet-2"),
	}
	tickets := make([]*mmpb.MatchmakingTicket, 0, 4)
	for i, ctx := range contexts {
		ticket := multiBattleTicket(t, m, ctx, "1234")
		tickets = append(tickets, ticket)
		for j := range tickets {
			result := multiBattleResult(t, m, contexts[j], tickets[j])
			if i < 3 && (result.State != mmpb.MatchmakingTicket_SEARCHING || result.GameSession != nil) {
				t.Fatalf("player %d succeeded with only %d participants", j, i+1)
			}
		}
	}
	var sessionName string
	for i, ticket := range tickets {
		result := multiBattleResult(t, m, contexts[i], ticket)
		if result.State != mmpb.MatchmakingTicket_SUCCEEDED || result.GameSession.GetCurrentParticipantCount() != 4 ||
			result.GameSession.GetMaxParticipantCount() != 4 || result.GameSession.GetCanParticipate() || len(result.GameSession.GetUserSessions()) != 4 ||
			len(result.MatchedUserSessions) != 1 || result.MatchedUserSessions[0].GetMatchmakingIdToken() == "" {
			t.Fatalf("player %d received incomplete four-player match", i)
		}
		if i == 0 {
			sessionName = result.GameSession.Name
			if err := g.initializeFixedData(result.GameSession); err != nil {
				t.Fatal(err)
			}
			fields := g.documents[sessionName]["docs/__gs/f"].Fields.GetFields()
			if fields["mcn"].GetStringValue() != "NbrMulti" || fields["maxu"].GetIntegerValue() != 4 {
				t.Fatal("Gamesync fixed data did not describe a four-player multi battle")
			}
		} else if result.GameSession.Name != sessionName {
			t.Fatal("players received different game sessions")
		}
		if _, err := g.IssueToken(context.Background(), &gspb.IssueTokenRequest{
			UserSession:        result.MatchedUserSessions[0].UserSession,
			MatchmakingIdToken: result.MatchedUserSessions[0].MatchmakingIdToken,
		}); err != nil {
			t.Fatalf("player %d Gamesync token: %v", i, err)
		}
	}
	newTicket := multiBattleTicket(t, m, violetAuthenticatedContext("u-violet-3"), "1234")
	if result := multiBattleResult(t, m, violetAuthenticatedContext("u-violet-3"), newTicket); result.State != mmpb.MatchmakingTicket_SEARCHING {
		t.Fatal("fifth player entered completed match")
	}
	otherCode := multiBattleTicket(t, m, violetAuthenticatedContext("u-other-code"), "5678")
	if publicMatchPoolKey(newTicket) == publicMatchPoolKey(otherCode) {
		t.Fatal("different link codes share a pool")
	}
}

func TestLinkMultiCancellationKeepsOtherSearchers(t *testing.T) {
	m := newMatchmaker(newSessionRegistry())
	contexts := []context.Context{
		violetAuthenticatedContext("u-first"),
		scarletAuthenticatedContext("u-cancel"),
		violetAuthenticatedContext("u-third"),
	}
	tickets := make([]*mmpb.MatchmakingTicket, 3)
	for i, ctx := range contexts {
		tickets[i] = multiBattleTicket(t, m, ctx, "1234")
		if result := multiBattleResult(t, m, ctx, tickets[i]); result.State != mmpb.MatchmakingTicket_SEARCHING {
			t.Fatal("multi battle completed before four players")
		}
	}
	if _, err := m.CancelMatchmakingTicket(contexts[1], &mmpb.CancelMatchmakingTicketRequest{Name: tickets[1].Name}); err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{0, 2} {
		if result := multiBattleResult(t, m, contexts[i], tickets[i]); result.State != mmpb.MatchmakingTicket_SEARCHING {
			t.Fatal("remaining player lost their search after another canceled")
		}
	}
	for _, uid := range []string{"u-replacement-1", "u-replacement-2"} {
		ctx := violetAuthenticatedContext(uid)
		ticket := multiBattleTicket(t, m, ctx, "1234")
		tickets = append(tickets, ticket)
		contexts = append(contexts, ctx)
		multiBattleResult(t, m, ctx, ticket)
	}
	for _, i := range []int{0, 2, 3, 4} {
		if result := multiBattleResult(t, m, contexts[i], tickets[i]); result.State != mmpb.MatchmakingTicket_SUCCEEDED || result.GameSession.CurrentParticipantCount != 4 {
			t.Fatal("replacement players did not complete multi battle")
		}
	}
}
