package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"

	"github.com/zalando/go-keyring"
)

const (
	keyringService     = "openrouter-switch"
	keyringAccount     = "openrouter"
	envAPIKey          = "OPENROUTER_API_KEY"
	envDisableKeychain = "OPENROUTER_SWITCH_AUTH_NO_KEYRING"
)

type Source string

const (
	SourceKeychain    Source = "keychain"
	SourceEnvironment Source = "environment"
)

var (
	ErrNoAPIKey                    = errors.New("OpenRouter API key is not configured")
	ErrInvalidAPIKey               = errors.New("OpenRouter API key is empty or malformed")
	ErrKeyNotFound                 = keyring.ErrNotFound
	ErrKeychainUnavailable         = errors.New("OpenRouter API key Keychain access failed")
	defaultCredentialStore Keyring = systemKeyring{}
)

// Keyring is the narrow generic-password store contract used by OpenRouter
// Switch. Tests inject an in-memory implementation and never touch Keychain.
type Keyring interface {
	Get(service, account string) (string, error)
	Set(service, account, value string) error
	Delete(service, account string) error
}

type systemKeyring struct{}

func (systemKeyring) Get(service, account string) (string, error) {
	return keyring.Get(service, account)
}

func (systemKeyring) Set(service, account, value string) error {
	return keyring.Set(service, account, value)
}

func (systemKeyring) Delete(service, account string) error {
	return keyring.Delete(service, account)
}

type keychainOperationError struct {
	operation string
	cause     error
}

func (e *keychainOperationError) Error() string {
	return "OpenRouter API key Keychain " + e.operation + " failed"
}

func (e *keychainOperationError) Unwrap() error { return e.cause }

func (e *keychainOperationError) Is(target error) bool {
	return target == ErrKeychainUnavailable
}

// ResolveAPIKey returns Keychain credentials first. OPENROUTER_API_KEY is the
// read-only fallback when the entry is absent or Keychain cannot be read.
func ResolveAPIKey(store Keyring) (string, Source, error) {
	if store == nil {
		return "", "", &keychainOperationError{
			operation: "read",
			cause:     errors.New("credential store is nil"),
		}
	}
	value, err := store.Get(keyringService, keyringAccount)
	switch {
	case err == nil:
		if !validAPIKey(value) {
			return "", "", ErrInvalidAPIKey
		}
		return value, SourceKeychain, nil
	default:
		envValue := os.Getenv(envAPIKey)
		if envValue != "" {
			if !validAPIKey(envValue) {
				return "", "", ErrInvalidAPIKey
			}
			return envValue, SourceEnvironment, nil
		}
		if errors.Is(err, ErrKeyNotFound) {
			return "", "", ErrNoAPIKey
		}
		return "", "", &keychainOperationError{
			operation: "read",
			cause:     err,
		}
	}
}

func ResolveDefaultAPIKey() (string, Source, error) {
	if os.Getenv(envDisableKeychain) == "1" {
		value := os.Getenv(envAPIKey)
		if value == "" {
			return "", "", ErrNoAPIKey
		}
		if !validAPIKey(value) {
			return "", "", ErrInvalidAPIKey
		}
		return value, SourceEnvironment, nil
	}
	return ResolveAPIKey(defaultCredentialStore)
}

func StoreAPIKey(store Keyring, value string) error {
	if !validAPIKey(value) {
		return ErrInvalidAPIKey
	}
	if store == nil {
		return &keychainOperationError{
			operation: "write",
			cause:     errors.New("credential store is nil"),
		}
	}
	if err := store.Set(keyringService, keyringAccount, value); err != nil {
		return &keychainOperationError{operation: "write", cause: err}
	}
	return nil
}

func StoreDefaultAPIKey(value string) error {
	return StoreAPIKey(defaultCredentialStore, value)
}

func DeleteAPIKey(store Keyring) error {
	if store == nil {
		return &keychainOperationError{
			operation: "delete",
			cause:     errors.New("credential store is nil"),
		}
	}
	if err := store.Delete(keyringService, keyringAccount); err != nil &&
		!errors.Is(err, ErrKeyNotFound) {
		return &keychainOperationError{operation: "delete", cause: err}
	}
	return nil
}

func DeleteDefaultAPIKey() error {
	return DeleteAPIKey(defaultCredentialStore)
}

func CredentialFingerprint(value string) string {
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func validAPIKey(value string) bool {
	return value != "" &&
		strings.TrimSpace(value) == value &&
		!strings.ContainsAny(value, "\r\n\x00")
}
