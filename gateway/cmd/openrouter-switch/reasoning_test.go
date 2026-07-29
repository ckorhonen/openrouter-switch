package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ckorhonen/openrouter-switch/gateway/internal/config"
)

type failingReasoningPreflight struct {
	called bool
}

func (f *failingReasoningPreflight) Check(
	string,
	string,
	string,
	string,
	config.ReasoningPolicy,
) (reasoningPreflightResult, error) {
	f.called = true
	return reasoningPreflightResult{}, fmt.Errorf("preflight must not run")
}

func installReasoningPreflight(t *testing.T, client reasoningPreflightClient) {
	t.Helper()
	old := activeReasoningPreflightClient
	activeReasoningPreflightClient = client
	t.Cleanup(func() { activeReasoningPreflightClient = old })
}

func TestParseReasoningPolicy(t *testing.T) {
	tests := []struct {
		args        []string
		want        config.ReasoningPolicy
		wantDefault bool
		wantErr     bool
	}{
		{args: []string{"openrouter", "zai-org/GLM-5.2", "off"}, want: config.ReasoningPolicy{Mode: config.ReasoningOff}},
		{args: []string{"openrouter", "zai-org/GLM-5.2", "follow-harness"}, want: config.ReasoningPolicy{Mode: config.ReasoningFollowHarness}},
		{args: []string{"openrouter", "deepseek-ai/DeepSeek-V4-Pro", "effort", "high"}, want: config.ReasoningPolicy{Mode: config.ReasoningFixed, Effort: "high"}},
		{args: []string{"openrouter", "zai-org/GLM-5.2", "default"}, wantDefault: true},
		{args: []string{"openai", "gpt-5", "off"}, wantErr: true},
		{args: []string{"openrouter", "model", "effort"}, wantErr: true},
		{args: []string{"openrouter", "model", "off", "high"}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(strings.Join(tc.args, "_"), func(t *testing.T) {
			_, _, got, gotDefault, err := parseReasoningPolicy(tc.args)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %t", err, tc.wantErr)
			}
			if err == nil && (got != tc.want || gotDefault != tc.wantDefault) {
				t.Fatalf("policy/default = %#v/%t, want %#v/%t", got, gotDefault, tc.want, tc.wantDefault)
			}
		})
	}
}

