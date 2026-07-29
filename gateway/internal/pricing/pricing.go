package pricing

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const maxOpenRouterCatalogBytes = 32 << 20

// Price stores provider rates in USD per one million tokens.
type Price struct {
	Prompt       float64 `json:"input"`
	Completion   float64 `json:"output"`
	CacheRead    float64 `json:"cache_read"`
	CacheWrite5m float64 `json:"cache_write_5m"`
	CacheWrite1h float64 `json:"cache_write_1h"`
}

// RatePresence distinguishes an explicitly priced zero from a rate dimension
// omitted by the source. Every persisted provider-cache quote includes this
// metadata.
type RatePresence struct {
	Input        bool `json:"input"`
	Output       bool `json:"output"`
	CacheRead    bool `json:"cache_read"`
	CacheWrite5m bool `json:"cache_write_5m"`
	CacheWrite1h bool `json:"cache_write_1h"`
}

// Quote is one immutable model-price lookup. Callers retain this value for the
// lifetime of a request, then apply final usage without consulting a newer
// catalog.
type Quote struct {
	Price             Price
	Priced            bool
	RatePresence      RatePresence
	RatePresenceKnown bool
	RateProvenance    RateProvenance
	Source            string
	Revision          string
	CapturedAt        time.Time
	ExecutionProfile  ExecutionProfile
}

var ErrUnpriced = errors.New("pricing quote is unpriced")

// NanoUSDRates stores integer nano-USD rates per token for telemetry v1.
type NanoUSDRates struct {
	Prompt       int64
	Completion   int64
	CacheRead    int64
	CacheWrite5m int64
	CacheWrite1h int64
}

// NanoUSDRates converts the captured USD-per-million rates into checked
// integer nano-USD-per-token rates using the telemetry-v1 rounding rule.
func (q Quote) NanoUSDRates() (NanoUSDRates, error) {
	if !q.Priced {
		return NanoUSDRates{}, ErrUnpriced
	}
	values := []float64{
		q.Price.Prompt,
		q.Price.Completion,
		q.Price.CacheRead,
		q.Price.CacheWrite5m,
		q.Price.CacheWrite1h,
	}
	converted := make([]int64, len(values))
	for index, value := range values {
		rate, err := nanoUSDPerToken(value)
		if err != nil {
			return NanoUSDRates{}, err
		}
		converted[index] = rate
	}
	return NanoUSDRates{
		Prompt:       converted[0],
		Completion:   converted[1],
		CacheRead:    converted[2],
		CacheWrite5m: converted[3],
		CacheWrite1h: converted[4],
	}, nil
}

// HasRatesForUsage reports whether every nonzero token dimension has a known
// rate. In-memory fallback quotes without source presence metadata are treated
// as complete.
func (q Quote) HasRatesForUsage(
	in,
	out,
	cacheRead,
	cacheWrite5m,
	cacheWrite1h int64,
) bool {
	if !q.Priced {
		return false
	}
	if !q.RatePresenceKnown {
		return true
	}
	return (in == 0 || q.RatePresence.Input) &&
		(out == 0 || q.RatePresence.Output) &&
		(cacheRead == 0 || q.RatePresence.CacheRead) &&
		(cacheWrite5m == 0 || q.RatePresence.CacheWrite5m) &&
		(cacheWrite1h == 0 || q.RatePresence.CacheWrite1h)
}

// CostNanoUSD applies usage with checked integer multiplication and addition.
func (rates NanoUSDRates) CostNanoUSD(
	in,
	out,
	cacheRead,
	cacheWrite5m,
	cacheWrite1h int64,
) (int64, error) {
	tokens := []int64{in, out, cacheRead, cacheWrite5m, cacheWrite1h}
	values := []int64{
		rates.Prompt,
		rates.Completion,
		rates.CacheRead,
		rates.CacheWrite5m,
		rates.CacheWrite1h,
	}
	var total int64
	for index, tokenCount := range tokens {
		if tokenCount < 0 {
			return 0, fmt.Errorf("token count must be nonnegative")
		}
		rate := values[index]
		if rate < 0 {
			return 0, fmt.Errorf("nano-USD rate must be nonnegative")
		}
		if tokenCount != 0 && rate > math.MaxInt64/tokenCount {
			return 0, fmt.Errorf("nano-USD cost overflow")
		}
		part := tokenCount * rate
		if part > math.MaxInt64-total {
			return 0, fmt.Errorf("nano-USD cost overflow")
		}
		total += part
	}
	return total, nil
}

