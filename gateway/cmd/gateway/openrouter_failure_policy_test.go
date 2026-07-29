package gateway

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestOpenRouterFallbackStatusPolicy(t *testing.T) {
	for _, status := range []int{
		http.StatusUnauthorized,
		http.StatusPaymentRequired,
		http.StatusForbidden,
	} {
		if fallbackTriggerStatus(status) {
			t.Errorf("status %d unexpectedly enables native fallback", status)
		}
	}
	for _, status := range []int{
		http.StatusTooManyRequests,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
	} {
		if !fallbackTriggerStatus(status) {
			t.Errorf("status %d does not enable native fallback", status)
		}
		if !retryableOpenRouterStatus(status) {
			t.Errorf("status %d is not retried", status)
		}
	}
}

func TestBoundedRetryAfter(t *testing.T) {
	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		value string
		want  time.Duration
		ok    bool
	}{
		{name: "empty", value: "", want: 0, ok: true},
		{name: "invalid", value: "later", want: 0, ok: false},
		{name: "negative", value: "-1", want: 0, ok: false},
		{name: "zero", value: "0", want: 0, ok: true},
		{name: "seconds", value: "1", want: time.Second, ok: true},
		{name: "seconds capped", value: "300", want: retryAfterMaxDelay, ok: true},
		{
			name:  "http date",
			value: now.Add(time.Second).Format(http.TimeFormat),
			want:  time.Second,
			ok:    true,
		},
		{
			name:  "http date capped",
			value: now.Add(time.Hour).Format(http.TimeFormat),
			want:  retryAfterMaxDelay,
			ok:    true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := boundedRetryAfter(tc.value, now)
			if got != tc.want || ok != tc.ok {
				t.Fatalf(
					"boundedRetryAfter(%q) = %s, %t, want %s, %t",
					tc.value,
					got,
					ok,
					tc.want,
					tc.ok,
				)
			}
		})
	}
}

func TestAuthAndPaymentFailuresNeverUseNativeFallback(t *testing.T) {
	for _, status := range []int{
		http.StatusUnauthorized,
		http.StatusPaymentRequired,
		http.StatusForbidden,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var fallbackHits atomic.Int32
			primary := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(status)
				},
			))
			defer primary.Close()
			fallback := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) {
					fallbackHits.Add(1)
					w.WriteHeader(http.StatusOK)
				},
			))
			defer fallback.Close()

			cfg := testConfig(t, primary.URL, fallback.URL)
			rc := resolvedAnthropicOpenRouter(t)
			rc.DefaultModel = "zai-org/GLM-5.2"
			rc.FallbackRoute = "anthropic"
			g, adminListener, _ := newGateway(t, cfg, rc)
			defer adminListener.Close()
			stop := start(t, g)
			defer stop()

			req, err := http.NewRequest(
				http.MethodPost,
				clientURL(g, "claude-code", "/v1/messages"),
				bytes.NewReader([]byte(`{
					"model":"claude-opus-4-8",
					"messages":[{"role":"user","content":"ping"}]
				}`)),
			)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != status {
				t.Fatalf(
					"response status = %d, want %d",
					resp.StatusCode,
					status,
				)
			}
			if hits := fallbackHits.Load(); hits != 0 {
				t.Fatalf("native fallback hits = %d, want 0", hits)
			}
			if status == http.StatusUnauthorized &&
				g.authHealth().Health != "invalid" {
				t.Fatalf(
					"auth health = %q, want invalid",
					g.authHealth().Health,
				)
			}
		})
	}
}

func TestWaitForRetryHonorsRequestBudget(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForRetry(ctx, time.Hour); err != context.Canceled {
		t.Fatalf("waitForRetry error = %v, want context canceled", err)
	}
}
