package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ckorhonen/openrouter-switch/gateway/internal/auth"
	"github.com/ckorhonen/openrouter-switch/gateway/internal/pricing"
)

const liveCatalogFixture = `{
  "data": [
    {
      "id": "new/model",
      "pricing": {
        "prompt": 0.000002,
        "completion": 0.000003
      }
    }
  ]
}`

const catalogTestKey = "sk-or-catalog-key"

func serveCatalogTestKeyValidation(
	w http.ResponseWriter,
	r *http.Request,
) bool {
	if r.URL.Path != "/v1/key" {
		return false
	}
	_, _ = w.Write([]byte(`{"data":{"label":"sk-or-...test"}}`))
	return true
}

func catalogTestGateway(server *httptest.Server) *Gateway {
	return &Gateway{
		cfg: Config{
			OpenRouterURL: server.URL,
			OpenRouterKey: catalogTestKey,
		},
		pricing:        pricing.New(),
		client:         server.Client(),
		catalogRefresh: newCatalogRefreshManager(),
	}
}

func TestCatalogRefreshPublishesSafeKeyMetadata(t *testing.T) {
	const key = "sk-or-catalog-secret"
	expiresAt := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Authorization"); got != "Bearer "+key {
				t.Errorf("Authorization = %q", got)
			}
			switch r.URL.Path {
			case "/v1/key":
				_, _ = w.Write([]byte(`{"data":{
					"label":"sk-or-...masked",
					"limit":20,
					"limit_remaining":12.5,
					"limit_reset":"monthly",
					"is_free_tier":true,
					"is_management_key":false,
					"is_provisioning_key":true,
					"expires_at":"2027-01-02T03:04:05Z"
				}}`))
			case "/v1/models/user":
				_, _ = w.Write([]byte(liveCatalogFixture))
			default:
				http.NotFound(w, r)
			}
		},
	))
	defer server.Close()

	g := &Gateway{
		cfg: Config{
			OpenRouterURL:         server.URL,
			OpenRouterKey:         key,
			CredentialSource:      auth.SourceKeychain,
			CredentialFingerprint: auth.CredentialFingerprint(key),
		},
		pricing:        pricing.New(),
		client:         server.Client(),
		catalogRefresh: newCatalogRefreshManager(),
	}
	g.refreshAuth()
	g.refreshCatalogOnce(context.Background())

	health := g.authHealth()
	metadata := health.Metadata
	if health.Health != "valid" ||
		health.Source != auth.SourceKeychain ||
		metadata.Label != "sk-or-...masked" ||
		metadata.Limit == nil || *metadata.Limit != 20 ||
		metadata.LimitRemaining == nil ||
		*metadata.LimitRemaining != 12.5 ||
		metadata.LimitReset == nil ||
		*metadata.LimitReset != "monthly" ||
		!metadata.IsFreeTier ||
		metadata.IsManagementKey ||
		!metadata.IsProvisioningKey ||
		metadata.ExpiresAt == nil ||
		!metadata.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("auth health = %+v", health)
	}
	encoded, err := json.Marshal(authStatusJSON(health))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), key) {
		t.Fatalf("auth status leaked API key: %s", encoded)
	}
}

