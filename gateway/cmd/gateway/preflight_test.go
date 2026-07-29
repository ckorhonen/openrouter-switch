package gateway

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ckorhonen/openrouter-switch/gateway/internal/config"
)

// noAuthStore points the auth loader at an empty temp store so preflight
// sees no OpenRouter credential regardless of the developer's real keychain.
func noAuthStore(t *testing.T) {
	t.Helper()
	t.Setenv("OPENROUTER_SWITCH_AUTH_NO_KEYRING", "1")
	t.Setenv("OPENROUTER_SWITCH_AUTH_FILE", filepath.Join(t.TempDir(), "auth.json"))
}

func writeYAML(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gateway.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUnresolvedPlaceholders(t *testing.T) {
	t.Setenv("TEST_PF_SET", "resolved")
	f := &config.File{
		Global: config.Global{
			Auth: map[string]string{"openrouter": "${TEST_PF_SET}"},
		},
		Clients: []config.Client{
			{
				Name:      "claude-code",
				Enabled:   true,
				AuthToken: &config.AuthToken{Header: "authorization", Value: "${TEST_PF_UNSET}"},
			},
			// A disabled client binds nothing, so its references are
			// inert and must not be warned about (the template ships
			// the codex client parked with ${CODEX_AUTH_TOKEN}).
			{
				Name:      "codex",
				Enabled:   false,
				AuthToken: &config.AuthToken{Header: "authorization", Value: "${TEST_PF_PARKED_UNSET}"},
			},
		},
	}
	got := UnresolvedPlaceholders(f)
	if len(got) != 1 || got[0] != "TEST_PF_UNSET" {
		t.Fatalf("UnresolvedPlaceholders = %v, want [TEST_PF_UNSET] (parked client excluded)", got)
	}
	var buf bytes.Buffer
	warnUnresolvedPlaceholders(f, &buf)
	out := buf.String()
	if strings.Count(out, "\n") != 1 {
		t.Fatalf("want exactly one warning line, got:\n%s", out)
	}
	if !strings.Contains(out, "${TEST_PF_UNSET}") {
		t.Fatalf("warning does not name the variable:\n%s", out)
	}
	if !strings.Contains(out, config.EnvFilePath()) {
		t.Fatalf("warning does not name the env file fix:\n%s", out)
	}
	if strings.Contains(out, "TEST_PF_SET") {
		t.Fatalf("resolved placeholder should not be warned about:\n%s", out)
	}
}

// OpenRouter credentials in gateway.yaml are ignored even when their
// placeholders resolve. Only Keychain and OPENROUTER_API_KEY are authorities.
func TestApplyGlobalAuthIgnoresOpenRouterCredential(t *testing.T) {
	noAuthStore(t)
	t.Setenv("TEST_PF_OPENROUTER_SWITCH_KEY", "sk-boot-42")
	t.Setenv("OPENROUTER_API_KEY", "")
	path := writeYAML(t, "global:\n  auth:\n    openrouter: ${TEST_PF_OPENROUTER_SWITCH_KEY}\nclients: []\n")
	cfg := Config{ConfigPath: path}
	applyGlobalAuth(&cfg)
	if got := os.Getenv("OPENROUTER_API_KEY"); got != "" {
		t.Fatalf("gateway.yaml set OPENROUTER_API_KEY = %q", got)
	}
	if cfg.OpenRouterKey != "" {
		t.Fatalf("gateway.yaml configured OpenRouter key = %q", cfg.OpenRouterKey)
	}
}

func TestApplyGlobalAuthUnsetPlaceholderStaysEmpty(t *testing.T) {
	noAuthStore(t)
	t.Setenv("OPENROUTER_API_KEY", "")
	path := writeYAML(t, "global:\n  auth:\n    openrouter: ${TEST_PF_NO_SUCH_KEY}\nclients: []\n")
	cfg := Config{ConfigPath: path}
	applyGlobalAuth(&cfg)
	if got := os.Getenv("OPENROUTER_API_KEY"); got != "" {
		t.Fatalf("OPENROUTER_API_KEY = %q, want empty (placeholder unset)", got)
	}
	if cfg.OpenRouterKey != "" {
		t.Fatalf("cfg.OpenRouterKey = %q, want empty", cfg.OpenRouterKey)
	}
}

func TestApplyGlobalAuthMissingFileIsNoop(t *testing.T) {
	cfg := Config{ConfigPath: filepath.Join(t.TempDir(), "no-such.yaml"), OpenRouterKey: "keep"}
	applyGlobalAuth(&cfg)
	if cfg.OpenRouterKey != "keep" {
		t.Fatalf("cfg.OpenRouterKey = %q, want keep", cfg.OpenRouterKey)
	}
}

func TestOpenRouterRoutedClients(t *testing.T) {
	resolved := []resolvedClientConfig{
		{Name: "claude-code", Route: "openrouter"},
		{Name: "opencode", Route: "openai", FallbackRoute: "openrouter"},
		{Name: "codex", Route: "anthropic"},
		{Name: "mon", Route: "monitor"},
	}
	got := openrouterRoutedClients(resolved)
	want := []string{"claude-code (route: openrouter)", "opencode (fallback_route: openrouter)"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestRunPreflightBanner(t *testing.T) {
	cases := []struct {
		name          string
		resolved      []resolvedClientConfig
		openrouterKey string
		wantBanner    bool
	}{
		{
			name:       "openrouter route without creds warns",
			resolved:   []resolvedClientConfig{{Name: "claude-code", Route: "openrouter"}},
			wantBanner: true,
		},
		{
			name:       "openrouter fallback route without creds warns",
			resolved:   []resolvedClientConfig{{Name: "opencode", Route: "openai", FallbackRoute: "openrouter"}},
			wantBanner: true,
		},
		{
			name:       "passthrough routes never warn",
			resolved:   []resolvedClientConfig{{Name: "claude-code", Route: "anthropic"}, {Name: "codex", Route: "openai"}},
			wantBanner: false,
		},
		{
			name:          "api key suppresses banner",
			resolved:      []resolvedClientConfig{{Name: "claude-code", Route: "openrouter"}},
			openrouterKey: "sk-x",
			wantBanner:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			noAuthStore(t)
			cfg := Config{
				ConfigPath:    filepath.Join(t.TempDir(), "no-such.yaml"),
				OpenRouterKey: tc.openrouterKey,
			}
			var buf bytes.Buffer
			runPreflight(&cfg, tc.resolved, &buf)
			out := buf.String()
			gotBanner := strings.Contains(out, "WARNING: no OpenRouter API key")
			if gotBanner != tc.wantBanner {
				t.Fatalf("banner = %t, want %t; output:\n%s", gotBanner, tc.wantBanner, out)
			}
			if tc.wantBanner {
				if !strings.Contains(out, tc.resolved[0].Name) {
					t.Fatalf("banner does not name client %q:\n%s", tc.resolved[0].Name, out)
				}
				if !strings.Contains(out, "openrouter-switch auth set-key") {
					t.Fatalf("banner does not name the fix:\n%s", out)
				}
			}
		})
	}
}

// TestRunPreflightWarnsPlaceholdersFromFile exercises the full startup
// path: a real gateway.yaml with an unresolved ${VAR} produces a warning
// line, and preflight stays warn-only (no panic, no error).
func TestRunPreflightWarnsPlaceholdersFromFile(t *testing.T) {
	noAuthStore(t)
	path := writeYAML(t, `global:
  routing_enabled: false
  auth:
    openrouter: ${TEST_PF_MISSING_KEY}
clients:
  - name: claude-code
    enabled: true
    bind_addr: 127.0.0.1:0
    protocol_shape: anthropic
    default_model: zai-org/GLM-5.2
`)
	cfg := Config{ConfigPath: path}
	var buf bytes.Buffer
	runPreflight(&cfg, []resolvedClientConfig{{Name: "claude-code", Route: "anthropic"}}, &buf)
	out := buf.String()
	if !strings.Contains(out, "${TEST_PF_MISSING_KEY}") {
		t.Fatalf("expected placeholder warning, got:\n%s", out)
	}
	if strings.Contains(out, "WARNING: no OpenRouter API key") {
		t.Fatalf("passthrough-only client should not trigger the credential banner:\n%s", out)
	}
}
