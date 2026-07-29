package auth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestValidateAPIKey(t *testing.T) {
	const secret = "sk-or-test-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/key" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+secret {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Fatalf("Accept = %q", got)
		}
		io.WriteString(w, `{"data":{"label":"sk-or-v1-abc...xyz","limit":100,"limit_remaining":74.5,"limit_reset":"monthly","is_free_tier":false,"is_management_key":false,"is_provisioning_key":false,"expires_at":"2027-12-31T23:59:59Z"}}`)
	}))
	defer server.Close()

	meta, err := ValidateAPIKey(context.Background(), server.Client(), server.URL, secret)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Label != "sk-or-v1-abc...xyz" ||
		meta.Limit == nil || *meta.Limit != 100 ||
		meta.LimitRemaining == nil || *meta.LimitRemaining != 74.5 ||
		meta.LimitReset == nil || *meta.LimitReset != "monthly" ||
		meta.IsFreeTier ||
		meta.ExpiresAt == nil ||
		!meta.ExpiresAt.Equal(time.Date(2027, 12, 31, 23, 59, 59, 0, time.UTC)) {
		t.Fatalf("metadata = %#v", meta)
	}
}

func TestDefaultKeyValidationEndpoint(t *testing.T) {
	endpoint, err := keyValidationEndpoint(DefaultBaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := endpoint.String(), "https://openrouter.ai/api/v1/key"; got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
}

func TestValidateAPIKeyRejectsUnauthorizedWithoutLeakingResponse(t *testing.T) {
	const secret = "sk-or-test-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"invalid `+secret+`"}}`)
	}))
	defer server.Close()

	_, err := ValidateAPIKey(context.Background(), server.Client(), server.URL, secret)
	if err == nil || !IsValidationStatus(err, http.StatusUnauthorized) {
		t.Fatalf("ValidateAPIKey error = %v", err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "invalid") {
		t.Fatalf("validation error leaked response/key: %v", err)
	}
}

func TestValidateAPIKeyDoesNotLeakTransportError(t *testing.T) {
	const secret = "sk-or-transport-secret"
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial failed with " + secret)
	})}
	_, err := ValidateAPIKey(context.Background(), client, "https://openrouter.test/api", secret)
	if err == nil {
		t.Fatal("ValidateAPIKey error = nil")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("validation error leaked transport/key: %v", err)
	}
}

func TestValidateAPIKeyDoesNotFollowRedirects(t *testing.T) {
	var redirected bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirected = true
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusFound)
	}))
	defer source.Close()

	_, err := ValidateAPIKey(context.Background(), source.Client(), source.URL, "sk-or-test")
	if err == nil || !IsValidationStatus(err, http.StatusFound) {
		t.Fatalf("ValidateAPIKey error = %v, want HTTP 302", err)
	}
	if redirected {
		t.Fatal("validation followed redirect and risked forwarding the key")
	}
}

func TestValidateAPIKeyRejectsMalformedAndOversizedResponses(t *testing.T) {
	for name, body := range map[string]string{
		"malformed": `{"data":`,
		"oversized": strings.Repeat("x", maxKeyMetadataBody+1),
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				io.WriteString(w, body)
			}))
			defer server.Close()
			_, err := ValidateAPIKey(context.Background(), server.Client(), server.URL, "sk-or-test")
			if err == nil {
				t.Fatal("ValidateAPIKey error = nil")
			}
		})
	}
}

func TestValidateAPIKeyRedactsLabelContainingKey(t *testing.T) {
	const secret = "sk-or-full-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"data":{"label":"`+secret+`"}}`)
	}))
	defer server.Close()
	meta, err := ValidateAPIKey(context.Background(), server.Client(), server.URL, secret)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(meta.Label, secret) {
		t.Fatalf("label leaked key: %q", meta.Label)
	}
}

func TestValidateAPIKeyDropsStringMetadataContainingKey(t *testing.T) {
	const secret = "sk-or-full-secret"
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			io.WriteString(
				w,
				`{"data":{"label":"safe","limit_reset":"`+
					secret+`"}}`,
			)
		},
	))
	defer server.Close()
	meta, err := ValidateAPIKey(
		context.Background(),
		server.Client(),
		server.URL,
		secret,
	)
	if err != nil {
		t.Fatal(err)
	}
	if meta.LimitReset != nil {
		t.Fatalf("limit_reset retained secret metadata: %q", *meta.LimitReset)
	}
}