func nanoUSDPerToken(usdPerMillion float64) (int64, error) {
	if math.IsNaN(usdPerMillion) || math.IsInf(usdPerMillion, 0) || usdPerMillion < 0 {
		return 0, fmt.Errorf("USD-per-million rate must be finite and nonnegative")
	}
	value := usdPerMillion * 1000
	rounded := math.Round(value)
	// float64 cannot distinguish the final 1,024 integer values below
	// MaxInt64. Reject that boundary conservatively rather than risk an
	// implementation-dependent out-of-range float-to-int conversion.
	if rounded >= float64(math.MaxInt64) {
		return 0, fmt.Errorf("USD-per-million rate %g overflows nano-USD-per-token", usdPerMillion)
	}
	if usdPerMillion > 0 && rounded == 0 {
		return 0, fmt.Errorf("positive USD-per-million rate %g rounds to zero nano-USD per token", usdPerMillion)
	}
	return int64(rounded), nil
}

// CostUSD applies observed usage to this quote. An unpriced quote returns zero;
// callers must inspect Priced to distinguish unknown cost from a priced zero.
func (q Quote) CostUSD(
	in,
	out,
	cacheRead,
	cacheWrite5m,
	cacheWrite1h int64,
) float64 {
	return costUSD(
		q.Price,
		in,
		out,
		cacheRead,
		cacheWrite5m,
		cacheWrite1h,
	)
}

// CatalogMetadata describes the OpenRouter catalog held by a Snapshot.
type CatalogMetadata struct {
	Source           string
	Provenance       string
	Revision         string
	FetchedAt        time.Time
	ModelCount       int
	PricedModelCount int
}

// Snapshot is immutable after publication. Its maps and slices are private so
// a captured request cannot accidentally mutate the catalog shared by other
// requests.
type Snapshot struct {
	providerLayers            map[providerLayerKey]providerCatalog
	providerCatalogs          map[string]providerCatalog
	officialPricingSupplement officialPricingSupplement
}

// Quote returns a price from this exact snapshot.
func (s *Snapshot) Quote(route, model string) Quote {
	return s.QuoteProfile(route, model, ProfileStandard)
}

// OpenRouterMetadata returns metadata for this exact snapshot.
func (s *Snapshot) OpenRouterMetadata() CatalogMetadata {
	if s == nil {
		return CatalogMetadata{}
	}
	return activeOpenRouterPricingCatalog(s.providerLayers).metadata
}

// OpenRouterModels returns a copy of the catalog's sorted model IDs.
func (s *Snapshot) OpenRouterModels() []string {
	if s == nil {
		return nil
	}
	return activeOpenRouterPricingCatalog(s.providerLayers).models
}

// Pricing atomically publishes immutable pricing snapshots.
type Pricing struct {
	current atomic.Pointer[Snapshot]

	publishMu sync.Mutex
}

// New constructs an empty OpenRouter catalog. Account-scoped live data is the
// only authority allowed to populate OpenRouter models and prices.
func New() *Pricing {
	p := &Pricing{}
	supplement, err := parseOfficialPricingSupplement(
		officialPricingSupplementJSON,
	)
	if err != nil {
		panic("invalid embedded official pricing supplement: " + err.Error())
	}
	snapshot := &Snapshot{
		providerLayers:            map[providerLayerKey]providerCatalog{},
		officialPricingSupplement: supplement,
	}
	snapshot.providerCatalogs = activeProviderCatalogsForSnapshot(snapshot)
	p.current.Store(snapshot)
	return p
}

