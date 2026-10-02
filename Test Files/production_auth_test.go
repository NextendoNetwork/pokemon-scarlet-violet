package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	authpb "npln.nintendo.net/npln-practice/proto/auth/v1"
)

func productionProof(t *testing.T, pid uint64, secret []byte) *authpb.ExternalIdToken {
	t.Helper()
	raw := fmt.Sprintf("%d.player.%d", pid, time.Now().Add(time.Hour).Unix())
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("nex:" + raw))
	nx2 := "nx2." + base64.RawURLEncoding.EncodeToString([]byte(raw)) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"nnex":%q}`, nx2)))
	return &authpb.ExternalIdToken{Token: &authpb.ExternalIdToken_NsaIdToken{NsaIdToken: "e30." + payload + ".local"}}
}

func TestProductionIdentityRequiresSignedAccountProof(t *testing.T) {
	t.Setenv("VIOLET_PRODUCTION", "1")
	secret := []byte("this-is-only-a-production-auth-unit-test-secret")
	secretPath := filepath.Join(t.TempDir(), "nextendo-secret")
	if err := os.WriteFile(secretPath, []byte(hex.EncodeToString(secret)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NEXTENDO_SECRET_FILE", secretPath)
	keyPath := filepath.Join(t.TempDir(), "internal-key")
	if err := os.WriteFile(keyPath, []byte("test-internal-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NEXTENDO_INTERNAL_KEY_FILE", keyPath)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Key") != "test-internal-key" || r.URL.Query().Get("pid") != "1234" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"pid":1234,"user_id":"u-testplayer"}`))
	}))
	defer server.Close()
	previousURL := accountBaseURL
	accountBaseURL = server.URL
	t.Cleanup(func() { accountBaseURL = previousURL })

	pid, user, err := productionIdentity(productionProof(t, 1234, secret), nplnTenant)
	if err != nil || pid != 1234 || user != nplnTenant+"/users/u-testplayer" {
		t.Fatalf("signed proof rejected: pid=%d user=%q err=%v", pid, user, err)
	}
	if _, _, err := productionIdentity(nil, nplnTenant); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("missing proof: got %v", err)
	}
	if _, _, err := productionIdentity(productionProof(t, 1234, secret), "tenants/other"); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("foreign tenant: got %v", err)
	}
	wrongSecret := []byte("a-different-test-secret-for-forged-proofs")
	if _, _, err := productionIdentity(productionProof(t, 1234, wrongSecret), nplnTenant); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("forged proof: got %v", err)
	}

	refresh := productionRefreshToken(pid, user)
	if gotPID, gotUser, ok := parseProductionRefreshToken(refresh); !ok || gotPID != pid || gotUser != user {
		t.Fatalf("refresh round trip failed: pid=%d user=%q ok=%t", gotPID, gotUser, ok)
	}
	if _, _, ok := parseProductionRefreshToken(refresh + "x"); ok {
		t.Fatal("tampered refresh token accepted")
	}
	response, err := (&authServer{}).RefreshToken(context.Background(), &authpb.RefreshTokenRequest{User: user, RefreshToken: refresh})
	if err != nil || response.GetToken().GetUser() != user {
		t.Fatalf("valid production refresh rejected: %v", err)
	}
	if _, err := (&authServer{}).RefreshToken(context.Background(), &authpb.RefreshTokenRequest{User: strings.Replace(user, "u-testplayer", "u-other", 1), RefreshToken: refresh}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("user-swapped refresh accepted: %v", err)
	}
}
