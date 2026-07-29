package gateway

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ckorhonen/openrouter-switch/gateway/internal/auth"
	"github.com/ckorhonen/openrouter-switch/gateway/internal/pricing"
)

func TestAdminModelCatalogUsesAccountEndpointAndFiltersTools(t *testing.T) {
	key := "sk-or-account"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/key" {
			if got := r.Header.Get("Authorization"); got != "Bearer "+key {
				t.Fatalf("key validation Authorization = %q", got)
			}
			_, _ = w.Write([]byte(
				`{"data":{"label":"sk-or-...account"}}`,
			))
			return
		}
		if r.URL.Path != "/v1/models/user" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+key {
			t.Fatalf("Authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{"data":[
			{"id":"model/tools","name":"Tools","context_length":1000,
			 "architecture":{"input_modalities":["text"],"output_modalities":["text"]},
			 "top_provider":{"max_completion_tokens":100},
			 "supported_parameters":["tools"],"pricing":{"prompt":"0.000001","completion":"0.000002"}},
			{"id":"model/no-tools","name":"No Tools","context_length":1000,
			 "architecture":{"input_modalities":["text"],"output_modalities":["text"]},
			 "top_provider":{"max_completion_tokens":100},
			 "supported_parameters":["temperature"],"pricing":{"prompt":"0.000001","completion":"0.000002"}},
			{"id":"model/image-only","name":"Image Only","context_length":1000,
			 "architecture":{"input_modalities":["text"],"output_modalities":["image"]},
			 "top_provider":{"max_completion_tokens":100},
			 "supported_parameters":["tools"],"pricing":{"prompt":"0.000001","completion":"0.000002"}}
		]}`))
	}))
	defer upstream.Close()

	cfg := Config{
		OpenRouterURL:         upstream.URL,
		OpenRouterKey:         key,
		CredentialSource:      auth.SourceEnvironment,
		CredentialFingerprint: auth.CredentialFingerprint(key),
		AdminAddr:             "127.0.0.1:0",
		ConfigPath:            "",
	}
	listener, err := net.Listen("tcp", cfg.AdminAddr)
	if err != nil {
		t.Fatal(err)
	}
	g, err := New(cfg, pricing.New(), listener, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = g.Shutdown(context.Background())
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/model-catalog", nil)
	rec := httptest.NewRecorder()
	g.adminModelCatalog(rec, req)
	var response modelCatalogResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.State != "ready" || len(response.Models) != 1 {
		t.Fatalf("response = %+v", response)
	}
	if response.Models[0].Slug != "model/tools" ||
		!response.Models[0].ToolCapable {
		t.Fatalf("model = %+v", response.Models[0])
	}
}

func TestAdminModelCatalogMissingCredential(t *testing.T) {
	g := &Gateway{pricing: pricing.New()}
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/model-catalog", nil)
	rec := httptest.NewRecorder()
	g.adminModelCatalog(rec, req)
	var response modelCatalogResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.State != "unavailable" ||
		response.UnavailableReason != modelCatalogUnavailableMissing ||
		len(response.Models) != 0 {
		t.Fatalf("response = %+v", response)
	}
}

func TestCachedCatalogIsNotEligibleForCurrentCredential(t *testing.T) {
	p := pricing.New()
	if err := p.ReplaceOpenRouterCatalog(
		[]byte(`{"data":[{"id":"model/tools","name":"Tools",
			"supported_parameters":["tools"],
			"pricing":{"prompt":"0.000001","completion":"0.000002"}}]}`),
		"openrouter_models_user",
		time.Unix(10, 0),
		"",
	); err != nil {
		t.Fatal(err)
	}
	cache, err := p.ExportProviderCache(pricing.ProviderOpenRouter)
	if err != nil {
		t.Fatal(err)
	}
	restored := pricing.New()
	if err := restored.ImportProviderCache(cache); err != nil {
		t.Fatal(err)
	}
	if got := eligibleModelCatalogModels(restored.Capture()); len(got) != 0 {
		t.Fatalf("cached models became selectable: %+v", got)
	}
}

func TestAdminModelCatalogKeepsCurrentCatalogAfterStickyAuthFailure(
	t *testing.T,
) {
	p := pricing.New()
	if err := p.ReplaceOpenRouterCatalog(
		[]byte(`{"data":[{
			"id":"model/tools",
			"name":"Tools",
			"architecture":{
				"input_modalities":["text"],
				"output_modalities":["text"]
			},
			"supported_parameters":["tools"],
			"pricing":{"prompt":"0.000001","completion":"0.000002"}
		}]}`),
		"openrouter_models_user",
		time.Unix(10, 0),
		"",
	); err != nil {
		t.Fatal(err)
	}
	const key = "sk-or-current"
	fingerprint := auth.CredentialFingerprint(key)
	for _, status := range []string{"invalid", "forbidden"} {
		t.Run(status, func(t *testing.T) {
			g := &Gateway{
				cfg: Config{
					OpenRouterKey:         key,
					CredentialFingerprint: fingerprint,
				},
				pricing:            p,
				authStatus:         "valid",
				authFingerprint:    fingerprint,
				catalogFingerprint: fingerprint,
			}
			if status == "invalid" {
				g.markAuthInvalid(fingerprint)
			} else {
				g.markAuthForbidden(fingerprint)
			}
			req := httptest.NewRequest(
				http.MethodGet,
				"/v1/admin/model-catalog",
				nil,
			)
			rec := httptest.NewRecorder()

			g.adminModelCatalog(rec, req)

			var response modelCatalogResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.State != "ready" ||
				response.UnavailableReason != "" ||
				len(response.Models) != 1 ||
				response.Models[0].Slug != "model/tools" {
				t.Fatalf("response = %+v", response)
			}
		})
	}
}
