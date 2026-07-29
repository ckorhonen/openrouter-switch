package gateway

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/ckorhonen/openrouter-switch/gateway/internal/modelmeta"
	"github.com/ckorhonen/openrouter-switch/gateway/internal/pricing"
)

const (
	modelCatalogUnavailableMissing   = "missing_credentials"
	modelCatalogUnavailableInvalid   = "invalid_credentials"
	modelCatalogUnavailableForbidden = "forbidden"
)

var modelCatalogNow = time.Now

type modelCatalogResponse struct {
	State             string              `json:"state"`
	UnavailableReason string              `json:"unavailable_reason,omitempty"`
	Models            []modelCatalogModel `json:"models"`
	FetchedAt         string              `json:"fetched_at,omitempty"`
	Error             string              `json:"error,omitempty"`
}

type modelCatalogModel struct {
	Slug                string                 `json:"slug"`
	DisplayName         string                 `json:"display_name"`
	ToolCapable         bool                   `json:"tool_capable"`
	ContextTokens       int64                  `json:"context_tokens,omitempty"`
	MaxOutputTokens     int64                  `json:"max_output_tokens,omitempty"`
	InputModalities     []string               `json:"input_modalities,omitempty"`
	OutputModalities    []string               `json:"output_modalities,omitempty"`
	SupportedParameters []string               `json:"supported_parameters,omitempty"`
	Reasoning           *modelCatalogReasoning `json:"reasoning,omitempty"`
}

type modelCatalogReasoning struct {
	Supported  bool                      `json:"supported"`
	Options    []pricing.ReasoningOption `json:"options"`
	Source     string                    `json:"source"`
	LoadedFrom string                    `json:"loaded_from"`
	Revision   string                    `json:"revision"`
	CapturedAt string                    `json:"captured_at"`
	Stale      bool                      `json:"stale"`
}

func (g *Gateway) adminModelCatalog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		g.reject(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	cfg := g.runtimeConfig()
	if cfg.OpenRouterKey == "" {
		writeModelCatalogJSON(w, r.Method, modelCatalogResponse{
			State:             "unavailable",
			UnavailableReason: modelCatalogUnavailableMissing,
			Models:            []modelCatalogModel{},
		})
		return
	}
	if !g.catalogMatchesCredential() {
		health := g.authHealth()
		if health.Health == "invalid" {
			writeModelCatalogJSON(w, r.Method, modelCatalogResponse{
				State:             "unavailable",
				UnavailableReason: modelCatalogUnavailableInvalid,
				Models:            []modelCatalogModel{},
			})
			return
		}
		if health.Health == "forbidden" {
			writeModelCatalogJSON(w, r.Method, modelCatalogResponse{
				State:             "unavailable",
				UnavailableReason: modelCatalogUnavailableForbidden,
				Models:            []modelCatalogModel{},
			})
			return
		}
		g.refreshCatalogOnce(r.Context())
	}
	if !g.catalogMatchesCredential() {
		health := g.authHealth()
		if health.Health == "invalid" {
			writeModelCatalogJSON(w, r.Method, modelCatalogResponse{
				State:             "unavailable",
				UnavailableReason: modelCatalogUnavailableInvalid,
				Models:            []modelCatalogModel{},
			})
			return
		}
		if health.Health == "forbidden" {
			writeModelCatalogJSON(w, r.Method, modelCatalogResponse{
				State:             "unavailable",
				UnavailableReason: modelCatalogUnavailableForbidden,
				Models:            []modelCatalogModel{},
			})
			return
		}
		writeModelCatalogJSON(w, r.Method, modelCatalogResponse{
			State:  "error",
			Models: []modelCatalogModel{},
			Error:  "OpenRouter account model catalog is unavailable",
		})
		return
	}
	snapshot := g.pricing.Capture()
	models := eligibleModelCatalogModels(snapshot)
	metadata := snapshot.OpenRouterMetadata()
	writeModelCatalogJSON(w, r.Method, modelCatalogResponse{
		State:     "ready",
		Models:    models,
		FetchedAt: metadata.FetchedAt.UTC().Format(time.RFC3339),
	})
}

func eligibleModelCatalogModels(snapshot *pricing.Snapshot) []modelCatalogModel {
	if snapshot == nil {
		return []modelCatalogModel{}
	}
	records := snapshot.Models(pricing.ProviderOpenRouter)
	models := make([]modelCatalogModel, 0, len(records))
	for _, record := range records {
		account := record.Availability.Account
		if account == nil ||
			account.Provenance.LoadedFrom != pricing.LoadedFromLive ||
			!record.ToolCapable ||
			!modalityAllowsText(record.InputModalities) ||
			!modalityAllowsText(record.OutputModalities) {
			continue
		}
		models = append(models, modelCatalogModel{
			Slug:                record.CanonicalModelID,
			DisplayName:         record.DisplayName,
			ToolCapable:         true,
			ContextTokens:       record.ContextTokens,
			MaxOutputTokens:     record.MaxOutputTokens,
			InputModalities:     append([]string(nil), record.InputModalities...),
			OutputModalities:    append([]string(nil), record.OutputModalities...),
			SupportedParameters: append([]string(nil), record.SupportedParams...),
			Reasoning: modelCatalogReasoningFromSnapshot(
				snapshot,
				record.CanonicalModelID,
				time.Now().UTC(),
			),
		})
	}
	sort.Slice(models, func(i, j int) bool {
		return models[i].Slug < models[j].Slug
	})
	return models
}

func modelCatalogReasoningFromSnapshot(
	snapshot *pricing.Snapshot,
	canonicalID string,
	now time.Time,
) *modelCatalogReasoning {
	capability, ok := snapshot.ModelReasoning(
		pricing.ProviderOpenRouter,
		canonicalID,
	)
	if !ok {
		return nil
	}
	freshFrom := capability.Provenance.CapturedAt
	return &modelCatalogReasoning{
		Supported:  capability.Supported,
		Options:    capability.Options,
		Source:     capability.Provenance.Source,
		LoadedFrom: string(capability.Provenance.LoadedFrom),
		Revision:   capability.Provenance.Revision,
		CapturedAt: capability.Provenance.CapturedAt.UTC().Format(time.RFC3339),
		Stale: !freshFrom.IsZero() &&
			now.Sub(freshFrom) > publicCatalogStaleAfter,
	}
}

func openrouterModelDisplayName(
	snapshot *pricing.Snapshot,
	canonicalID string,
) string {
	if displayName, ok := snapshot.DisplayName(
		pricing.ProviderOpenRouter,
		canonicalID,
	); ok {
		return displayName
	}
	return modelmeta.ResolveOpenRouter(canonicalID).DisplayName
}

func writeModelCatalogJSON(
	w http.ResponseWriter,
	method string,
	response modelCatalogResponse,
) {
	body, _ := json.Marshal(response)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if method != http.MethodHead {
		_, _ = w.Write(body)
	}
}
