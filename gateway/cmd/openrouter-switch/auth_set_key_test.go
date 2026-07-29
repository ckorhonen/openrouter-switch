package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ckorhonen/openrouter-switch/gateway/internal/auth"
	"github.com/ckorhonen/openrouter-switch/gateway/internal/pidfile"
)

func setAuthSignalSeam(t *testing.T) *[]int {
	t.Helper()
	pids := &[]int{}
	oldSignal := signalRouter
	signalRouter = func(pid int) error {
		*pids = append(*pids, pid)
		return nil
	}
	t.Cleanup(func() { signalRouter = oldSignal })
	return pids
}

func TestAuthSetKeyValidatesBeforeStoreAndReloadsRouter(t *testing.T) {
	setAuthSeams(t)
	const secret = "sk-or-new-secret"
	var order []string
	readAuthAPIKey = func() (string, error) {
		order = append(order, "read")
		return secret, nil
	}
	validateAuthAPIKey = func(_ context.Context, key string) (auth.KeyMetadata, error) {
		order = append(order, "validate")
		if key != secret {
			t.Fatalf("validated key = %q", key)
		}
		return auth.KeyMetadata{Label: "sk-or-v1-new...cret"}, nil
	}
	storeAuthAPIKey = func(key string) error {
		order = append(order, "store")
		if key != secret {
			t.Fatalf("stored key = %q", key)
		}
		return nil
	}
	pids := setAuthSignalSeam(t)
	pidPath := filepath.Join(t.TempDir(), "gateway.pid")
	t.Setenv("OPENROUTER_SWITCH_GATEWAY_PIDFILE", pidPath)
	if err := pidfile.WriteAt(pidPath, os.Getpid()); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := runAuth([]string{"set-key"}, &stdout, &stderr); code != 0 {
		t.Fatalf("runAuth set-key = %d\nstderr:\n%s", code, stderr.String())
	}
	if strings.Join(order, ",") != "read,validate,store" {
		t.Fatalf("operation order = %v", order)
	}
	if len(*pids) != 1 || (*pids)[0] != os.Getpid() {
		t.Fatalf("signaled pids = %v", *pids)
	}
	if !strings.Contains(stdout.String(), "stored in Keychain") ||
		!strings.Contains(stderr.String(), "router reloaded") {
		t.Fatalf("stdout/stderr missing success:\n%s\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), secret) || strings.Contains(stderr.String(), secret) {
		t.Fatal("set-key printed the API key")
	}
}

func TestAuthSetKeyValidationFailurePreservesPriorKey(t *testing.T) {
	setAuthSeams(t)
	const secret = "sk-or-invalid-secret"
	readAuthAPIKey = func() (string, error) { return secret, nil }
	validateAuthAPIKey = func(context.Context, string) (auth.KeyMetadata, error) {
		return auth.KeyMetadata{}, &auth.ValidationError{StatusCode: 401}
	}
	storeCalls := 0
	storeAuthAPIKey = func(string) error {
		storeCalls++
		return nil
	}
	pids := setAuthSignalSeam(t)

	var stdout, stderr bytes.Buffer
	if code := runAuth([]string{"set-key"}, &stdout, &stderr); code != 1 {
		t.Fatalf("runAuth set-key = %d, want 1", code)
	}
	if storeCalls != 0 || len(*pids) != 0 {
		t.Fatalf("store calls = %d; signals = %v", storeCalls, *pids)
	}
	if !strings.Contains(stderr.String(), "existing Keychain key was not changed") {
		t.Fatalf("stderr missing preservation notice:\n%s", stderr.String())
	}
	if strings.Contains(stderr.String(), secret) {
		t.Fatal("validation failure printed the API key")
	}
}

func TestAuthSetKeyReadAndStoreFailuresDoNotLeakKey(t *testing.T) {
	for _, test := range []struct {
		name     string
		readErr  error
		storeErr error
	}{
		{name: "read", readErr: errors.New("terminal failed")},
		{name: "store", storeErr: errors.New("keychain failed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			setAuthSeams(t)
			const secret = "sk-or-write-secret"
			readAuthAPIKey = func() (string, error) {
				return secret, test.readErr
			}
			validateAuthAPIKey = func(context.Context, string) (auth.KeyMetadata, error) {
				return auth.KeyMetadata{Label: "masked"}, nil
			}
			storeAuthAPIKey = func(string) error { return test.storeErr }
			var stdout, stderr bytes.Buffer
			if code := runAuth([]string{"set-key"}, &stdout, &stderr); code != 1 {
				t.Fatalf("runAuth set-key = %d, want 1", code)
			}
			if strings.Contains(stdout.String(), secret) ||
				strings.Contains(stderr.String(), secret) {
				t.Fatal("failure output printed the API key")
			}
		})
	}
}

func TestAuthSetKeyRouterDownStillSucceeds(t *testing.T) {
	setAuthSeams(t)
	readAuthAPIKey = func() (string, error) { return "sk-or-new", nil }
	validateAuthAPIKey = func(context.Context, string) (auth.KeyMetadata, error) {
		return auth.KeyMetadata{Label: "masked"}, nil
	}
	storeAuthAPIKey = func(string) error { return nil }
	pids := setAuthSignalSeam(t)
	t.Setenv("OPENROUTER_SWITCH_GATEWAY_PIDFILE", filepath.Join(t.TempDir(), "missing.pid"))

	var stdout, stderr bytes.Buffer
	if code := runAuth([]string{"set-key"}, &stdout, &stderr); code != 0 {
		t.Fatalf("runAuth set-key = %d", code)
	}
	if !strings.Contains(stderr.String(), "router not running") || len(*pids) != 0 {
		t.Fatalf("stderr/signals = %q / %v", stderr.String(), *pids)
	}
}
