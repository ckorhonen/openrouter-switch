package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultBaseURL     = "https://openrouter.ai/api"
	maxKeyMetadataBody = 1 << 20
)

// KeyMetadata is the safe subset of GET /api/v1/key returned by CLI and admin
// status surfaces. Label is OpenRouter's masked label, never the credential.
type KeyMetadata struct {
	Label             string     `json:"label"`
	Limit             *float64   `json:"limit"`
	LimitRemaining    *float64   `json:"limit_remaining"`
	LimitReset        *string    `json:"limit_reset"`
	IsFreeTier        bool       `json:"is_free_tier"`
	IsManagementKey   bool       `json:"is_management_key"`
	IsProvisioningKey bool       `json:"is_provisioning_key"`
	ExpiresAt         *time.Time `json:"expires_at"`
}

type ValidationError struct {
	StatusCode int
	kind       string
	cause      error
}

func (e *ValidationError) Error() string {
	switch {
	case e.StatusCode == http.StatusUnauthorized:
		return "OpenRouter rejected the API key"
	case e.StatusCode != 0:
		return fmt.Sprintf("OpenRouter key validation returned HTTP %d", e.StatusCode)
	case e.kind != "":
		return "OpenRouter key validation " + e.kind
	default:
		return "OpenRouter key validation failed"
	}
}

func (e *ValidationError) Unwrap() error { return e.cause }

func IsValidationStatus(err error, status int) bool {
	var validationErr *ValidationError
	return errors.As(err, &validationErr) &&
		validationErr.StatusCode == status
}

func ValidateAPIKey(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	apiKey string,
) (KeyMetadata, error) {
	if !validAPIKey(apiKey) {
		return KeyMetadata{}, ErrInvalidAPIKey
	}
	if client == nil {
		client = http.DefaultClient
	}
	endpoint, err := keyValidationEndpoint(baseURL)
	if err != nil {
		return KeyMetadata{}, &ValidationError{
			kind:  "configuration is invalid",
			cause: err,
		}
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		endpoint.String(),
		nil,
	)
	if err != nil {
		return KeyMetadata{}, &ValidationError{
			kind:  "request could not be built",
			cause: err,
		}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	clientCopy := *client
	clientCopy.CheckRedirect = func(
		_ *http.Request,
		_ []*http.Request,
	) error {
		return http.ErrUseLastResponse
	}
	resp, err := clientCopy.Do(req)
	if err != nil {
		return KeyMetadata{}, &ValidationError{
			kind:  "request failed",
			cause: err,
		}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxKeyMetadataBody+1))
	if err != nil {
		return KeyMetadata{}, &ValidationError{
			kind:  "response could not be read",
			cause: err,
		}
	}
	if len(body) > maxKeyMetadataBody {
		return KeyMetadata{}, &ValidationError{
			kind: "response was too large",
		}
	}
	if resp.StatusCode < http.StatusOK ||
		resp.StatusCode >= http.StatusMultipleChoices {
		return KeyMetadata{}, &ValidationError{StatusCode: resp.StatusCode}
	}

	var envelope struct {
		Data *KeyMetadata `json:"data"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&envelope); err != nil {
		return KeyMetadata{}, &ValidationError{
			kind:  "response was invalid",
			cause: err,
		}
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return KeyMetadata{}, &ValidationError{
			kind:  "response was invalid",
			cause: err,
		}
	}
	if envelope.Data == nil {
		return KeyMetadata{}, &ValidationError{
			kind: "response omitted key metadata",
		}
	}
	metadata := *envelope.Data
	metadata.Label = safeKeyLabel(metadata.Label, apiKey)
	metadata.LimitReset = safeMetadataString(
		metadata.LimitReset,
		apiKey,
	)
	return metadata, nil
}

func keyValidationEndpoint(baseURL string) (*url.URL, error) {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultBaseURL
	}
	endpoint, err := url.Parse(strings.TrimRight(baseURL, "/") + "/v1/key")
	if err != nil {
		return nil, err
	}
	if (endpoint.Scheme != "http" && endpoint.Scheme != "https") ||
		endpoint.Host == "" {
		return nil, errors.New("OpenRouter base URL must be HTTP(S)")
	}
	return endpoint, nil
}

func safeKeyLabel(label, apiKey string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return "configured key"
	}
	if len(label) > 128 || strings.Contains(label, apiKey) {
		return "configured key"
	}
	return label
}

func safeMetadataString(value *string, apiKey string) *string {
	if value == nil {
		return nil
	}
	safe := strings.TrimSpace(*value)
	if safe == "" || len(safe) > 128 ||
		strings.Contains(safe, apiKey) {
		return nil
	}
	return &safe
}
