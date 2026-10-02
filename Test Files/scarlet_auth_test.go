package main

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	authpb "npln.nintendo.net/npln-practice/proto/auth/v1"
)

func TestScarletAndVioletShareTenantButKeepDistinctAppIDs(t *testing.T) {
	if nplnTenantID != "t-50e39f8f-lp1" || !supportedNplnAppID(nplnScarletAppID) || !supportedNplnAppID(nplnAppID) || supportedNplnAppID("0100C2500FC20000") {
		t.Fatal("unexpected supported NPLN title or tenant")
	}
	for _, appID := range []string{nplnAppID, nplnScarletAppID} {
		payload, _ := json.Marshal(map[string]string{"app_id": appID})
		jwt := "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
		ext := &authpb.ExternalIdToken{Token: &authpb.ExternalIdToken_NsaIdToken{NsaIdToken: jwt}}
		if got := appIDFromExternal(ext); got != appID {
			t.Fatalf("app hint: got %q, want %q", got, appID)
		}
		refresh := productionRefreshTokenForApp(1234, nplnTenant+"/users/u-test", appID)
		pid, user, gotAppID, ok := parseProductionRefreshTokenWithApp(refresh)
		if !ok || pid != 1234 || user != nplnTenant+"/users/u-test" || gotAppID != appID {
			t.Fatalf("refresh did not preserve %q", appID)
		}
		access := mintNplnAccessTokenForApp(1234, user, nplnTenant, appID)
		var accessClaims struct {
			NPLN struct {
				AppID string `json:"app_id"`
			} `json:"npln"`
		}
		decodeTestJWT(t, access, &accessClaims)
		if accessClaims.NPLN.AppID != appID {
			t.Fatalf("access token app_id = %q", accessClaims.NPLN.AppID)
		}
		match := mintGssMatchTokenForApp("u-test", nplnTenant, nplnTenant+"/gameSessions/g-test", "us-test", "", "{}", "{}", appID)
		parsed, err := verifyGssMatchToken(match)
		if err != nil {
			t.Fatal(err)
		}
		if appID == nplnScarletAppID && parsed.Game.AppID != appID {
			t.Fatalf("Scarlet match token app_id = %q", parsed.Game.AppID)
		}
		if appID == nplnAppID && parsed.Game.AppID != "" {
			t.Fatalf("Violet match token unexpectedly changed: %q", parsed.Game.AppID)
		}
	}
	if got := appIDFromExternal(nil); got != nplnAppID {
		t.Fatalf("Violet default changed to %q", got)
	}
}

func decodeTestJWT(t *testing.T, token string, out any) {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatal("invalid JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(payload, out); err != nil {
		t.Fatal(err)
	}
}
