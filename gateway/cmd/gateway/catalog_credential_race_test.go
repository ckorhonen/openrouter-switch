package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ckorhonen/openrouter-switch/gateway/internal/auth"
	"github.com/ckorhonen/openrouter-switch/gateway/internal/pricing"
)

func TestCatalogRefreshCannotPublishAcrossCredentialChange(t *testing.T) {
	requestStarted := make(chan struct{})
	releaseResponse := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			close(requestStarted)
			<-releaseResponse
			_, _ = w.Write([]byte(`{"data":[{
				"id":"model/tools",
				"supported_parameters":["tools"],
				"pricing":{"prompt":"0.000001","completion":"0.000002"}
			}]}`))
		},
	))
	defer upstream.Close()

	const oldKey = "sk-or-old"
	const newKey = "sk-or-new"
	oldFingerprint := auth.CredentialFingerprint(oldKey)
	g := &Gateway{
		cfg: Config{
			OpenRouterKey:         oldKey,
			CredentialFingerprint: oldFingerprint,
		},
		client:          upstream.Client(),
		pricing:         pricing.New(),
		authStatus:      "invalid",
		authFingerprint: oldFingerprint,
		authLastErr:     "old credential error",
		authLastErrAt:   time.Unix(10, 0),
	}
	req, err := http.NewRequest(
		http.MethodGet,
		upstream.URL+"/v1/models/user",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		done <- g.fetchAndPublishCatalog(
			upstream.Client(),
			req,
			oldFingerprint,
		)
	}()
	<-requestStarted
	g.cfgMu.Lock()
	g.cfg.OpenRouterKey = newKey
	g.cfg.CredentialFingerprint = auth.CredentialFingerprint(newKey)
	g.cfgMu.Unlock()
	newFingerprint := auth.CredentialFingerprint(newKey)
	g.authMu.Lock()
	g.authStatus = "forbidden"
	g.authFingerprint = newFingerprint
	g.authLastErr = "current credential error"
	g.authLastErrAt = time.Unix(20, 0)
	g.authMu.Unlock()
	close(releaseResponse)

	if err := <-done; err == nil {
		t.Fatal("catalog refresh published after credential change")
	}
	if models := g.pricing.Capture().Models(
		pricing.ProviderOpenRouter,
	); len(models) != 0 {
		t.Fatalf("stale credential catalog became active: %+v", models)
	}
	if health := g.authHealth(); health.Health != "forbidden" ||
		health.Fingerprint != newFingerprint ||
		health.LastError != "current credential error" ||
		!health.LastErrorAt.Equal(time.Unix(20, 0)) ||
		!health.LastOKAt.IsZero() {
		t.Fatalf("stale credential success mutated current auth: %+v", health)
	}
}