// NewWithPrices constructs a static OpenRouter snapshot for tests and embedders.
func NewWithPrices(openrouter map[string]Price) *Pricing {
	p := New()
	p.publishMu.Lock()
	defer p.publishMu.Unlock()
	openrouterCopy := clonePrices(openrouter)
	models := sortedModelIDs(openrouterCopy)
	snapshot := &Snapshot{
		providerLayers: cloneProviderLayers(p.current.Load().providerLayers),
		officialPricingSupplement: cloneOfficialPricingSupplement(
			p.current.Load().officialPricingSupplement,
		),
	}
	capturedAt := time.Now().UTC()
	if len(openrouterCopy) > 0 {
		snapshot.providerLayers[providerLayerKey{
			provider: ProviderOpenRouter, loadedFrom: LoadedFromLive, source: "static",
		}] = staticOpenRouterProviderCatalog(openrouterCopy, models, capturedAt)
	}
	snapshot.providerCatalogs = activeProviderCatalogsForSnapshot(snapshot)
	p.publishLocked(snapshot)
	return p
}

// Capture returns the currently published immutable pricing snapshot.
func (p *Pricing) Capture() *Snapshot {
	if p == nil {
		return nil
	}
	return p.current.Load()
}

// Quote performs a one-shot lookup against one atomically loaded snapshot.
func (p *Pricing) Quote(route, model string) Quote {
	return p.Capture().Quote(route, model)
}

func (p *Pricing) HydrateFromOpenRouter(baseURL, apiKey, expectedModel string) error {
	url := strings.TrimRight(baseURL, "/") + "/v1/models/user"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("authorization", "Bearer "+apiKey)
	req.Header.Set("accept", "application/json")
	client := &http.Client{Timeout: 20 * time.Second}
	return p.hydrateFromRequest(client, req, expectedModel)
}

func (p *Pricing) HydrateFromOpenRouterClient(client *http.Client, baseURL, expectedModel string) error {
	url := strings.TrimRight(baseURL, "/") + "/v1/models/user"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("accept", "application/json")
	return p.hydrateFromRequest(client, req, expectedModel)
}

func (p *Pricing) hydrateFromRequest(client *http.Client, req *http.Request, expectedModel string) error {
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("openrouter /v1/models/user returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxOpenRouterCatalogBytes+1))
	if err != nil {
		return err
	}
	if len(body) > maxOpenRouterCatalogBytes {
		return fmt.Errorf("openrouter /v1/models/user response exceeds %d bytes", maxOpenRouterCatalogBytes)
	}
	return p.ReplaceOpenRouterCatalog(body, "openrouter_models_user", time.Now().UTC(), expectedModel)
}

// ReplaceOpenRouterCatalog validates a complete /v1/models/user response and
// atomically replaces the live OpenRouter catalog. Any error leaves the
// last-known-good snapshot untouched.
func (p *Pricing) ReplaceOpenRouterCatalog(body []byte, source string, fetchedAt time.Time, expectedModel string) error {
	candidate, err := parseOpenRouterCatalog(body, source, fetchedAt)
	if err != nil {
		return err
	}
	if expectedModel != "" && !containsSorted(candidate.models, expectedModel) {
		return fmt.Errorf("expected model %q not present in OpenRouter /v1/models/user catalog", expectedModel)
	}

	p.publishMu.Lock()
	defer p.publishMu.Unlock()
	current := p.current.Load()
	snapshot := &Snapshot{
		providerLayers: cloneProviderLayers(current.providerLayers),
		officialPricingSupplement: cloneOfficialPricingSupplement(
			current.officialPricingSupplement,
		),
	}
	provenance := Provenance{
		Source: candidate.source, LoadedFrom: LoadedFromLive,
		Revision: candidate.revision, CapturedAt: candidate.fetchedAt,
	}
	snapshot.providerLayers[providerLayerKey{
		provider: ProviderOpenRouter, loadedFrom: LoadedFromLive, source: candidate.source,
	}] = openrouterProviderCatalog(candidate, provenance, "", true)
	snapshot.providerCatalogs = activeProviderCatalogsForSnapshot(snapshot)
	p.publishLocked(snapshot)
	return nil
}

func (p *Pricing) publishLocked(snapshot *Snapshot) {
	if snapshot.providerLayers == nil {
		snapshot.providerLayers = map[providerLayerKey]providerCatalog{}
	}
	if snapshot.providerCatalogs == nil {
		snapshot.providerCatalogs = activeProviderCatalogsForSnapshot(snapshot)
	}
	p.current.Store(snapshot)
}

type openrouterCandidate struct {
	prices       map[string]Price
	ratePresence map[string]RatePresence
	models       []string
	metadata     map[string]openrouterModelMetadata
	diagnostics  []string
	source       string
	revision     string
	fetchedAt    time.Time
}

type openrouterCatalogModel struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	ContextLength       int64    `json:"context_length"`
	SupportedParameters []string `json:"supported_parameters"`
	Architecture        struct {
		InputModalities  []string `json:"input_modalities"`
		OutputModalities []string `json:"output_modalities"`
	} `json:"architecture"`
	TopProvider struct {
		MaxCompletionTokens int64 `json:"max_completion_tokens"`
	} `json:"top_provider"`
	Reasoning json.RawMessage `json:"reasoning"`
	Pricing   json.RawMessage `json:"pricing"`
}

