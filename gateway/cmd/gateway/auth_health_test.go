package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ckorhonen/openrouter-switch/gateway/internal/auth"
)

func TestAuthHealthConfiguredCredential(t *testing.T) {
	key := "sk-or-test"
	g := &Gateway{cfg: Config{
		OpenRouterKey:         key,
		CredentialSource:      auth.SourceEnvironment,
		CredentialFingerprint: auth.CredentialFingerprint(key),
	}}
	g.refreshAuth()
	state := g.authHealth()
	if state.Health != "configured" ||
		state.Source != auth.SourceEnvironment ||
		state.Fingerprint == "" {
		t.Fatalf("state = %+v", state)
	}
}

func TestAuthValidationMarksUnauthorizedWithoutLeakingKey(t *testing.T) {
	key := "sk-or-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+key {
			t.Fatalf("Authorization = %q", got)
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"raw secret body"}`))
	}))
	defer server.Close()
	g := &Gateway{
		cfg: Config{
			OpenRouterURL:         server.URL,
			OpenRouterKey:         key,
			CredentialSource:      auth.SourceEnvironment,
			CredentialFingerprint: auth.CredentialFingerprint(key),
		},
		client: server.Client(),
	}
	g.refreshAuth()
	if err := g.validateAuth(t.Context()); err == nil {
		t.Fatal("validation succeeded")
	}
	state := g.authHealth()
	if state.Health != "invalid" || state.LastError == "" {
		t.Fatalf("state = %+v", state)
	}
	encoded, _ := json.Marshal(authStatusJSON(state))
	if string(encoded) == "" || containsStringForTest(string(encoded), key) ||
		containsStringForTest(string(encoded), "raw secret body") {
		t.Fatalf("unsafe auth JSON: %s", encoded)
	}
}

func TestAuthStatusJSONIncludesSafeMetadata(t *testing.T) {
	limit := 20.0
	remaining := 12.5
	reset := "daily"
	state := authHealthState{
		Health:   "valid",
		Source:   auth.SourceKeychain,
		LastOKAt: time.Unix(10, 0),
		Metadata: auth.KeyMetadata{
			Label:          "sk-or-...abcd",
			Limit:          &limit,
			LimitRemaining: &remaining,
			LimitReset:     &reset,
		},
	}
	got := authStatusJSON(state)
	if got["status"] != "valid" || got["source"] != auth.SourceKeychain ||
		got["label"] != "sk-or-...abcd" {
		t.Fatalf("status = %+v", got)
	}
}

func containsStringForTest(value, target string) bool {
	for i := 0; i+len(target) <= len(value); i++ {
		if value[i:i+len(target)] == target {
			return true
		}
	}
	return false
}
