package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mmpb "npln.nintendo.net/npln-practice/proto/matchmaking/v1"
)

func TestDashboardAuthenticatesAndReportsMixedGameLobby(t *testing.T) {
	registry := newSessionRegistry()
	gs := &mmpb.GameSession{
		Name:  nplnTenant + "/gameSessions/dashboard-test",
		State: mmpb.GameSession_ACTIVE, MaxParticipantCount: 2,
		UserSessions: []*mmpb.UserSession{
			{Name: nplnTenant + "/gameSessions/dashboard-test/userSessions/host", User: nplnTenant + "/users/dashboard-host", State: mmpb.UserSession_ACTIVE, Team: "owner"},
			{Name: nplnTenant + "/gameSessions/dashboard-test/userSessions/guest", User: nplnTenant + "/users/dashboard-guest", State: mmpb.UserSession_ACTIVE},
		},
	}
	registry.sessions[lastResourceSegment(gs.Name)] = gs
	registry.configs[gs.Name] = "RaidPublic"
	retain := func(uid string, pid uint64) { retenirIdentite(nplnTenant+"/users/"+uid, pid) }
	retain("dashboard-host", 81001)
	retain("dashboard-guest", 81002)
	g := newGamesyncServer(registry)
	g.rememberSession(gamesyncSession{UID: "dashboard-host", AppID: nplnAppID, IP: "10.0.0.10", GameSession: gs.Name, UserSession: "userSessions/host"})
	g.rememberSession(gamesyncSession{UID: "dashboard-guest", AppID: nplnScarletAppID, IP: "10.0.0.11", GameSession: gs.Name, UserSession: "userSessions/guest"})
	g.streamStarted("host")
	g.streamStarted("guest")
	d := newGameDashboard(g)
	handler := d.handler("test-secret")

	for _, url := range []string{"/api/stats", "/api/stats?key=incorrect"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, url, nil))
		if response.Code != http.StatusUnauthorized || strings.Contains(response.Body.String(), "dashboard-host") {
			t.Fatalf("unauthorized %q: status %d, body %q", url, response.Code, response.Body.String())
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/stats?key=test-secret", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status %d", response.Code)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/stats?key=test-secret", nil))
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || strings.Contains(response.Body.String(), "test-secret") {
		t.Fatalf("GET status %d, body %q", response.Code, response.Body.String())
	}
	var result dashboardStats
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Connected != 2 || result.InLobby != 2 || result.ActiveLobbies != 1 || result.PeakConnected != 2 || result.TotalSessions != 1 || result.Server.AccessKey != nplnTenantID {
		t.Fatalf("wrong summary: %+v", result)
	}
	if len(result.Gatherings) != 1 || result.Gatherings[0].Mode != "raid" || result.Gatherings[0].Max != 2 || result.Gatherings[0].HostPID != 81001 {
		t.Fatalf("wrong lobby: %+v", result.Gatherings)
	}
	if len(result.Players) != 2 || result.Players[0].State != "Violet raid" || result.Players[1].State != "Scarlet raid" || result.Players[0].Gathering != result.Gatherings[0].ID || result.Players[1].Gathering != result.Gatherings[0].ID {
		t.Fatalf("wrong players: %+v", result.Players)
	}

	g.streamEnded("guest")
	remaining := d.stats()
	if remaining.Connected != 1 || remaining.InLobby != 1 || remaining.PeakConnected != 2 {
		t.Fatalf("disconnect not reflected: %+v", remaining)
	}
}

func TestDashboardRejectsPublicBindingAndMissingToken(t *testing.T) {
	d := newGameDashboard(newGamesyncServer())
	t.Setenv("DASH_LISTEN", "0.0.0.0:8109")
	t.Setenv("DASH_TOKEN", "secret")
	if err := d.listenFromEnvironment(); err == nil {
		t.Fatal("accepted public dashboard binding")
	}
	t.Setenv("DASH_LISTEN", "127.0.0.1:8109")
	t.Setenv("DASH_TOKEN", "")
	if err := d.listenFromEnvironment(); err == nil {
		t.Fatal("accepted dashboard without token")
	}
}
