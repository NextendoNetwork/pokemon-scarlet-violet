package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/peer"
	mmpb "npln.nintendo.net/npln-practice/proto/matchmaking/v1"
)

// The dashboard has its own listener. It must never be exposed through the
// public game REST mux, which also serves unauthenticated endpoints.
type gameDashboard struct {
	gamesync *gamesyncServer
	started  time.Time
	mu       sync.Mutex
	present  map[string]*dashboardPresence
	peak     int
}

type dashboardPresence struct {
	uid, appID, ip string
	pid            uint64
	started        time.Time
	streams        int
}

type dashboardPlayer struct {
	PID           uint64 `json:"pid"`
	Name          string `json:"name"`
	IP            string `json:"ip"`
	State         string `json:"state"`
	Gathering     uint32 `json:"gathering"`
	OnlineSeconds int64  `json:"onlineSeconds"`
	IdleSeconds   int64  `json:"idleSeconds"`
	IsHost        bool   `json:"isHost"`
}

type dashboardMember struct {
	PID  uint64 `json:"pid"`
	Name string `json:"name"`
	Host bool   `json:"host"`
}

type dashboardGathering struct {
	ID       uint32            `json:"id"`
	HostPID  uint64            `json:"hostPid"`
	HostName string            `json:"hostName"`
	Mode     string            `json:"mode"`
	Count    int               `json:"count"`
	Max      int32             `json:"max"`
	State    string            `json:"state"`
	Players  []dashboardMember `json:"players"`
}

type dashboardStats struct {
	ServerTime    string `json:"serverTime"`
	UptimeSeconds int64  `json:"uptimeSeconds"`
	Connected     int    `json:"connected"`
	InLobby       int    `json:"inLobby"`
	ActiveLobbies int    `json:"activeLobbies"`
	PeakConnected int    `json:"peakConnected"`
	TotalSessions int    `json:"totalSessions"`
	Server        struct {
		AccessKey string `json:"accessKey"`
	} `json:"server"`
	Players    []dashboardPlayer    `json:"players"`
	Gatherings []dashboardGathering `json:"gatherings"`
}

func newGameDashboard(g *gamesyncServer) *gameDashboard {
	return &gameDashboard{gamesync: g, started: time.Now(), present: make(map[string]*dashboardPresence)}
}

func dashboardAppID(appID string) string {
	if appID == nplnScarletAppID {
		return nplnScarletAppID
	}
	return nplnAppID // older Violet match tokens omit gamesync.app_id
}

func dashboardGame(appID string) string {
	if appID == nplnScarletAppID {
		return "Scarlet"
	}
	return "Violet"
}

func dashboardPeerIP(ctx context.Context) string {
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		if host, _, err := net.SplitHostPort(p.Addr.String()); err == nil {
			return host
		}
	}
	return ""
}

func dashboardKey(uid, appID string) string { return uid + "\x00" + dashboardAppID(appID) }

func (d *gameDashboard) trackPresence(ctx context.Context) func() {
	uid, err := authenticatedNPLNUID(ctx)
	pid, ok := callerPID(ctx)
	if err != nil || !ok || pid == 0 {
		return func() {}
	}
	appID := callerAppID(ctx)
	key := dashboardKey(uid, appID)
	d.mu.Lock()
	entry := d.present[key]
	if entry == nil {
		entry = &dashboardPresence{uid: uid, appID: appID, pid: pid, started: time.Now()}
		d.present[key] = entry
	}
	entry.streams++
	if ip := dashboardPeerIP(ctx); ip != "" {
		entry.ip = ip
	}
	d.mu.Unlock()
	return func() {
		d.mu.Lock()
		entry.streams--
		if entry.streams == 0 {
			delete(d.present, key)
		}
		d.mu.Unlock()
	}
}

func dashboardSessionID(name string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	id := h.Sum32()
	if id == 0 {
		return 1
	}
	return id
}

func dashboardMode(config string) string {
	switch lastResourceSegment(config) {
	case "RaidPublic", "RaidPrivate":
		return "raid"
	case "BoxTrade":
		return "trade"
	case "NbrSingle", "CasualBattle", "RankBattle", "Competition":
		return "battle"
	case "NbrMulti":
		return "multiple battle"
	case "TeamCircle":
		return "union circle"
	default:
		return "lobby"
	}
}

type dashboardRoom struct {
	name, mode, hostSession string
	max                     int32
}

