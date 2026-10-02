package main

import (
	"testing"

	commonpb "npln.nintendo.net/npln-practice/proto/common"
	mmpb "npln.nintendo.net/npln-practice/proto/matchmaking/v1"
)

func TestRaidPrivateHostAndCodeJoin(t *testing.T) {
	g := newGameSessionServer(newSessionRegistry())
	host := violetAuthenticatedContext("raid-private-host")
	ticket, err := g.CreateGameSessionCreationTicket(host, &mmpb.CreateGameSessionCreationTicketRequest{
		Parent: "tenants/current",
		GameSessionCreationTicket: &mmpb.GameSessionCreationTicket{
			MatchmakingConfig: "tenants/current/matchmakingConfigs/RaidPrivate",
			UserDefinitions:   []*mmpb.UserDefinition{{User: "users/current", Team: "owner"}},
			GameSession:       &mmpb.GameSession{Password: "RaidPrivate", Properties: &commonpb.MapValue{Fields: map[string]*commonpb.Value{"raid_table_id": gamesyncIntegerValue(4061)}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	room, ok := g.completeGameSessionCreationTicket(host, ticket.Name, []string{"users/current"})
	if !ok || room.GetState() != mmpb.GameSessionCreationTicket_SUCCEEDED {
		t.Fatal("private raid creation failed")
	}
	gs := room.GameSession
	if gs.IsPublic || gs.MaxParticipantCount != 4 || gs.Properties.Fields["raid_table_id"].GetIntegerValue() != 4061 {
		t.Fatal("invalid private raid room")
	}
	alias, err := g.CreateGameSessionShortAlias(host, &mmpb.CreateGameSessionShortAliasRequest{GameSessionShortAlias: &mmpb.GameSessionShortAlias{GameSession: gs.Name}})
	if err != nil {
		t.Fatal(err)
	}
	guest := violetAuthenticatedContext("raid-private-guest")
	resolved, err := g.GetGameSessionShortAlias(guest, &mmpb.GetGameSessionShortAliasRequest{Name: alias.Name})
	if err != nil || resolved.GetGameSession() != gs.Name {
		t.Fatalf("code lookup failed: %v", err)
	}
	_, err = g.JoinGameSession(guest, &mmpb.JoinGameSessionRequest{Name: resolved.GameSession, Password: "RaidPrivate", UserDefinitions: []*mmpb.UserDefinition{{User: "users/current"}}})
	if err != nil {
		t.Fatal(err)
	}
	query, err := g.QueryGameSessions(guest, &mmpb.QueryGameSessionsRequest{Tenant: "tenants/current", GameSessionSearchConfig: "tenants/current/gameSessionSearchConfigs/RaidPublicSearch", MinVacancyCount: 1})
	if err != nil || len(query.GetGameSessions()) != 0 {
		t.Fatalf("private raid leaked into public board: %v", err)
	}
}

func TestRaidPublicSearchWithoutPostings(t *testing.T) {
	g := newGameSessionServer(newSessionRegistry())
	result, err := g.QueryGameSessions(violetAuthenticatedContext("raid-searcher"), &mmpb.QueryGameSessionsRequest{
		Tenant:                  "tenants/current",
		GameSessionSearchConfig: "tenants/current/gameSessionSearchConfigs/RaidPublicSearch",
		MinVacancyCount:         1,
		PageSize:                20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.GameSessions) != 0 {
		t.Fatalf("unexpected raid postings: %d", len(result.GameSessions))
	}
}

func TestRaidRandomWaitsForPublicHost(t *testing.T) {
	r := newSessionRegistry()
	g, m := newGameSessionServer(r), newMatchmaker(r)
	guest := violetAuthenticatedContext("random-guest")
	ticket, err := m.CreateMatchmakingTicket(guest, &mmpb.CreateMatchmakingTicketRequest{MatchmakingTicket: &mmpb.MatchmakingTicket{
		MatchmakingConfig: "RaidPublic", UserDefinitions: []*mmpb.UserDefinition{{User: "users/current", Team: "participant"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	pending, ok := m.completeMatchmakingTicket(guest, ticket.Name, []string{"users/current"})
	if !ok || pending.State != mmpb.MatchmakingTicket_SEARCHING || pending.GameSession != nil || len(r.sessions) != 0 {
		t.Fatal("empty random search manufactured a raid")
	}
	host := violetAuthenticatedContext("random-host")
	created, err := g.CreateGameSessionCreationTicket(host, &mmpb.CreateGameSessionCreationTicketRequest{GameSessionCreationTicket: &mmpb.GameSessionCreationTicket{
		MatchmakingConfig: "RaidPublic", UserDefinitions: []*mmpb.UserDefinition{{User: "users/current", Team: "owner"}},
		GameSession: &mmpb.GameSession{Properties: &commonpb.MapValue{Fields: map[string]*commonpb.Value{"raid_table_id": gamesyncIntegerValue(4061)}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	room, ok := g.completeGameSessionCreationTicket(host, created.Name, []string{"users/current"})
	if !ok {
		t.Fatal("host creation failed")
	}
	r.sessions[lastResourceSegment(room.GameSession.Name)].CanParticipate = false
	pending, ok = m.completeMatchmakingTicket(guest, ticket.Name, nil)
	if !ok || pending.State != mmpb.MatchmakingTicket_SEARCHING {
		t.Fatal("random selected closed raid")
	}
	r.sessions[lastResourceSegment(room.GameSession.Name)].CanParticipate = true
	matched, ok := m.completeMatchmakingTicket(guest, ticket.Name, []string{"users/current"})
	if !ok || matched.State != mmpb.MatchmakingTicket_SUCCEEDED || matched.GameSession.Name != room.GameSession.Name || matched.GameSession.CurrentParticipantCount != 2 || matched.GameSession.Properties.Fields["raid_table_id"].GetIntegerValue() != 4061 || len(matched.MatchedUserSessions) != 1 || matched.MatchedUserSessions[0].MatchmakingIdToken == "" {
		t.Fatal("random failed to join the live host")
	}
}

func TestRaidPublicHostCreation(t *testing.T) {
	g := newGameSessionServer(newSessionRegistry())
	fields := map[string]*commonpb.Value{
		"difficulty":     gamesyncIntegerValue(4),
		"is_distributed": gamesyncIntegerValue(0),
		"mons_no":        gamesyncIntegerValue(941),
		"raid_table_id":  gamesyncIntegerValue(4061),
	}
	ctx := violetAuthenticatedContext("raid-host")
	created, err := g.CreateGameSessionCreationTicket(ctx, &mmpb.CreateGameSessionCreationTicketRequest{
		Parent: "tenants/current",
		GameSessionCreationTicket: &mmpb.GameSessionCreationTicket{
			MatchmakingConfig: "tenants/current/matchmakingConfigs/RaidPublic",
			UserDefinitions: []*mmpb.UserDefinition{{
				User: "tenants/current/users/current", Team: "owner",
				Attributes: &commonpb.MapValue{Fields: fields},
			}},
			GameSession: &mmpb.GameSession{Properties: &commonpb.MapValue{Fields: fields}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := g.completeGameSessionCreationTicket(ctx, created.Name, []string{"tenants/current/users/current"})
	if !ok || result.GetState() != mmpb.GameSessionCreationTicket_SUCCEEDED {
		t.Fatalf("raid host creation did not succeed: ok=%t state=%s", ok, result.GetState())
	}
	if result.GameSession.MaxParticipantCount != 4 || !result.GameSession.IsPublic ||
		result.GameSession.Properties.Fields["raid_table_id"].GetIntegerValue() != 4061 {
		t.Fatalf("raid host session lost its room contract: %v", result.GameSession)
	}
	query, err := g.QueryGameSessions(violetAuthenticatedContext("raid-guest"), &mmpb.QueryGameSessionsRequest{
		Tenant:                  "tenants/current",
		GameSessionSearchConfig: "tenants/current/gameSessionSearchConfigs/RaidPublicSearch",
		MinVacancyCount:         1,
		Properties: &commonpb.MapValue{Fields: map[string]*commonpb.Value{
			"difficulty": gamesyncIntegerValue(4), "is_distributed": gamesyncIntegerValue(0),
		}},
	})
	if err != nil || len(query.GameSessions) != 1 || query.GameSessions[0].Name != result.GameSession.Name {
		t.Fatalf("raid host not searchable: sessions=%d err=%v", len(query.GameSessions), err)
	}
	g.mu.Lock()
	g.registry.configs[result.GameSession.Name] = violetUnionCircleConfig
	g.mu.Unlock()
	query, err = g.QueryGameSessions(violetAuthenticatedContext("raid-guest"), &mmpb.QueryGameSessionsRequest{
		Tenant:                  "tenants/current",
		GameSessionSearchConfig: "tenants/current/gameSessionSearchConfigs/RaidPublicSearch",
		MinVacancyCount:         1,
	})
	if err != nil || len(query.GameSessions) != 0 {
		t.Fatalf("non-raid room leaked into raid search: sessions=%d err=%v", len(query.GameSessions), err)
	}
}