type openrouterModelMetadata struct {
	displayName         string
	contextTokens       int64
	maxOutputTokens     int64
	inputModalities     []string
	outputModalities    []string
	supportedParameters []string
	reasoningSupported  bool
	reasoningOptions    []ReasoningOption
}

func openrouterProviderCatalog(
	candidate openrouterCandidate,
	provenance Provenance,
	provenanceDescription string,
	replacesPricing bool,
) providerCatalog {
	records := make(map[string]ModelRecord, len(candidate.models))
	authenticatedAvailability :=
		provenance.Source == "openrouter_models_user" ||
			provenance.Source == "openrouter-models-user"
	for _, id := range candidate.models {
		metadata := candidate.metadata[id]
		availability := ModelAvailability{}
		if authenticatedAvailability {
			availability.Account = &AvailabilityEvidence{
				State:      AvailabilityAvailable,
				Scope:      AvailabilityScopeUnscopedLastObserved,
				Provenance: provenance,
			}
		}
		record := ModelRecord{
			Provider: ProviderOpenRouter, CanonicalModelID: id,
			DisplayName:      metadata.displayName,
			ContextTokens:    metadata.contextTokens,
			MaxOutputTokens:  metadata.maxOutputTokens,
			InputModalities:  append([]string(nil), metadata.inputModalities...),
			OutputModalities: append([]string(nil), metadata.outputModalities...),
			SupportedParams:  append([]string(nil), metadata.supportedParameters...),
			ToolCapable:      containsString(metadata.supportedParameters, "tools"),
			Availability:     availability,
			Profiles: map[ExecutionProfile]ProfileDefinition{
				ProfileStandard: {
					Profile: ProfileStandard, Supported: true, Provenance: provenance,
				},
			},
			Prices:     map[ExecutionProfile]PriceProfile{},
			Provenance: provenance,
		}
		if metadata.reasoningSupported {
			record.Reasoning = &ReasoningCapability{
				Supported:  true,
				Options:    cloneReasoningOptions(metadata.reasoningOptions),
				Provenance: provenance,
			}
		}
		if price, ok := candidate.prices[id]; ok {
			record.Prices[ProfileStandard] = PriceProfile{
				Profile: ProfileStandard, Price: price,
				RatePresence: candidate.ratePresence[id], RatePresenceKnown: true,
				Provenance: provenance,
				RateProvenance: rateProvenanceForPresence(
					candidate.ratePresence[id],
					provenance,
				),
			}
		}
		records[id] = record
	}
	return providerCatalog{
		metadata: ProviderMetadata{
			Provider: ProviderOpenRouter, Provenance: provenance,
			ModelCount: len(records), PricedModelCount: len(candidate.prices),
			Diagnostics: append([]string(nil), candidate.diagnostics...),
		},
		models:                      records,
		replacesAccountAvailability: authenticatedAvailability,
		replacesPricing:             replacesPricing,
		openrouterPricing: &openrouterPricingCatalog{
			metadata: CatalogMetadata{
				Source:           candidate.source,
				Provenance:       provenanceDescription,
				Revision:         provenance.Revision,
				FetchedAt:        candidate.fetchedAt,
				ModelCount:       len(candidate.models),
				PricedModelCount: len(candidate.prices),
			},
			models: append([]string(nil), candidate.models...),
		},
	}
}