func (d *gameDashboard) stats() dashboardStats {
	now := time.Now()
	stats := dashboardStats{
		ServerTime: now.Format("15:04:05"), UptimeSeconds: int64(now.Sub(d.started).Seconds()),
		Players: []dashboardPlayer{}, Gatherings: []dashboardGathering{},
	}
	stats.Server.AccessKey = nplnTenantID

	// Copy each source under its own lock; no dashboard request holds a game
	// lock while encoding JSON or talking to the account service.
	d.mu.Lock()
	presences := make([]dashboardPresence, 0, len(d.present))
	for _, p := range d.present {
		presences = append(presences, *p)
	}
	d.mu.Unlock()

	g := d.gamesync
	g.mu.RLock()
	active := make([]gamesyncSession, 0, len(g.sessions))
	for id, s := range g.sessions {
		if g.streamCounts[id] > 0 {
			active = append(active, s)
		}
	}
	g.mu.RUnlock()
	sort.Slice(active, func(i, j int) bool {
		return active[i].CreatedAt.AsTime().After(active[j].CreatedAt.AsTime())
	})

	rooms := make(map[string]dashboardRoom)
	if g.registry != nil {
		r := g.registry
		r.mu.Lock()
		stats.TotalSessions = len(r.sessions)
		for _, gs := range r.sessions {
			if gs.GetState() != mmpb.GameSession_ACTIVE {
				continue
			}
			room := dashboardRoom{name: gs.GetName(), mode: dashboardMode(r.configs[gs.GetName()]), max: gs.GetMaxParticipantCount()}
			for _, u := range gs.GetUserSessions() {
				if u.GetState() == mmpb.UserSession_ACTIVE {
					room.hostSession = u.GetName()
					if u.GetTeam() == "owner" {
						break
					}
				}
			}
			rooms[gs.GetName()] = room
		}
		r.mu.Unlock()
	}

	seen := make(map[string]bool)
	groups := make(map[string]*dashboardGathering)
	for _, s := range active {
		key := dashboardKey(s.UID, s.AppID)
		if seen[key] {
			continue
		}
		pid := pidPourUid(s.UID)
		if pid == 0 {
			continue
		}
		seen[key] = true
		name := fmt.Sprintf("Player-%d", pid)
		p := dashboardPlayer{PID: pid, Name: name, IP: s.IP, State: dashboardGame(s.AppID) + " online"}
		if s.CreatedAt != nil {
			p.OnlineSeconds = max(0, int64(now.Sub(s.CreatedAt.AsTime()).Seconds()))
		}
		if room, ok := rooms[s.GameSession]; ok {
			p.Gathering = dashboardSessionID(room.name)
			p.IsHost = lastResourceSegment(s.UserSession) == lastResourceSegment(room.hostSession)
			p.State = dashboardGame(s.AppID) + " " + room.mode
			group := groups[room.name]
			if group == nil {
				group = &dashboardGathering{ID: p.Gathering, Mode: room.mode, Max: room.max, State: "active", Players: []dashboardMember{}}
				groups[room.name] = group
			}
			group.Players = append(group.Players, dashboardMember{PID: pid, Name: name, Host: p.IsHost})
			group.Count++
			stats.InLobby++
			if p.IsHost {
				group.HostPID, group.HostName = pid, name
			}
		}
		stats.Players = append(stats.Players, p)
	}
	for _, presence := range presences {
		key := dashboardKey(presence.uid, presence.appID)
		if seen[key] {
			continue
		}
		seen[key] = true
		stats.Players = append(stats.Players, dashboardPlayer{
			PID: presence.pid, Name: fmt.Sprintf("Player-%d", presence.pid), IP: presence.ip,
			State: dashboardGame(presence.appID) + " online", OnlineSeconds: max(0, int64(now.Sub(presence.started).Seconds())),
		})
	}
	for _, group := range groups {
		if group.HostPID == 0 && len(group.Players) > 0 {
			group.Players[0].Host = true
			group.HostPID, group.HostName = group.Players[0].PID, group.Players[0].Name
			for i := range stats.Players {
				if stats.Players[i].Gathering == group.ID && stats.Players[i].PID == group.HostPID {
					stats.Players[i].IsHost = true
					break
				}
			}
		}
		stats.Gatherings = append(stats.Gatherings, *group)
	}
	sort.Slice(stats.Players, func(i, j int) bool {
		if stats.Players[i].PID == stats.Players[j].PID {
			return stats.Players[i].State < stats.Players[j].State
		}
		return stats.Players[i].PID < stats.Players[j].PID
	})
	sort.Slice(stats.Gatherings, func(i, j int) bool { return stats.Gatherings[i].ID < stats.Gatherings[j].ID })
	stats.Connected = len(stats.Players)
	stats.ActiveLobbies = len(stats.Gatherings)
	d.mu.Lock()
	if stats.Connected > d.peak {
		d.peak = stats.Connected
	}
	stats.PeakConnected = d.peak
	d.mu.Unlock()
	return stats
}

func (d *gameDashboard) handler(token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/stats", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if len(token) == 0 || subtle.ConstantTimeCompare([]byte(token), []byte(r.URL.Query().Get("key"))) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(d.stats())
	})
	return mux
}

func (d *gameDashboard) listenFromEnvironment() error {
	address := strings.TrimSpace(os.Getenv("DASH_LISTEN"))
	if address == "" {
		return nil
	}
	token := os.Getenv("DASH_TOKEN")
	if token == "" {
		return fmt.Errorf("DASH_TOKEN is required when DASH_LISTEN is set")
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid DASH_LISTEN: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || (!ip.IsPrivate() && !ip.IsLoopback()) {
		return fmt.Errorf("DASH_LISTEN must bind to a private or loopback IP")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listen on DASH_LISTEN: %w", err)
	}
	server := &http.Server{Handler: d.handler(token), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			fatalf("dashboard listener stopped: %v", err)
		}
	}()
	return nil
}
