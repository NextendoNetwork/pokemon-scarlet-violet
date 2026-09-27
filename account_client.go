package main

// account_client — Bridge to nextendo-account service.
// Reads the unified Nextendo identity and friend graph so Violet accesses the local Nextendo accounts.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

var accountBaseURL = envOr("NEXTENDO_ACCOUNT_URL", "http://127.0.0.1:18080")

var accountHTTP = &http.Client{Timeout: 5 * time.Second}

type nplnFriendData struct {
	PID        uint64         `json:"pid"`
	UserID     string         `json:"user_id"`
	AccountHex string         `json:"account_hex"`
	Name       string         `json:"name"`
	Presence   map[string]any `json:"presence"`
}

type nplnAccountData struct {
	PID        uint64           `json:"pid"`
	UserID     string           `json:"user_id"`
	AccountHex string           `json:"account_hex"`
	Verified   bool             `json:"verified"`
	Friends    []nplnFriendData `json:"friends"`
}

func readInternalKey() (string, error) {
	if path := os.Getenv("NEXTENDO_INTERNAL_KEY_FILE"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		key := strings.TrimSpace(string(data))
		if key == "" {
			return "", fmt.Errorf("internal key file is empty")
		}
		return key, nil
	}
	if productionMode() {
		return "", fmt.Errorf("NEXTENDO_INTERNAL_KEY_FILE is required")
	}
	return os.Getenv("NEXTENDO_INTERNAL_KEY"), nil
}

func accountFriends(pid uint64) (*nplnAccountData, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/internal/npln-friends?pid=%d", accountBaseURL, pid), nil)
	if err != nil {
		return nil, err
	}
	key, err := readInternalKey()
	if err != nil {
		return nil, err
	}
	if key != "" {
		req.Header.Set("X-Internal-Key", key)
	}
	resp, err := accountHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("npln-friends pid=%d: %s", pid, resp.Status)
	}
	var out nplnAccountData
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func nsaToDecimal(nsa string) (string, bool) {
	h := strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(nsa)), "0x"), "nsa:")
	if v, err := strconv.ParseUint(h, 10, 64); err == nil {
		return strconv.FormatUint(v, 10), true
	}
	if h == "" {
		return "", false
	}
	if len(h) > 16 {
		h = h[len(h)-16:] // low 64 bits
	}
	v, err := strconv.ParseUint(h, 16, 64)
	if err != nil {
		return "", false
	}
	return strconv.FormatUint(v, 10), true
}

func resolveNSAToPID(nsa string) (uint64, error) {
	q, ok := nsaToDecimal(nsa)
	if !ok {
		return 0, fmt.Errorf("nsa %q: cannot convert to uint64 decimal", nsa)
	}
	resp, err := accountHTTP.Get(accountBaseURL + "/api/nsa?id=" + url.QueryEscape(q))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("nsa %s: %s", nsa, resp.Status)
	}
	var out struct {
		PID uint64 `json:"pid"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, err
	}
	return out.PID, nil
}