func staticOpenRouterProviderCatalog(
	prices map[string]Price,
	models []string,
	capturedAt time.Time,
) providerCatalog {
	revision := priceTableRevision(prices)
	provenance := Provenance{
		Source: "static", LoadedFrom: LoadedFromLive,
		Revision: revision, CapturedAt: capturedAt,
	}
	records := make(map[string]ModelRecord, len(prices))
	for id, price := range prices {
		records[id] = ModelRecord{
			Provider: ProviderOpenRouter, CanonicalModelID: id, DisplayName: id,
			Profiles: map[ExecutionProfile]ProfileDefinition{
				ProfileStandard: {
					Profile: ProfileStandard, Supported: true, Provenance: provenance,
				},
			},
			Prices: map[ExecutionProfile]PriceProfile{
				ProfileStandard: {
					Profile: ProfileStandard, Price: price,
					RatePresence: RatePresence{
						Input:        true,
						Output:       true,
						CacheRead:    true,
						CacheWrite5m: true,
						CacheWrite1h: true,
					},
					RatePresenceKnown: true,
					Provenance:        provenance,
					RateProvenance: rateProvenanceForPresence(
						RatePresence{
							Input:        true,
							Output:       true,
							CacheRead:    true,
							CacheWrite5m: true,
							CacheWrite1h: true,
						},
						provenance,
					),
				},
			},
			Provenance: provenance,
		}
	}
	return providerCatalog{
		metadata: ProviderMetadata{
			Provider: ProviderOpenRouter, Provenance: provenance,
			ModelCount: len(records), PricedModelCount: len(records),
		},
		models:          records,
		replacesPricing: true,
		openrouterPricing: &openrouterPricingCatalog{
			metadata: CatalogMetadata{
				Source: "static", Provenance: "static",
				Revision: revision, FetchedAt: capturedAt,
				ModelCount: len(models), PricedModelCount: len(prices),
			},
			models: append([]string(nil), models...),
		},
	}
}

func parseOpenRouterCatalog(body []byte, source string, fetchedAt time.Time) (openrouterCandidate, error) {
	var data struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return openrouterCandidate{}, fmt.Errorf("decode OpenRouter catalog: %w", err)
	}
	if len(data.Data) == 0 {
		return openrouterCandidate{}, fmt.Errorf("OpenRouter catalog contains no models")
	}

	prices := make(map[string]Price, len(data.Data))
	ratePresence := make(map[string]RatePresence, len(data.Data))
	metadata := make(map[string]openrouterModelMetadata, len(data.Data))
	models := make([]string, 0, len(data.Data))
	seen := make(map[string]bool, len(data.Data))
	diagnostics := make([]string, 0)
	for _, rawModel := range data.Data {
		var identity struct {
			ID json.RawMessage `json:"id"`
		}
		if err := json.Unmarshal(rawModel, &identity); err != nil {
			diagnostics = append(
				diagnostics,
				"excluded malformed OpenRouter model with unusable id: entry must be an object",
			)
			continue
		}
		var rawID string
		if len(identity.ID) == 0 ||
			json.Unmarshal(identity.ID, &rawID) != nil {
			diagnostics = append(
				diagnostics,
				"excluded malformed OpenRouter model with unusable id: id must be a non-empty string",
			)
			continue
		}
		id := strings.TrimSpace(rawID)
		if id == "" {
			diagnostics = append(
				diagnostics,
				"excluded malformed OpenRouter model with unusable id: id must be a non-empty string",
			)
			continue
		}
		if seen[id] {
			return openrouterCandidate{}, fmt.Errorf("OpenRouter catalog contains duplicate model %q", id)
		}
		seen[id] = true

		modelMetadata, price, presence, priced, err :=
			parseOpenRouterCatalogModel(rawModel, id)
		if err != nil {
			diagnostics = append(
				diagnostics,
				fmt.Sprintf(
					"excluded malformed OpenRouter model %q: %v",
					id,
					err,
				),
			)
			continue
		}
		models = append(models, id)
		metadata[id] = modelMetadata
		if priced {
			prices[id] = price
			ratePresence[id] = presence
		}
	}
	sort.Strings(models)
	sort.Strings(diagnostics)
	if len(models) == 0 {
		return openrouterCandidate{}, fmt.Errorf(
			"OpenRouter catalog contains no usable models: %s",
			strings.Join(diagnostics, "; "),
		)
	}
	if source == "" {
		source = "openrouter-models-user"
	}
	return openrouterCandidate{
		prices:       prices,
		ratePresence: ratePresence,
		models:       models,
		metadata:     metadata,
		diagnostics:  diagnostics,
		source:       source,
		revision:     catalogRevisionWithRatePresence(models, prices, ratePresence),
		fetchedAt:    fetchedAt,
	}, nil
}

