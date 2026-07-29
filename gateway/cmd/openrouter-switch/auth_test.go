package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ckorhonen/openrouter-switch/gateway/internal/auth"
)

func setAuthSeams(t *testing.T) {
	t.Helper()
	oldResolve := resolveAuthAPIKey
	oldValidate := validateAuthAPIKey
	oldStore := storeAuthAPIKey
	oldRead := readAuthAPIKey
	t.Cleanup(func() {
		resolveAuthAPIKey = oldResolve
		validateAuthAPIKey = oldValidate
		storeAuthAPIKey = oldStore
		readAuthAPIKey = oldRead
	})
}

func TestAuthStatusPrintsSafeMetadata(t *testing.T) {
	setAuthSeams(t)
	const secret = "sk-or-never-print-this"
	reset := "monthly"
	limit := 100.0
	remaining := 74.5
	expires := time.Date(2027, 12, 31, 23, 59, 59, 0, time.UTC)
	resolveAuthAPIKey = func() (string, auth.Source, error) {
		return secret, auth.SourceKeychain, nil
	}
	validateAuthAPIKey = func(_ context.Context, key string) (auth.KeyMetadata, error) {
		if key != secret {
			t.Fatalf("validated key = %q", key)
		}
		return auth.KeyMetadata{
			Label:          "sk-or-v1-abc...xyz",
			Limit:          &limit,
			LimitRemaining: &remaining,
			LimitReset:     &reset,
			ExpiresAt:      &expires,
		}, nil
	}

	var stdout, stderr bytes.Buffer
	if code := runAuth([]string{"status"}, &stdout, &stderr); code != 0 {
		t.Fatalf("runAuth status = %d\nstderr:\n%s", code, stderr.String())
	}
	for _, want := range []string{
		"Source: keychain",
		"Label: sk-or-v1-abc...xyz",
		"Limit: $100.00",
		"Remaining: $74.50",
		"Reset: monthly",
		"Expires: 2027-12-31T23:59:59Z",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), secret) ||
		strings.Contains(stderr.String(), secret) {
		t.Fatal("auth status printed the API key")
	}
}

func TestAuthStatusReportsMissingCredential(t *testing.T) {
	setAuthSeams(t)
	resolveAuthAPIKey = func() (string, auth.Source, error) {
		return "", "", auth.ErrNoAPIKey
	}
	var stdout, stderr bytes.Buffer
	if code := runAuth([]string{"status"}, &stdout, &stderr); code != 3 {
		t.Fatalf("runAuth status = %d, want 3", code)
	}
	if !strings.Contains(stderr.String(), "auth set-key") {
		t.Fatalf("stderr missing set-key fix:\n%s", stderr.String())
	}
}

func TestAuthStatusDoesNotLeakValidationErrors(t *testing.T) {
	setAuthSeams(t)
	const secret = "sk-or-status-secret"
	resolveAuthAPIKey = func() (string, auth.Source, error) {
		return secret, auth.SourceEnvironment, nil
	}
	validateAuthAPIKey = func(context.Context, string) (auth.KeyMetadata, error) {
		return auth.KeyMetadata{}, errors.New("transport accidentally included " + secret)
	}
	var stdout, stderr bytes.Buffer
	if code := runAuth([]string{"status"}, &stdout, &stderr); code != 1 {
		t.Fatalf("runAuth status = %d, want 1", code)
	}
	if strings.Contains(stderr.String(), secret) {
		t.Fatalf("stderr leaked key:\n%s", stderr.String())
	}
}

func TestAuthUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"bogus"}, {"status", "extra"}, {"set-key", "secret"}} {
		var stdout, stderr bytes.Buffer
		if code := runAuth(args, &stdout, &stderr); code != 2 {
			t.Errorf("runAuth(%v) = %d, want 2", args, code)
		}
	}
}
