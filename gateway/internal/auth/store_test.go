package auth

import (
	"errors"
	"strings"
	"testing"
)

type fakeKeyring struct {
	value       string
	getErr      error
	setErr      error
	deleteErr   error
	setService  string
	setAccount  string
	setValue    string
	deleteCalls int
}

func (f *fakeKeyring) Get(service, account string) (string, error) {
	if service != keyringService || account != keyringAccount {
		return "", errors.New("unexpected keyring locator")
	}
	return f.value, f.getErr
}

func (f *fakeKeyring) Set(service, account, value string) error {
	f.setService = service
	f.setAccount = account
	f.setValue = value
	return f.setErr
}

func (f *fakeKeyring) Delete(service, account string) error {
	if service != keyringService || account != keyringAccount {
		return errors.New("unexpected keyring locator")
	}
	f.deleteCalls++
	return f.deleteErr
}

func TestResolveAPIKeyPrefersKeychain(t *testing.T) {
	t.Setenv(envAPIKey, "env-key")
	store := &fakeKeyring{value: "keychain-key"}
	got, source, err := ResolveAPIKey(store)
	if err != nil || got != "keychain-key" || source != SourceKeychain {
		t.Fatalf("ResolveAPIKey() = %q, %q, %v", got, source, err)
	}
}

func TestResolveAPIKeyFallsBackToEnvironment(t *testing.T) {
	t.Setenv(envAPIKey, "env-key")
	got, source, err := ResolveAPIKey(&fakeKeyring{getErr: ErrKeyNotFound})
	if err != nil || got != "env-key" || source != SourceEnvironment {
		t.Fatalf("ResolveAPIKey() = %q, %q, %v", got, source, err)
	}
}

func TestResolveAPIKeyMissing(t *testing.T) {
	t.Setenv(envAPIKey, "")
	key, source, err := ResolveAPIKey(&fakeKeyring{getErr: ErrKeyNotFound})
	if !errors.Is(err, ErrNoAPIKey) {
		t.Fatalf("ResolveAPIKey error = %v, want ErrNoAPIKey", err)
	}
	if key != "" || source != "" {
		t.Fatalf("ResolveAPIKey() = %q, %q, want empty", key, source)
	}
}

func TestResolveAPIKeyUsesEnvironmentWhenKeychainUnavailable(t *testing.T) {
	const secret = "sk-or-secret-read"
	t.Setenv(envAPIKey, secret)
	key, source, err := ResolveAPIKey(&fakeKeyring{
		getErr: errors.New("backend failed near " + secret),
	})
	if err != nil || key != secret || source != SourceEnvironment {
		t.Fatalf("ResolveAPIKey() = %q, %q, %v", key, source, err)
	}
}

func TestResolveDefaultAPIKeyCanDisableKeychainForIsolatedRuntime(
	t *testing.T,
) {
	previous := defaultCredentialStore
	defaultCredentialStore = &fakeKeyring{
		value: "keychain-key-must-not-be-read",
	}
	t.Cleanup(func() {
		defaultCredentialStore = previous
	})
	t.Setenv(envDisableKeychain, "1")
	t.Setenv(envAPIKey, "preview-env-key")

	key, source, err := ResolveDefaultAPIKey()
	if err != nil || key != "preview-env-key" ||
		source != SourceEnvironment {
		t.Fatalf(
			"ResolveDefaultAPIKey() = %q, %q, %v",
			key,
			source,
			err,
		)
	}
}

func TestResolveAPIKeyReportsKeychainFailureWithoutFallbackOrSecret(t *testing.T) {
	const secret = "sk-or-secret-read"
	t.Setenv(envAPIKey, "")
	_, _, err := ResolveAPIKey(&fakeKeyring{
		getErr: errors.New("backend failed near " + secret),
	})
	if !errors.Is(err, ErrKeychainUnavailable) {
		t.Fatalf("ResolveAPIKey error = %v, want ErrKeychainUnavailable", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked API key: %v", err)
	}
}

func TestStoreAndDeleteAPIKey(t *testing.T) {
	store := &fakeKeyring{}
	if err := StoreAPIKey(store, "sk-or-value"); err != nil {
		t.Fatal(err)
	}
	if store.setService != "openrouter-switch" ||
		store.setAccount != "openrouter" ||
		store.setValue != "sk-or-value" {
		t.Fatalf("Set locator/value = %q, %q, %q", store.setService, store.setAccount, store.setValue)
	}
	if err := DeleteAPIKey(store); err != nil {
		t.Fatal(err)
	}
	if store.deleteCalls != 1 {
		t.Fatalf("Delete calls = %d, want 1", store.deleteCalls)
	}
}

func TestDeleteAPIKeyIsIdempotent(t *testing.T) {
	if err := DeleteAPIKey(&fakeKeyring{deleteErr: ErrKeyNotFound}); err != nil {
		t.Fatalf("DeleteAPIKey = %v, want nil", err)
	}
}

func TestCredentialStoreErrorsNeverContainKey(t *testing.T) {
	const secret = "sk-or-secret-write"
	store := &fakeKeyring{
		setErr:    errors.New("cannot store " + secret),
		deleteErr: errors.New("cannot delete " + secret),
	}
	for name, err := range map[string]error{
		"store":  StoreAPIKey(store, secret),
		"delete": DeleteAPIKey(store),
	} {
		if err == nil {
			t.Fatalf("%s error = nil", name)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("%s error leaked API key: %v", name, err)
		}
	}
}

func TestStoreAPIKeyRejectsEmptyValue(t *testing.T) {
	if err := StoreAPIKey(&fakeKeyring{}, " \n"); !errors.Is(err, ErrInvalidAPIKey) {
		t.Fatalf("StoreAPIKey error = %v, want ErrInvalidAPIKey", err)
	}
}

func TestCredentialFingerprintStableAndNonReversible(t *testing.T) {
	a := CredentialFingerprint("sk-or-one")
	b := CredentialFingerprint("sk-or-one")
	c := CredentialFingerprint("sk-or-two")
	if a == "" || a != b || a == c {
		t.Fatalf("fingerprints = %q, %q, %q", a, b, c)
	}
	if strings.Contains(a, "sk-or-one") {
		t.Fatalf("fingerprint contains API key: %q", a)
	}
}