func parseOpenRouterCatalogModel(
	rawModel json.RawMessage,
	id string,
) (
	openrouterModelMetadata,
	Price,
	RatePresence,
	bool,
	error,
) {
	var model openrouterCatalogModel
	if err := json.Unmarshal(rawModel, &model); err != nil {
		return openrouterModelMetadata{}, Price{}, RatePresence{}, false,
			fmt.Errorf("decode metadata: invalid field type")
	}
	if model.ContextLength < 0 || model.TopProvider.MaxCompletionTokens < 0 {
		return openrouterModelMetadata{}, Price{}, RatePresence{}, false,
			fmt.Errorf("contains negative token limits")
	}
	parameters, err := normalizedUniqueStrings(
		model.SupportedParameters,
		"supported parameter",
	)
	if err != nil {
		return openrouterModelMetadata{}, Price{}, RatePresence{}, false, err
	}
	inputModalities, err := normalizedUniqueStrings(
		model.Architecture.InputModalities,
		"input modality",
	)
	if err != nil {
		return openrouterModelMetadata{}, Price{}, RatePresence{}, false, err
	}
	outputModalities, err := normalizedUniqueStrings(
		model.Architecture.OutputModalities,
		"output modality",
	)
	if err != nil {
		return openrouterModelMetadata{}, Price{}, RatePresence{}, false, err
	}
	displayName := strings.TrimSpace(model.Name)
	if displayName == "" {
		displayName = id
	}
	reasoningSupported, reasoningOptions, err := parseOpenRouterReasoning(
		model.Reasoning,
		parameters,
	)
	if err != nil {
		return openrouterModelMetadata{}, Price{}, RatePresence{}, false,
			fmt.Errorf("reasoning: %w", err)
	}
	modelMetadata := openrouterModelMetadata{
		displayName:         displayName,
		contextTokens:       model.ContextLength,
		maxOutputTokens:     model.TopProvider.MaxCompletionTokens,
		inputModalities:     inputModalities,
		outputModalities:    outputModalities,
		supportedParameters: parameters,
		reasoningSupported:  reasoningSupported,
		reasoningOptions:    reasoningOptions,
	}
	price, presence, priced, err := parseOpenRouterPrice(model.Pricing)
	if err != nil {
		return openrouterModelMetadata{}, Price{}, RatePresence{}, false,
			fmt.Errorf("pricing: %w", err)
	}
	return modelMetadata, price, presence, priced, nil
}

var allOpenRouterReasoningEfforts = []string{
	"max",
	"xhigh",
	"high",
	"medium",
	"low",
	"minimal",
	"none",
}