func TestCatalogRefreshPublishesLiveSnapshotWithBearer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveCatalogTestKeyValidation(w, r) {
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models/user" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+catalogTestKey {
			t.Fatalf("Authorization = %q, want Bearer %s", got, catalogTestKey)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Fatalf("Accept = %q", got)
		}
		_, _ = w.Write([]byte(liveCatalogFixture))
	}))
	defer server.Close()

	g := catalogTestGateway(server)
	g.cfg.ConfigPath = filepath.Join(t.TempDir(), "gateway.yaml")
	g.refreshCatalogOnce(context.Background())

	metadata := g.pricing.Capture().OpenRouterMetadata()
	if metadata.Source != "openrouter_models_user" || metadata.ModelCount != 1 {
		t.Fatalf("metadata = %+v", metadata)
	}
	if got := g.pricing.OpenRouterPrice("new/model").Prompt; got != 2 {
		t.Fatalf("live prompt = %v, want 2", got)
	}
	health := g.catalogHealth()
	if !health.LiveHydrated || health.LastAttemptAt.IsZero() ||
		health.LastSuccessAt.IsZero() || health.LastError != "" {
		t.Fatalf("health = %+v", health)
	}
	restored := pricing.New()
	loadProviderCatalogCaches(
		restored,
		g.cfg.ConfigPath,
		configCredentialFingerprint(g.cfg),
	)
	if quote := restored.Quote(
		pricing.ProviderOpenRouter,
		"new/model",
	); !quote.Priced || quote.Price.Prompt != 2 {
		t.Fatalf("restored OpenRouter quote = %+v", quote)
	}
	restoredMetadata := restored.Capture().OpenRouterMetadata()
	if restoredMetadata.Source != "openrouter_models_user" ||
		restoredMetadata.Revision != metadata.Revision ||
		restoredMetadata.ModelCount != 1 ||
		restoredMetadata.PricedModelCount != 1 {
		t.Fatalf(
			"cache-restored OpenRouter metadata = %+v, live = %+v",
			restoredMetadata,
			metadata,
		)
	}
	if err := restored.CheckOpenRouterModel("new/model"); err != nil ||
		restored.OpenRouterModelCount() != 1 ||
		restored.OpenRouterCount() != 1 {
		t.Fatalf(
			"cache-restored OpenRouter catalog APIs disagree: check=%v models=%d prices=%d",
			err,
			restored.OpenRouterModelCount(),
			restored.OpenRouterCount(),
		)
	}
	restoredGateway := &Gateway{pricing: restored}
	restoredHealth := restoredGateway.catalogHealth()
	if !restoredHealth.LiveHydrated ||
		restoredHealth.Source != "openrouter_models_user" ||
		restoredHealth.Revision != metadata.Revision ||
		restoredHealth.ModelCount != 1 {
		t.Fatalf(
			"cache-restored catalog health = %+v",
			restoredHealth,
		)
	}

	otherCredential := pricing.New()
	loadProviderCatalogCaches(
		otherCredential,
		g.cfg.ConfigPath,
		configCredentialFingerprint(Config{
			OpenRouterKey: "different-catalog-key",
		}),
	)
	if metadata := otherCredential.Capture().OpenRouterMetadata(); metadata.ModelCount != 0 {
		t.Fatalf(
			"catalog cache crossed credential boundary: %+v",
			metadata,
		)
	}
}

func TestCatalogRefreshFailureRetainsLastKnownGood(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveCatalogTestKeyValidation(w, r) {
			return
		}
		if calls.Add(1) == 1 {
			_, _ = w.Write([]byte(liveCatalogFixture))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"new/model","pricing":{"prompt":-2}}]}`))
	}))
	defer server.Close()

	g := catalogTestGateway(server)
	g.refreshCatalogOnce(context.Background())
	lastGood := g.pricing.Capture()
	g.refreshCatalogOnce(context.Background())

	if g.pricing.Capture() != lastGood {
		t.Fatal("invalid refresh replaced last-known-good snapshot")
	}
	health := g.catalogHealth()
	if !health.LiveHydrated || health.LastError == "" {
		t.Fatalf("health after failed refresh = %+v", health)
	}
	if !strings.Contains(health.LastError, "nonnegative") {
		t.Fatalf("last error = %q", health.LastError)
	}
}

func TestCatalogRefreshLoopAttemptsImmediatelyAndRespondsToKick(t *testing.T) {
	var calls atomic.Int32
	callCh := make(chan struct{}, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveCatalogTestKeyValidation(w, r) {
			return
		}
		calls.Add(1)
		callCh <- struct{}{}
		_, _ = w.Write([]byte(liveCatalogFixture))
	}))
	defer server.Close()

	g := catalogTestGateway(server)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g.startCatalogRefresh(ctx)
	waitForCatalogCall(t, callCh)
	g.kickCatalogRefresh()
	waitForCatalogCall(t, callCh)
	if got := calls.Load(); got != 2 {
		t.Fatalf("catalog calls = %d, want 2", got)
	}

	cancel()
	select {
	case <-g.catalogRefresh.done:
	case <-time.After(2 * time.Second):
		t.Fatal("catalog refresh loop did not stop")
	}
}

func TestCatalogRefreshStopCancelsInFlightRequest(t *testing.T) {
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveCatalogTestKeyValidation(w, r) {
			return
		}
		close(entered)
		<-r.Context().Done()
	}))
	defer server.Close()

	g := catalogTestGateway(server)
	g.startCatalogRefresh(context.Background())
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("catalog request did not start")
	}
	g.stopCatalogRefresh()
	select {
	case <-g.catalogRefresh.done:
	case <-time.After(2 * time.Second):
		t.Fatal("catalog refresh loop did not stop after cancel")
	}
}

func TestRandomCatalogRefreshDelayWithinBounds(t *testing.T) {
	for range 100 {
		delay := randomCatalogRefreshDelay()
		if delay < catalogRefreshMinDelay || delay > catalogRefreshMaxDelay {
			t.Fatalf("delay %s outside [%s, %s]", delay, catalogRefreshMinDelay, catalogRefreshMaxDelay)
		}
	}
}

func waitForCatalogCall(t *testing.T, calls <-chan struct{}) {
	t.Helper()
	select {
	case <-calls:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for catalog refresh")
	}
}
