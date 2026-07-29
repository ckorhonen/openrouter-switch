package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ckorhonen/openrouter-switch/gateway/internal/auth"
	"github.com/ckorhonen/openrouter-switch/gateway/internal/pricing"
)

func eligibilityGateway(t *testing.T) *Gateway {
	t.Helper()
	const key = "sk-or-eligibility"
	p := pricing.New()
	if err := p.ReplaceOpenRouterCatalog(
		[]byte(`{"data":[
			{"id":"anthropic/claude-tools","name":"Claude Tools",
			 "architecture":{"input_modalities":["text"],"output_modalities":["text"]},
			 "supported_parameters":["tools"],
			 "pricing":{"prompt":"0.000001","completion":"0.000002"}},
			{"id":"openai/no-tools","name":"No Tools",
			 "architecture":{"input_modalities":["text"],"output_modalities":["text"]},
			 "supported_parameters":["temperature"],
			 "pricing":{"prompt":"0.000001","completion":"0.000002"}}
		]}`),
		"openrouter_models_user",
		time.Unix(10, 0),
		"",
	); err != nil {
		t.Fatal(err)
	}
	fingerprint := auth.CredentialFingerprint(key)
	return &Gateway{
		cfg: Config{
			OpenRouterURL:         "https://openrouter.invalid",
			OpenRouterKey:         key,
			CredentialFingerprint: fingerprint,
		},
		pricing:            p,
		client:             &http.Client{Transport: defaultTransport()},
		authFingerprint:    fingerprint,
		catalogFingerprint: fingerprint,
	}
}

func TestAccountModelDiscoveryOffersOnlyEligibleToolsModels(t *testing.T) {
	g := eligibilityGateway(t)
	entries, aliases := g.accountOpenRouterModelEntries(
		g.pricing.Capture(),
		map[string]string{
			"claude-openrouter-custom": "anthropic/claude-tools",
			"claude-openrouter-bad":    "openai/no-tools",
		},
	)
	if len(entries) != 2 {
		t.Fatalf("eligible entries = %+v, want dynamic plus custom alias", entries)
	}
	if aliases["claude-openrouter-custom"] !=
		"anthropic/claude-tools" {
		t.Fatalf("custom aliases = %+v", aliases)
	}
	if aliases[dynamicOpenRouterAlias("anthropic/claude-tools")] !=
		"anthropic/claude-tools" {
		t.Fatalf("dynamic aliases = %+v", aliases)
	}
	if _, found := aliases["claude-openrouter-bad"]; found {
		t.Fatalf("non-tool model became selectable: %+v", aliases)
	}
}

func TestDynamicOpenRouterAliasStableAcrossShortNameCollisions(t *testing.T) {
	firstSlug := "alpha/glm-4.6"
	secondSlug := "beta/glm-4.6"
	firstAlias := dynamicOpenRouterAlias(firstSlug)

	if firstAlias != dynamicOpenRouterAlias(firstSlug) {
		t.Fatal("same slug produced a different dynamic alias")
	}
	if firstAlias == dynamicOpenRouterAlias(secondSlug) {
		t.Fatalf(
			"colliding short names produced the same alias %q",
			firstAlias,
		)
	}

	p := pricing.New()
	if err := p.ReplaceOpenRouterCatalog(
		[]byte(`{"data":[
			{"id":"alpha/glm-4.6","supported_parameters":["tools"],
			 "pricing":{"prompt":"0.000001","completion":"0.000002"}}
		]}`),
		"openrouter_models_user",
		time.Unix(10, 0),
		"",
	); err != nil {
		t.Fatal(err)
	}
	before := eligibleOpenRouterAliases(p.Capture(), nil)
	if before[firstAlias] != firstSlug {
		t.Fatalf("initial aliases = %+v", before)
	}

	if err := p.ReplaceOpenRouterCatalog(
		[]byte(`{"data":[
			{"id":"alpha/glm-4.6","supported_parameters":["tools"],
			 "pricing":{"prompt":"0.000001","completion":"0.000002"}},
			{"id":"beta/glm-4.6","supported_parameters":["tools"],
			 "pricing":{"prompt":"0.000001","completion":"0.000002"}}
		]}`),
		"openrouter_models_user",
		time.Unix(20, 0),
		"",
	); err != nil {
		t.Fatal(err)
	}
	after := eligibleOpenRouterAliases(p.Capture(), nil)
	if after[firstAlias] != firstSlug {
		t.Fatalf("original alias changed after collision: %+v", after)
	}
	if after[dynamicOpenRouterAlias(secondSlug)] != secondSlug {
		t.Fatalf("second colliding slug missing: %+v", after)
	}
}

func TestOpenAIModelDiscoveryUsesEligibleAccountCatalog(t *testing.T) {
	g := eligibilityGateway(t)
	recorder := httptest.NewRecorder()
	g.accountOpenAIModelsGet(recorder)
	var response struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 1 ||
		response.Data[0].ID != "anthropic/claude-tools" {
		t.Fatalf("OpenAI model list = %+v", response.Data)
	}
}