func parseOpenRouterReasoning(
	raw json.RawMessage,
	supportedParameters []string,
) (bool, []ReasoningOption, error) {
	advertised := containsString(supportedParameters, "reasoning") ||
		containsString(supportedParameters, "include_reasoning")
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		if !advertised {
			return false, nil, nil
		}
		return true, []ReasoningOption{{Type: ReasoningToggle}}, nil
	}

	var fields struct {
		SupportedEfforts  json.RawMessage `json:"supported_efforts"`
		SupportsMaxTokens bool            `json:"supports_max_tokens"`
		Mandatory         bool            `json:"mandatory"`
	}
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return false, nil, err
	}
	options := make([]ReasoningOption, 0, 3)
	if !fields.Mandatory {
		options = append(options, ReasoningOption{Type: ReasoningToggle})
	}
	if len(fields.SupportedEfforts) != 0 {
		efforts := allOpenRouterReasoningEfforts
		if !bytes.Equal(
			bytes.TrimSpace(fields.SupportedEfforts),
			[]byte("null"),
		) {
			var decoded []string
			if err := json.Unmarshal(fields.SupportedEfforts, &decoded); err != nil {
				return false, nil, fmt.Errorf(
					"supported_efforts is not an array or null",
				)
			}
			var err error
			efforts, err = normalizedUniqueStrings(
				decoded,
				"reasoning effort",
			)
			if err != nil {
				return false, nil, err
			}
			for _, effort := range efforts {
				if !validReasoningEffort(effort) {
					return false, nil, fmt.Errorf(
						"reasoning effort %q is invalid",
						effort,
					)
				}
			}
			if !fields.Mandatory &&
				!containsString(efforts, "none") {
				efforts = append(efforts, "none")
			}
		}
		values := make([]*string, 0, len(efforts))
		for _, effort := range efforts {
			value := strings.Clone(effort)
			values = append(values, &value)
		}
		options = append(options, ReasoningOption{
			Type:   ReasoningEffort,
			Values: values,
		})
	}
	if fields.SupportsMaxTokens {
		options = append(options, ReasoningOption{
			Type: ReasoningBudgetTokens,
		})
	}
	for _, option := range options {
		if err := validateReasoningOption(option); err != nil {
			return false, nil, err
		}
	}
	return true, options, nil
}