func TestCatalogSuccessRecoversRejectedAuthState(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"data":[{
				"id":"model/tools",
				"supported_parameters":["tools"],
				"pricing":{"prompt":"0.000001","completion":"0.000002"}
			}]}`))
		},
	))
	defer upstream.Close()

	const key = "sk-or-current"
	fingerprint := auth.CredentialFingerprint(key)
	for _, status := range []string{"invalid", "forbidden"} {
		t.Run(status, func(t *testing.T) {
			g := &Gateway{
				cfg: Config{
					OpenRouterKey:         key,
					CredentialFingerprint: fingerprint,
				},
				client:          upstream.Client(),
				pricing:         pricing.New(),
				authStatus:      status,
				authFingerprint: fingerprint,
				authLastErr:     "sticky credential error",
				authLastErrAt:   time.Unix(10, 0),
			}
			req, err := http.NewRequest(
				http.MethodGet,
				upstream.URL+"/v1/models/user",
				nil,
			)
			if err != nil {
				t.Fatal(err)
			}

			if err := g.fetchAndPublishCatalog(
				upstream.Client(),
				req,
				fingerprint,
			); err != nil {
				t.Fatal(err)
			}

			health := g.authHealth()
			if health.Health != "valid" ||
				health.Fingerprint != fingerprint ||
				health.LastError != "" ||
				!health.LastErrorAt.IsZero() ||
				health.LastOKAt.IsZero() {
				t.Fatalf("recovered auth = %+v", health)
			}
		})
	}
}

func TestReloadCredentialsReadsConfiguredKeychainResolver(t *testing.T) {
	const newKey = "sk-or-keychain-new"
	g := &Gateway{
		cfg: Config{
			OpenRouterKey: "sk-or-old",
			CredentialResolver: func() (string, auth.Source, error) {
				return newKey, auth.SourceKeychain, nil
			},
		},
	}
	g.reloadCredentials()
	cfg := g.runtimeConfig()
	if cfg.OpenRouterKey != newKey ||
		cfg.CredentialSource != auth.SourceKeychain ||
		cfg.CredentialFingerprint != auth.CredentialFingerprint(newKey) {
		t.Fatalf("reloaded credential = %+v", cfg)
	}
	if health := g.authHealth(); health.Source != auth.SourceKeychain ||
		health.Fingerprint != auth.CredentialFingerprint(newKey) {
		t.Fatalf("reloaded auth health = %+v", health)
	}
}

func TestResolveConfigCredentialUsesCanonicalResolver(t *testing.T) {
	previous := resolveDefaultAPIKey
	t.Cleanup(func() { resolveDefaultAPIKey = previous })
	resolveDefaultAPIKey = func() (string, auth.Source, error) {
		return "sk-or-keychain", auth.SourceKeychain, nil
	}
	t.Setenv("OPENROUTER_API_KEY", "sk-or-environment")

	key, source, err := resolveConfigCredential(Config{
		OpenRouterKey:    "sk-or-config",
		CredentialSource: auth.SourceEnvironment,
	})
	if err != nil {
		t.Fatal(err)
	}
	if key != "sk-or-keychain" || source != auth.SourceKeychain {
		t.Fatalf("credential = %q/%q, want canonical Keychain result", key, source)
	}
}

func TestStaleCredentialFailureCannotMutateCurrentAuthState(t *testing.T) {
	const (
		oldKey = "sk-or-old"
		newKey = "sk-or-new"
	)
	newFingerprint := auth.CredentialFingerprint(newKey)
	g := &Gateway{
		cfg: Config{
			OpenRouterKey:         newKey,
			CredentialFingerprint: newFingerprint,
		},
		authStatus:      "configured",
		authFingerprint: newFingerprint,
	}

	g.markAuthInvalid(auth.CredentialFingerprint(oldKey))
	g.markAuthForbidden(auth.CredentialFingerprint(oldKey))

	if health := g.authHealth(); health.Health != "configured" ||
		health.Fingerprint != newFingerprint {
		t.Fatalf("stale failure mutated current auth: %+v", health)
	}
}

func TestValidateAuthCannotPublishAcrossCredentialChange(t *testing.T) {
	requestStarted := make(chan struct{})
	releaseResponse := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			close(requestStarted)
			<-releaseResponse
			w.WriteHeader(http.StatusUnauthorized)
		},
	))
	defer upstream.Close()

	const (
		oldKey = "sk-or-old"
		newKey = "sk-or-new"
	)
	oldFingerprint := auth.CredentialFingerprint(oldKey)
	newFingerprint := auth.CredentialFingerprint(newKey)
	g := &Gateway{
		cfg: Config{
			OpenRouterURL:         upstream.URL,
			OpenRouterKey:         oldKey,
			CredentialFingerprint: oldFingerprint,
		},
		client:          upstream.Client(),
		authStatus:      "configured",
		authFingerprint: oldFingerprint,
	}
	done := make(chan error, 1)
	go func() {
		done <- g.validateAuth(context.Background())
	}()
	<-requestStarted
	g.cfgMu.Lock()
	g.cfg.OpenRouterKey = newKey
	g.cfg.CredentialFingerprint = newFingerprint
	g.cfgMu.Unlock()
	g.authMu.Lock()
	g.authStatus = "configured"
	g.authFingerprint = newFingerprint
	g.authMu.Unlock()
	close(releaseResponse)

	if err := <-done; err == nil {
		t.Fatal("stale validation unexpectedly succeeded")
	}
	if health := g.authHealth(); health.Health != "configured" ||
		health.Fingerprint != newFingerprint {
		t.Fatalf("stale validation mutated current auth: %+v", health)
	}
}

func TestStaleSuccessfulValidationCannotMutateCurrentAuthState(t *testing.T) {
	requestStarted := make(chan struct{})
	releaseResponse := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			close(requestStarted)
			<-releaseResponse
			_, _ = w.Write([]byte(`{"data":{
				"label":"old key",
				"limit":10,
				"limit_remaining":9
			}}`))
		},
	))
	defer upstream.Close()

	const (
		oldKey = "sk-or-old"
		newKey = "sk-or-new"
	)
	oldFingerprint := auth.CredentialFingerprint(oldKey)
	newFingerprint := auth.CredentialFingerprint(newKey)
	g := &Gateway{
		cfg: Config{
			OpenRouterURL:         upstream.URL,
			OpenRouterKey:         oldKey,
			CredentialFingerprint: oldFingerprint,
		},
		client:          upstream.Client(),
		authStatus:      "configured",
		authFingerprint: oldFingerprint,
	}
	done := make(chan error, 1)
	go func() {
		done <- g.validateAuth(context.Background())
	}()
	<-requestStarted
	g.cfgMu.Lock()
	g.cfg.OpenRouterKey = newKey
	g.cfg.CredentialFingerprint = newFingerprint
	g.cfgMu.Unlock()
	g.authMu.Lock()
	g.authStatus = "forbidden"
	g.authFingerprint = newFingerprint
	g.authMetadata = auth.KeyMetadata{Label: "current key"}
	g.authLastErr = "current credential error"
	g.authLastErrAt = time.Unix(20, 0)
	g.authMu.Unlock()
	close(releaseResponse)

	if err := <-done; err != nil {
		t.Fatalf("stale validation request failed: %v", err)
	}
	health := g.authHealth()
	if health.Health != "forbidden" ||
		health.Fingerprint != newFingerprint ||
		health.Metadata.Label != "current key" ||
		health.LastError != "current credential error" ||
		!health.LastErrorAt.Equal(time.Unix(20, 0)) ||
		!health.LastOKAt.IsZero() {
		t.Fatalf("stale validation mutated current auth: %+v", health)
	}
}