func TestReasoningDefaultIsOfflineSafeAndSkipsPreflight(t *testing.T) {
	path := writeSwitchFixture(t, `global:
  routing_enabled: true
clients:
  - name: claude-code
    enabled: true
    bind_addr: 127.0.0.1:18081
    protocol_shape: anthropic
    default_model: zai-org/GLM-5.2
    model_options:
      openrouter:
        "zai-org/GLM-5.2":
          reasoning:
            mode: follow_harness
`)
	preflight := &failingReasoningPreflight{}
	installReasoningPreflight(t, preflight)
	var out strings.Builder
	rc := runClientReasoning("claude-code", []string{
		"openrouter", "zai-org/GLM-5.2", "default",
		"--operation-id", "reasoning-default-test",
	}, &out)
	if rc != 0 {
		t.Fatalf("rc = %d, output = %s", rc, out.String())
	}
	if preflight.called {
		t.Fatal("default called semantic preflight")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "model_options:") {
		t.Fatalf("default did not remove the client override:\n%s", body)
	}
	if !strings.Contains(out.String(), "claude-code reasoning: openrouter/zai-org/GLM-5.2 -> default") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestReasoningNonDefaultRequiresRunningRouterBeforePreflight(t *testing.T) {
	path := writeSwitchFixture(t, `global:
  routing_enabled: true
clients:
  - name: claude-code
    enabled: true
    bind_addr: 127.0.0.1:18081
    protocol_shape: anthropic
    default_model: zai-org/GLM-5.2
`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	preflight := &failingReasoningPreflight{}
	installReasoningPreflight(t, preflight)
	var out strings.Builder
	rc := runClientReasoning(
		"claude-code",
		[]string{"openrouter", "zai-org/GLM-5.2", "off"},
		&out,
	)
	if rc != 1 {
		t.Fatalf("rc = %d, want 1", rc)
	}
	if preflight.called {
		t.Fatal("preflight called without a running router")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("config changed after refused mutation")
	}
}

func TestHTTPReasoningPreflightUsesGatewaySnapshotAndClientProjection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/admin/reasoning/preflight":
			if r.Method != http.MethodPost {
				t.Errorf("preflight method = %s, want POST", r.Method)
			}
			var request struct {
				Client string                 `json:"client"`
				Policy config.ReasoningPolicy `json:"policy"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.Client != "claude-code" {
				t.Errorf("preflight client = %q", request.Client)
			}
			if request.Policy.Effort == "xhigh" {
				fmt.Fprint(w, `{"available":false,"error":"model does not advertise reasoning effort \"xhigh\"","clients":[]}`)
				return
			}
			fmt.Fprint(w, `{"available":true,"warning":"reasoning metadata is stale but remains validated (captured 2026-07-25T00:00:00Z)","clients":[{"name":"claude-code","reachable":true,"supported":true,"failure_behaviors":[]}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	addr := strings.TrimPrefix(server.URL, "http://")

	got, err := (httpReasoningPreflightClient{}).Check(addr, "claude-code", "openrouter", "deepseek-ai/DeepSeek-V4-Pro", config.ReasoningPolicy{
		Mode: config.ReasoningFixed, Effort: "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Warning, "stale") || !strings.Contains(got.Warning, "2026-07-25") {
		t.Fatalf("warning = %q", got.Warning)
	}

	_, err = (httpReasoningPreflightClient{}).Check(addr, "claude-code", "openrouter", "deepseek-ai/DeepSeek-V4-Pro", config.ReasoningPolicy{
		Mode: config.ReasoningFixed, Effort: "xhigh",
	})
	if err == nil || !strings.Contains(err.Error(), "does not advertise") {
		t.Fatalf("invalid effort error = %v", err)
	}
}

func TestHTTPReasoningPreflightRequiresGatewayContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()
	_, err := (httpReasoningPreflightClient{}).Check(
		strings.TrimPrefix(server.URL, "http://"),
		"claude-code",
		"openrouter",
		"zai-org/GLM-5.2",
		config.ReasoningPolicy{Mode: config.ReasoningOff},
	)
	if err == nil || !strings.Contains(err.Error(), "preflight") {
		t.Fatalf("error = %v", err)
	}
}

func TestRequireEligibleOpenRouterModel(t *testing.T) {
	tests := []struct {
		name     string
		response string
		model    string
		wantCode string
	}{
		{
			name:     "eligible",
			response: `{"state":"ready","models":[{"slug":"org/model","tool_capable":true}]}`,
			model:    "org/model",
		},
		{
			name:     "absent",
			response: `{"state":"ready","models":[]}`,
			model:    "org/model",
			wantCode: "model_unavailable",
		},
		{
			name:     "not tool capable",
			response: `{"state":"ready","models":[{"slug":"org/model","tool_capable":false}]}`,
			model:    "org/model",
			wantCode: "model_unavailable",
		},
		{
			name:     "catalog unavailable",
			response: `{"state":"unavailable","models":[]}`,
			model:    "org/model",
			wantCode: "model_catalog_unavailable",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/admin/model-catalog" {
					http.NotFound(w, r)
					return
				}
				fmt.Fprint(w, tc.response)
			}))
			defer server.Close()
			t.Setenv("OPENROUTER_SWITCH_ADMIN_ADDR", strings.TrimPrefix(server.URL, "http://"))
			got := requireEligibleOpenRouterModel(tc.model)
			if tc.wantCode == "" {
				if got != nil {
					t.Fatalf("error = %v", got)
				}
				return
			}
			if got == nil || got.code != tc.wantCode {
				t.Fatalf("error = %#v, want code %q", got, tc.wantCode)
			}
		})
	}
}

func TestRequireEligibleOpenRouterModelAllowsSynchronousCatalogHydration(
	t *testing.T,
) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/admin/model-catalog" {
			http.NotFound(w, r)
			return
		}
		time.Sleep(600 * time.Millisecond)
		fmt.Fprint(w, `{"state":"ready","models":[{"slug":"org/model","tool_capable":true}]}`)
	}))
	defer server.Close()
	t.Setenv(
		"OPENROUTER_SWITCH_ADMIN_ADDR",
		strings.TrimPrefix(server.URL, "http://"),
	)

	if got := requireEligibleOpenRouterModel("org/model"); got != nil {
		t.Fatalf("delayed catalog eligibility = %v", got)
	}
}

func TestRequireEligibleOpenRouterModelMapsCredentialFailures(t *testing.T) {
	tests := []struct {
		name         string
		reason       string
		wantCode     string
		wantGuidance []string
	}{
		{
			name:         "invalid",
			reason:       "invalid_credentials",
			wantCode:     "invalid_credentials",
			wantGuidance: []string{"auth set-key"},
		},
		{
			name:         "forbidden",
			reason:       "forbidden",
			wantCode:     "forbidden_credentials",
			wantGuidance: []string{"auth status", "auth set-key"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/v1/admin/model-catalog" {
						http.NotFound(w, r)
						return
					}
					fmt.Fprintf(
						w,
						`{"state":"unavailable","unavailable_reason":%q,"models":[]}`,
						tc.reason,
					)
				},
			))
			defer server.Close()
			t.Setenv(
				"OPENROUTER_SWITCH_ADMIN_ADDR",
				strings.TrimPrefix(server.URL, "http://"),
			)

			got := requireEligibleOpenRouterModel("org/model")

			if got == nil || got.code != tc.wantCode || got.retriable {
				t.Fatalf("error = %#v, want code %q non-retriable", got, tc.wantCode)
			}
			for _, guidance := range tc.wantGuidance {
				if !strings.Contains(got.message, guidance) {
					t.Fatalf("message = %q, want %q", got.message, guidance)
				}
			}
		})
	}
}