func parseOpenRouterPrice(raw json.RawMessage) (Price, RatePresence, bool, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return Price{}, RatePresence{}, false, nil
	}
	var fields struct {
		Prompt          json.RawMessage `json:"prompt"`
		Completion      json.RawMessage `json:"completion"`
		InputCacheRead  json.RawMessage `json:"input_cache_read"`
		InputCacheWrite json.RawMessage `json:"input_cache_write"`
		CacheRead       json.RawMessage `json:"cache_read"`
		CacheWrite      json.RawMessage `json:"cache_write"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return Price{}, RatePresence{}, false, err
	}
	if !ratePresent(fields.InputCacheRead) {
		fields.InputCacheRead = fields.CacheRead
	}
	if !ratePresent(fields.InputCacheWrite) {
		fields.InputCacheWrite = fields.CacheWrite
	}
	values := []struct {
		name    string
		raw     json.RawMessage
		out     *float64
		present *bool
	}{
		{name: "prompt", raw: fields.Prompt},
		{name: "completion", raw: fields.Completion},
		{name: "input_cache_read", raw: fields.InputCacheRead},
		{name: "input_cache_write", raw: fields.InputCacheWrite},
	}
	var price Price
	var presence RatePresence
	values[0].out = &price.Prompt
	values[1].out = &price.Completion
	values[2].out = &price.CacheRead
	values[3].out = &price.CacheWrite5m
	values[0].present = &presence.Input
	values[1].present = &presence.Output
	values[2].present = &presence.CacheRead
	values[3].present = &presence.CacheWrite5m
	for _, value := range values {
		perToken, present, err := openRouterOptionalRate(value.raw)
		if err != nil {
			return Price{}, RatePresence{}, false, fmt.Errorf("%s: %w", value.name, err)
		}
		*value.out = perToken * 1e6
		*value.present = present
	}
	return price, presence, presence.Input && presence.Output, nil
}

// OpenRouter publishes -1 for variable-price router models whose token rates
// are not meaningful at catalog time. Keep the model available to the account,
// but treat that individual rate as unknown. Other negative values remain
// malformed.
func openRouterOptionalRate(
	raw json.RawMessage,
) (float64, bool, error) {
	if !ratePresent(raw) {
		return 0, false, nil
	}
	value, err := optionalFloat(raw)
	if err != nil {
		return 0, false, err
	}
	if value == -1 {
		return 0, false, nil
	}
	if value < 0 {
		return 0, false, fmt.Errorf("must be finite and nonnegative or -1")
	}
	return value, true, nil
}

func normalizedUniqueStrings(values []string, field string) ([]string, error) {
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("%s is empty", field)
		}
		if _, exists := seen[value]; exists {
			return nil, fmt.Errorf("%s %q is duplicated", field, value)
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func containsString(values []string, target string) bool {
	index := sort.SearchStrings(values, target)
	return index < len(values) && values[index] == target
}

func strictOptionalFloat(raw json.RawMessage) (float64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, nil
	}
	value, err := optionalFloat(raw)
	if err != nil {
		return 0, err
	}
	if value < 0 {
		return 0, fmt.Errorf("must be finite and nonnegative")
	}
	return value, nil
}

func optionalFloat(raw json.RawMessage) (float64, error) {
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		value, err := number.Float64()
		if err != nil {
			return 0, fmt.Errorf("invalid number")
		}
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return 0, fmt.Errorf("must be finite")
		}
		return value, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, fmt.Errorf("must be a number or numeric string")
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("must be finite")
	}
	return value, nil
}

func (p *Pricing) CheckOpenRouterModel(expectedModel string) error {
	if expectedModel == "" {
		return nil
	}
	if !containsSorted(p.Capture().OpenRouterModels(), expectedModel) {
		return fmt.Errorf("expected model %q not present in OpenRouter /v1/models/user catalog", expectedModel)
	}
	return nil
}

func (p *Pricing) OpenRouterPrice(model string) Price {
	return p.Quote("openrouter", model).Price
}

func (p *Pricing) OpenRouterCount() int {
	snapshot := p.Capture()
	if snapshot == nil {
		return 0
	}
	return snapshot.OpenRouterMetadata().PricedModelCount
}

func (p *Pricing) OpenRouterModelCount() int {
	return p.Capture().OpenRouterMetadata().ModelCount
}

func costUSD(
	price Price,
	in,
	out,
	cacheRead,
	cacheWrite5m,
	cacheWrite1h int64,
) float64 {
	return (float64(in)*price.Prompt +
		float64(out)*price.Completion +
		float64(cacheRead)*price.CacheRead +
		float64(cacheWrite5m)*price.CacheWrite5m +
		float64(cacheWrite1h)*price.CacheWrite1h) / 1e6
}

func clonePrices(source map[string]Price) map[string]Price {
	out := make(map[string]Price, len(source))
	for model, price := range source {
		out[model] = price
	}
	return out
}

func sortedModelIDs(prices map[string]Price) []string {
	models := make([]string, 0, len(prices))
	for model := range prices {
		models = append(models, model)
	}
	sort.Strings(models)
	return models
}

func containsSorted(values []string, target string) bool {
	index := sort.SearchStrings(values, target)
	return index < len(values) && values[index] == target
}

func catalogRevision(models []string, prices map[string]Price) string {
	hash := sha256.New()
	for _, model := range models {
		price, priced := prices[model]
		_, _ = io.WriteString(hash, model)
		_, _ = hash.Write([]byte{0})
		if priced {
			_, _ = hash.Write([]byte{1})
		} else {
			_, _ = hash.Write([]byte{0})
		}
		for _, value := range []float64{
			price.Prompt,
			price.Completion,
			price.CacheRead,
			price.CacheWrite5m,
			price.CacheWrite1h,
		} {
			_, _ = io.WriteString(hash, strconv.FormatFloat(value, 'g', -1, 64))
			_, _ = hash.Write([]byte{0})
		}
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func catalogRevisionWithRatePresence(
	models []string,
	prices map[string]Price,
	presence map[string]RatePresence,
) string {
	hash := sha256.New()
	_, _ = io.WriteString(hash, catalogRevision(models, prices))
	for _, model := range models {
		value := presence[model]
		for _, present := range []bool{
			value.Input,
			value.Output,
			value.CacheRead,
			value.CacheWrite5m,
			value.CacheWrite1h,
		} {
			if present {
				_, _ = hash.Write([]byte{1})
			} else {
				_, _ = hash.Write([]byte{0})
			}
		}
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func priceTableRevision(prices map[string]Price) string {
	return catalogRevision(sortedModelIDs(prices), prices)
}
