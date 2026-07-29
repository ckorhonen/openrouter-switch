package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ckorhonen/openrouter-switch/gateway/internal/auth"
)

func setupTestDependencies(path string) setupDependencies {
	return setupDependencies{
		loadCredential: func() (string, error) {
			return "valid keychain key (sk-or-v1-abc...xyz)", nil
		},
		setKey:     func(io.Writer) (string, error) { return "new key", nil },
		configPath: func() string { return path },
		stat:       os.Stat,
		initConfig: func(string, bool, io.Writer) int { return 0 },
	}
}

func TestSetupWithValidKeyInitializesConfigAndPrintsNextCommands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.yaml")
	deps := setupTestDependencies(path)
	setKeyCalls := 0
	initCalls := 0
	deps.setKey = func(io.Writer) (string, error) {
		setKeyCalls++
		return "", nil
	}
	deps.initConfig = func(gotPath string, force bool, _ io.Writer) int {
		initCalls++
		if gotPath != path || force {
			t.Fatalf("initConfig(%q, %v), want (%q, false)", gotPath, force, path)
		}
		return 0
	}

	var stdout, stderr bytes.Buffer
	if code := runSetup(deps, &stdout, &stderr); code != 0 {
		t.Fatalf("runSetup = %d\nstderr:\n%s", code, stderr.String())
	}
	if setKeyCalls != 0 || initCalls != 1 {
		t.Fatalf("set-key calls = %d, init calls = %d", setKeyCalls, initCalls)
	}
	if !strings.Contains(stdout.String(), "Gateway config: created "+path) {
		t.Fatalf("stdout missing config creation:\n%s", stdout.String())
	}
	assertSetupNextCommands(t, stdout.String())
}

func TestSetupPromptsForMissingKeyWithoutLegacyCLI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.yaml")
	deps := setupTestDependencies(path)
	deps.loadCredential = func() (string, error) {
		return "", errSetupCredentialUnavailable
	}
	setKeyCalls := 0
	deps.setKey = func(out io.Writer) (string, error) {
		setKeyCalls++
		fmtFprintForTest(out, "secure prompt")
		return "valid Keychain key (masked)", nil
	}

	var stdout, stderr bytes.Buffer
	if code := runSetup(deps, &stdout, &stderr); code != 0 {
		t.Fatalf("runSetup = %d\nstderr:\n%s", code, stderr.String())
	}
	if setKeyCalls != 1 {
		t.Fatalf("set-key calls = %d, want 1", setKeyCalls)
	}
	if !strings.Contains(stdout.String(), "enter a key") ||
		!strings.Contains(stdout.String(), "valid Keychain key (masked)") {
		t.Fatalf("stdout missing key flow:\n%s", stdout.String())
	}
	if strings.Contains(strings.ToLower(stdout.String()+stderr.String()), "baseten") ||
		strings.Contains(strings.ToLower(stdout.String()+stderr.String()), "oauth") {
		t.Fatalf("setup retained Baseten/OAuth dependency:\n%s\n%s", stdout.String(), stderr.String())
	}
}

func TestSetupStopsWhenSetKeyFails(t *testing.T) {
	deps := setupTestDependencies(filepath.Join(t.TempDir(), "gateway.yaml"))
	deps.loadCredential = func() (string, error) {
		return "", errSetupCredentialUnavailable
	}
	deps.setKey = func(io.Writer) (string, error) {
		return "", errors.New("validation failed")
	}
	initCalls := 0
	deps.initConfig = func(string, bool, io.Writer) int {
		initCalls++
		return 0
	}

	var stdout, stderr bytes.Buffer
	if code := runSetup(deps, &stdout, &stderr); code != 1 {
		t.Fatalf("runSetup = %d, want 1", code)
	}
	if initCalls != 0 {
		t.Fatalf("init calls = %d, want 0", initCalls)
	}
	if !strings.Contains(stderr.String(), "validation or storage failed") {
		t.Fatalf("stderr missing key failure:\n%s", stderr.String())
	}
}

func TestSetupKeepsExistingConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.yaml")
	original := []byte("existing: config\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	deps := setupTestDependencies(path)
	initCalls := 0
	deps.initConfig = func(string, bool, io.Writer) int {
		initCalls++
		return 0
	}

	var stdout, stderr bytes.Buffer
	if code := runSetup(deps, &stdout, &stderr); code != 0 {
		t.Fatalf("runSetup = %d\nstderr:\n%s", code, stderr.String())
	}
	if initCalls != 0 {
		t.Fatalf("init calls = %d, want 0", initCalls)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, original) {
		t.Fatalf("existing config changed: %q", after)
	}
}

func TestSetupReportsConfigInitializationFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.yaml")
	deps := setupTestDependencies(path)
	deps.initConfig = func(string, bool, io.Writer) int { return 1 }
	var stdout, stderr bytes.Buffer
	if code := runSetup(deps, &stdout, &stderr); code != 1 {
		t.Fatalf("runSetup = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "could not initialize gateway config at "+path) {
		t.Fatalf("stderr missing config failure:\n%s", stderr.String())
	}
}

func TestLoadCurrentSetupCredentialUsesOpenRouterStore(t *testing.T) {
	setAuthSeams(t)
	resolveAuthAPIKey = func() (string, auth.Source, error) {
		return "sk-or-secret", auth.SourceEnvironment, nil
	}
	validateAuthAPIKey = func(context.Context, string) (auth.KeyMetadata, error) {
		return auth.KeyMetadata{Label: "sk-or-v1-abc...xyz"}, nil
	}
	got, err := loadCurrentSetupCredential()
	if err != nil {
		t.Fatal(err)
	}
	if got != "valid environment key (sk-or-v1-abc...xyz)" {
		t.Fatalf("credential description = %q", got)
	}
}

func TestSetupRejectsFlags(t *testing.T) {
	var code int
	stderr := captureStderr(t, func() { code = cmdSetup([]string{"--force"}) })
	if code != 2 || !strings.Contains(stderr, "usage: openrouter-switch setup") {
		t.Fatalf("code/stderr = %d / %q", code, stderr)
	}
}

func assertSetupNextCommands(t *testing.T, output string) {
	t.Helper()
	want := "Next commands:\n" +
		"openrouter-switch up --install\n" +
		"openrouter-switch menubar\n" +
		"select an account model in the app, then turn OpenRouter routing on\n" +
		"openrouter-switch claude on\n" +
		"openrouter-switch codex route <account-model>\n" +
		"openrouter-switch codex on\n" +
		"openrouter-switch doctor --probe\n"
	if !strings.HasSuffix(output, want) {
		t.Fatalf("setup next commands differ\nwant suffix:\n%s\ngot:\n%s", want, output)
	}
}

func fmtFprintForTest(out io.Writer, value string) {
	_, _ = io.WriteString(out, value)
}