func TestExplicitOpenRouterSelectionRequiresCurrentEligibleCatalog(
	t *testing.T,
) {
	g := eligibilityGateway(t)
	rc := resolvedAnthropicOpenRouter(t)
	rc.ModelOptions = nil
	cl := &clientListener{cfg: rc}
	request := newRequestWithModel(t, "anthropic/claude-tools")
	if _, ok, err := g.resolveExplicitModelAttempt(
		cl,
		request,
		[]byte(`{"model":"anthropic/claude-tools"}`),
		"messages",
	); err != nil || !ok {
		t.Fatalf("eligible explicit model = ok:%t err:%v", ok, err)
	}

	request = newRequestWithModel(t, "openai/no-tools")
	if _, _, err := g.resolveExplicitModelAttempt(
		cl,
		request,
		[]byte(`{"model":"openai/no-tools"}`),
		"messages",
	); err == nil {
		t.Fatal("non-tool explicit model was accepted")
	}

	g.authMu.Lock()
	g.authFingerprint = auth.CredentialFingerprint("sk-or-new")
	g.authMu.Unlock()
	request = newRequestWithModel(t, "anthropic/claude-tools")
	if _, _, err := g.resolveExplicitModelAttempt(
		cl,
		request,
		[]byte(`{"model":"anthropic/claude-tools"}`),
		"messages",
	); err == nil {
		t.Fatal("catalog from an old credential was accepted")
	}
}

func TestConfiguredModelUsesNativeFallbackUntilCatalogMatchesCredential(
	t *testing.T,
) {
	for _, tc := range []struct {
		name               string
		catalogFingerprint string
	}{
		{name: "cold start"},
		{
			name: "credential changed",
			catalogFingerprint: auth.CredentialFingerprint(
				"sk-or-previous",
			),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var openRouterHits atomic.Int32
			openRouter := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) {
					openRouterHits.Add(1)
					w.WriteHeader(http.StatusTeapot)
				},
			))
			defer openRouter.Close()
			var nativeHits atomic.Int32
			native := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) {
					nativeHits.Add(1)
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(
						`{"content":[{"type":"text","text":"native"}]}`,
					))
				},
			))
			defer native.Close()

			cfg := testConfig(t, openRouter.URL, native.URL)
			rc := resolvedAnthropicOpenRouter(t)
			rc.FallbackRoute = pricing.ProviderAnthropic
			g, adminListener, _ := newGateway(t, cfg, rc)
			defer adminListener.Close()
			g.authMu.Lock()
			g.catalogFingerprint = tc.catalogFingerprint
			g.authMu.Unlock()
			stop := start(t, g)
			defer stop()

			resp, body := postModelMessages(
				t,
				g,
				"claude-opus-4-8",
				"",
			)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf(
					"response = %d %s, want native fallback 200",
					resp.StatusCode,
					body,
				)
			}
			if got := openRouterHits.Load(); got != 0 {
				t.Fatalf("OpenRouter hits = %d, want 0", got)
			}
			if got := nativeHits.Load(); got != 1 {
				t.Fatalf("native hits = %d, want 1", got)
			}
			rows := waitForRows(
				t,
				cfg.TelemetryDir,
				1,
				2*time.Second,
			)
			if !rows[0].Fallback.Attempted ||
				rows[0].Fallback.Count != 1 ||
				valueOrZero(rows[0].Fallback.Trigger) !=
					fallbackTriggerCatalogUnavailable {
				t.Fatalf(
					"fallback telemetry = %+v, want catalog unavailable",
					rows[0].Fallback,
				)
			}
		})
	}
}

func TestConfiguredModelWithoutFallbackReturnsCatalogUnavailable503(
	t *testing.T,
) {
	openRouter := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		},
	))
	defer openRouter.Close()
	native := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		},
	))
	defer native.Close()

	cfg := testConfig(t, openRouter.URL, native.URL)
	rc := resolvedAnthropicOpenRouter(t)
	g, adminListener, _ := newGateway(t, cfg, rc)
	defer adminListener.Close()
	g.authMu.Lock()
	g.catalogFingerprint = ""
	g.authMu.Unlock()
	stop := start(t, g)
	defer stop()

	resp, body := postModelMessages(t, g, "claude-opus-4-8", "")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf(
			"response = %d %s, want catalog-unavailable 503",
			resp.StatusCode,
			body,
		)
	}
	if got := resp.Header.Get("X-OpenRouter-Switch"); got !=
		"catalog-unavailable" {
		t.Fatalf(
			"X-OpenRouter-Switch = %q, want catalog-unavailable",
			got,
		)
	}
}

func newRequestWithModel(t *testing.T, model string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(
		http.MethodPost,
		"http://gateway.invalid/v1/messages",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Test-Model", model)
	return request
}
