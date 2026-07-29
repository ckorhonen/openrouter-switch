package pricing

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestNewHasNoOpenRouterFallback(t *testing.T) {
	p := New()
	if quote := p.Quote("openrouter", "zai-org/GLM-5.2"); quote.Priced {
		t.Fatalf("cold-start OpenRouter quote must be unpriced: %+v", quote)
	}
	if models := p.Capture().OpenRouterModels(); len(models) != 0 {
		t.Fatalf("cold-start OpenRouter models = %+v, want none", models)
	}
	metadata := p.Capture().OpenRouterMetadata()
	if metadata.Source != "" || metadata.ModelCount != 0 ||
		metadata.PricedModelCount != 0 || !metadata.FetchedAt.IsZero() {
		t.Fatalf("cold-start OpenRouter metadata = %+v", metadata)
	}
}

func TestOpenRouterModelsUserMetadataAndEligibility(t *testing.T) {
	body := []byte(`{
		"data": [
			{
				"id": "anthropic/claude-tools",
				"name": "Claude Tools",
				"context_length": 200000,
				"architecture": {
					"input_modalities": ["text", "image"],
					"output_modalities": ["text"]
				},
				"top_provider": {"max_completion_tokens": 64000},
				"supported_parameters": ["tools", "reasoning", "temperature"],
				"reasoning": {
					"supported_efforts": ["high", "medium", "low"],
					"supports_max_tokens": true,
					"mandatory": false
				},
				"pricing": {
					"prompt": "0.000003",
					"completion": "0.000015",
					"cache_read": "0.0000003"
				}
			},
			{
				"id": "openai/gpt-no-tools",
				"name": "GPT No Tools",
				"context_length": 128000,
				"architecture": {
					"input_modalities": ["text"],
					"output_modalities": ["text"]
				},
				"top_provider": {"max_completion_tokens": 16000},
				"supported_parameters": ["temperature"],
				"pricing": {"prompt": "0.000001", "completion": "0.000002"}
			}
		]
	}`)
	p := New()
	if err := p.ReplaceOpenRouterCatalog(
		body,
		"openrouter_models_user",
		time.Unix(10, 0),
		"",
	); err != nil {
		t.Fatal(err)
	}
	tools, ok := p.Capture().Model(ProviderOpenRouter, "anthropic/claude-tools")
	if !ok {
		t.Fatal("tool-capable model missing")
	}
	if !tools.ToolCapable || tools.ContextTokens != 200000 ||
		tools.MaxOutputTokens != 64000 ||
		tools.DisplayName != "Claude Tools" {
		t.Fatalf("tool-capable metadata = %+v", tools)
	}
	if len(tools.InputModalities) != 2 ||
		len(tools.OutputModalities) != 1 ||
		len(tools.SupportedParams) != 3 {
		t.Fatalf("tool-capable slices = %+v", tools)
	}
	if tools.Availability.Account == nil {
		t.Fatal("authenticated account availability missing")
	}
	if tools.Reasoning == nil || !tools.Reasoning.Supported ||
		len(tools.Reasoning.Options) != 3 {
		t.Fatalf("reasoning metadata = %+v", tools.Reasoning)
	}
	effort := tools.Reasoning.Options[1]
	if effort.Type != ReasoningEffort ||
		len(effort.Values) != 4 ||
		effort.Values[3] == nil ||
		*effort.Values[3] != "none" {
		t.Fatalf("reasoning effort metadata = %+v", effort)
	}
	if got := p.Quote(ProviderOpenRouter, tools.CanonicalModelID).Price; got.Prompt != 3 || got.Completion != 15 || got.CacheRead != 0.3 {
		t.Fatalf("numeric-string prices = %+v", got)
	}
	noTools, ok := p.Capture().Model(ProviderOpenRouter, "openai/gpt-no-tools")
	if !ok || noTools.ToolCapable {
		t.Fatalf("non-tool model eligibility = %+v, found=%t", noTools, ok)
	}
}

func TestReplaceOpenRouterCatalogReplacesAndRemoves(t *testing.T) {
	p := New()
	first := catalogJSON(
		catalogModel{id: "model-a", prompt: 0.000001},
		catalogModel{id: "model-b", prompt: 0.000002},
	)
	if err := p.ReplaceOpenRouterCatalog(first, "live", time.Unix(10, 0), ""); err != nil {
		t.Fatal(err)
	}
	if p.OpenRouterModelCount() != 2 {
		t.Fatalf("first model count = %d, want 2", p.OpenRouterModelCount())
	}

	second := catalogJSON(catalogModel{id: "model-b", prompt: 0.000003})
	if err := p.ReplaceOpenRouterCatalog(second, "live", time.Unix(20, 0), ""); err != nil {
		t.Fatal(err)
	}
	if p.Quote("openrouter", "model-a").Priced {
		t.Fatal("removed model-a survived replacement")
	}
	if got := p.OpenRouterPrice("model-b").Prompt; got != 3 {
		t.Fatalf("model-b prompt = %v, want 3", got)
	}
	if p.OpenRouterModelCount() != 1 {
		t.Fatalf("second model count = %d, want 1", p.OpenRouterModelCount())
	}
}

func TestOpenRouterCatalogTracksMissingCacheRates(t *testing.T) {
	p := New()
	body := []byte(`{
		"data": [{
			"id": "model-a",
			"pricing": {"prompt": 0.000001, "completion": 0.000002}
		}]
	}`)
	if err := p.ReplaceOpenRouterCatalog(
		body,
		"openrouter_models_user",
		time.Unix(10, 0),
		"",
	); err != nil {
		t.Fatal(err)
	}
	quote := p.Quote(ProviderOpenRouter, "model-a")
	if !quote.Priced || !quote.RatePresenceKnown ||
		!quote.RatePresence.Input ||
		!quote.RatePresence.Output ||
		quote.RatePresence.CacheRead ||
		quote.RatePresence.CacheWrite5m {
		t.Fatalf("OpenRouter rate presence = %+v", quote)
	}
	if !quote.HasRatesForUsage(1, 1, 0, 0, 0) {
		t.Fatal("zero cache usage required missing OpenRouter cache rates")
	}
	if quote.HasRatesForUsage(1, 1, 1, 0, 0) {
		t.Fatal("nonzero cache usage accepted missing OpenRouter cache-read rate")
	}
}

func TestInvalidOpenRouterRefreshRetainsLastKnownGood(t *testing.T) {
	p := New()
	good := catalogJSON(catalogModel{id: "model-a", prompt: 0.000002})
	fetchedAt := time.Unix(100, 0).UTC()
	if err := p.ReplaceOpenRouterCatalog(good, "live", fetchedAt, ""); err != nil {
		t.Fatal(err)
	}
	before := p.Capture()
	beforeMetadata := before.OpenRouterMetadata()

	bad := []byte(`{"data":[{"id":"model-a","pricing":{"prompt":-2}}]}`)
	if err := p.ReplaceOpenRouterCatalog(bad, "broken", time.Unix(200, 0), ""); err == nil {
		t.Fatal("negative pricing refresh succeeded")
	}
	after := p.Capture()
	if after != before {
		t.Fatal("invalid refresh published a new snapshot")
	}
	if after.OpenRouterMetadata() != beforeMetadata {
		t.Fatalf("metadata changed after failure: before=%+v after=%+v",
			beforeMetadata, after.OpenRouterMetadata())
	}
	if got := after.Quote("openrouter", "model-a").Price.Prompt; got != 2 {
		t.Fatalf("last-known-good prompt = %v, want 2", got)
	}
}

func TestOpenRouterCatalogSkipsMalformedModels(t *testing.T) {
	p := New()
	body := []byte(`{"data":[
		{
			"id":"good/tools",
			"name":"Good Tools",
			"context_length":128000,
			"supported_parameters":["tools","reasoning"],
			"reasoning":{"supported_efforts":["high","low"]},
			"pricing":{"prompt":"0.000001","completion":"0.000002"}
		},
		{
			"id":"bad/metadata",
			"context_length":"many",
			"supported_parameters":["tools"]
		},
		{
			"id":"bad/reasoning",
			"supported_parameters":["tools","reasoning"],
			"reasoning":{"supported_efforts":"high"}
		},
		{
			"id":"bad/pricing",
			"supported_parameters":["tools"],
			"pricing":{"prompt":"not-a-number"}
		}
	]}`)
	if err := p.ReplaceOpenRouterCatalog(
		body,
		"openrouter_models_user",
		time.Unix(100, 0).UTC(),
		"good/tools",
	); err != nil {
		t.Fatal(err)
	}

	snapshot := p.Capture()
	if got := snapshot.OpenRouterModels(); len(got) != 1 ||
		got[0] != "good/tools" {
		t.Fatalf("published models = %v, want [good/tools]", got)
	}
	record, ok := snapshot.Model(ProviderOpenRouter, "good/tools")
	if !ok || !record.ToolCapable || record.Reasoning == nil {
		t.Fatalf("valid tool-capable model = %+v, found=%t", record, ok)
	}
	if quote := snapshot.Quote(ProviderOpenRouter, "good/tools"); !quote.Priced ||
		quote.Price.Prompt != 1 || quote.Price.Completion != 2 {
		t.Fatalf("valid model route quote = %+v", quote)
	}
	for _, id := range []string{"bad/metadata", "bad/reasoning", "bad/pricing"} {
		if _, ok := snapshot.Model(ProviderOpenRouter, id); ok {
			t.Errorf("malformed model %q was published", id)
		}
	}
	wantDiagnostics := []string{
		`excluded malformed OpenRouter model "bad/metadata": decode metadata: invalid field type`,
		`excluded malformed OpenRouter model "bad/pricing": pricing: prompt: must be finite`,
		`excluded malformed OpenRouter model "bad/reasoning": reasoning: supported_efforts is not an array or null`,
	}
	if got := snapshot.ProviderMetadata(ProviderOpenRouter).Diagnostics; !slices.Equal(got, wantDiagnostics) {
		t.Fatalf("diagnostics = %#v, want %#v", got, wantDiagnostics)
	}
}

func TestOpenRouterCatalogRejectsAllMalformedModelsAtomically(t *testing.T) {
	p := New()
	if err := p.ReplaceOpenRouterCatalog(
		catalogJSON(catalogModel{id: "last-known-good", prompt: 0.000001}),
		"live",
		time.Unix(10, 0).UTC(),
		"",
	); err != nil {
		t.Fatal(err)
	}
	before := p.Capture()

	allBad := []byte(`{"data":[
		{"id":"bad/metadata","context_length":"many"},
		{"id":"bad/pricing","pricing":{"prompt":"not-a-number"}}
	]}`)
	err := p.ReplaceOpenRouterCatalog(
		allBad,
		"openrouter_models_user",
		time.Unix(20, 0).UTC(),
		"",
	)
	if err == nil || !strings.Contains(err.Error(), "no usable models") {
		t.Fatalf("all-bad catalog error = %v, want no usable models", err)
	}
	if after := p.Capture(); after != before {
		t.Fatal("all-bad catalog replaced the last-known-good snapshot")
	}
}

func TestOpenRouterCatalogDuplicateIDsRemainAtomic(t *testing.T) {
	p := New()
	err := p.ReplaceOpenRouterCatalog(
		[]byte(`{"data":[
			{"id":"duplicate","pricing":{"prompt":0.000001}},
			{"id":"duplicate","pricing":{"prompt":0.000002}}
		]}`),
		"openrouter_models_user",
		time.Unix(20, 0).UTC(),
		"",
	)
	if err == nil || !strings.Contains(err.Error(), `duplicate model "duplicate"`) {
		t.Fatalf("duplicate catalog error = %v", err)
	}
	if models := p.Capture().OpenRouterModels(); len(models) != 0 {
		t.Fatalf("duplicate catalog published models: %v", models)
	}
}

func TestOpenRouterVariablePriceSentinelKeepsModelUnpriced(t *testing.T) {
	p := New()
	body := []byte(`{"data":[{
		"id":"openrouter/auto",
		"architecture":{
			"input_modalities":["text"],
			"output_modalities":["text"]
		},
		"supported_parameters":["tools"],
		"pricing":{"prompt":"-1","completion":"-1"}
	}]}`)
	if err := p.ReplaceOpenRouterCatalog(
		body,
		"openrouter_models_user",
		time.Unix(100, 0).UTC(),
		"",
	); err != nil {
		t.Fatal(err)
	}
	record, ok := p.Capture().Model(ProviderOpenRouter, "openrouter/auto")
	if !ok || !record.ToolCapable || record.Availability.Account == nil {
		t.Fatalf("variable-price model availability = %+v, found=%t", record, ok)
	}
	if quote := p.Quote(ProviderOpenRouter, "openrouter/auto"); quote.Priced {
		t.Fatalf("variable-price model quote = %+v, want unpriced", quote)
	}
}

func TestOpenRouterRevisionIndependentOfResponseOrder(t *testing.T) {
	modelA := catalogModel{id: "model-a", prompt: 0.000001, completion: 0.000002}
	modelB := catalogModel{id: "model-b", prompt: 0.000003, completion: 0.000004}
	p1 := New()
	p2 := New()
	if err := p1.ReplaceOpenRouterCatalog(
		catalogJSON(modelA, modelB), "one", time.Unix(10, 0), "",
	); err != nil {
		t.Fatal(err)
	}
	if err := p2.ReplaceOpenRouterCatalog(
		catalogJSON(modelB, modelA), "two", time.Unix(20, 0), "",
	); err != nil {
		t.Fatal(err)
	}
	revision1 := p1.Capture().OpenRouterMetadata().Revision
	revision2 := p2.Capture().OpenRouterMetadata().Revision
	if revision1 != revision2 {
		t.Fatalf("revisions differ by response order: %q != %q", revision1, revision2)
	}
}

func TestOpenRouterRevisionDistinguishesUnpricedFromPricedZero(t *testing.T) {
	unpriced := New()
	if err := unpriced.ReplaceOpenRouterCatalog(
		[]byte(`{"data":[{"id":"model-a"}]}`),
		"live",
		time.Unix(10, 0),
		"",
	); err != nil {
		t.Fatal(err)
	}
	pricedZero := New()
	if err := pricedZero.ReplaceOpenRouterCatalog(
		[]byte(`{"data":[{"id":"model-a","pricing":{"prompt":0,"completion":0}}]}`),
		"live",
		time.Unix(10, 0),
		"",
	); err != nil {
		t.Fatal(err)
	}
	if unpriced.Capture().OpenRouterMetadata().Revision ==
		pricedZero.Capture().OpenRouterMetadata().Revision {
		t.Fatal("unpriced and explicit zero-price catalogs have the same revision")
	}
}

func TestCapturedSnapshotStableAcrossRefresh(t *testing.T) {
	p := New()
	if err := p.ReplaceOpenRouterCatalog(
		catalogJSON(catalogModel{id: "model-a", prompt: 0.000001}),
		"revision-a",
		time.Unix(10, 0),
		"",
	); err != nil {
		t.Fatal(err)
	}
	captured := p.Capture()
	oldQuote := captured.Quote("openrouter", "model-a")

	if err := p.ReplaceOpenRouterCatalog(
		catalogJSON(catalogModel{id: "model-a", prompt: 0.000009}),
		"revision-b",
		time.Unix(20, 0),
		"",
	); err != nil {
		t.Fatal(err)
	}
	if got := captured.Quote("openrouter", "model-a"); got != oldQuote {
		t.Fatalf("captured quote changed: before=%+v after=%+v", oldQuote, got)
	}
	if got := p.Quote("openrouter", "model-a").Price.Prompt; got != 9 {
		t.Fatalf("current prompt = %v, want 9", got)
	}
	if oldQuote.CostUSD(1_000_000, 0, 0, 0, 0) != 1 {
		t.Fatalf("captured cost = %v, want 1", oldQuote.CostUSD(1_000_000, 0, 0, 0, 0))
	}
}

func TestQuoteNanoUSDRatesAndCheckedCost(t *testing.T) {
	p := NewWithPrices(map[string]Price{
		"zai-org/GLM-5.2": {
			Prompt: 1.4, Completion: 4.4, CacheRead: 0.14,
		},
	})
	quote := p.Quote("openrouter", "zai-org/GLM-5.2")
	rates, err := quote.NanoUSDRates()
	if err != nil {
		t.Fatal(err)
	}
	if rates != (NanoUSDRates{
		Prompt:       1400,
		Completion:   4400,
		CacheRead:    140,
		CacheWrite5m: 0,
	}) {
		t.Fatalf("nano-USD rates = %+v", rates)
	}
	cost, err := rates.CostNanoUSD(100, 10, 20, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if cost != 186_800 {
		t.Fatalf("cost = %d nano-USD, want 186800", cost)
	}
	if _, err := rates.CostNanoUSD(math.MaxInt64, 1, 0, 0, 0); err == nil {
		t.Fatal("overflowing cost succeeded")
	}
	if _, err := (Quote{}).NanoUSDRates(); err != ErrUnpriced {
		t.Fatalf("unpriced error = %v, want ErrUnpriced", err)
	}
	rounded, err := (Quote{
		Priced: true,
		Price:  Price{Prompt: 0.0006},
	}).NanoUSDRates()
	if err != nil || rounded.Prompt != 1 {
		t.Fatalf("rounded rates = %+v, err=%v, want prompt=1", rounded, err)
	}
	if _, err := (Quote{
		Priced: true,
		Price:  Price{Prompt: 0.0004},
	}).NanoUSDRates(); err == nil {
		t.Fatal("positive rate that rounds to zero succeeded")
	}
}

func TestExplicitZeroOpenRouterRatesRemainPriced(t *testing.T) {
	p := New()
	body := []byte(`{
	  "data": [
	    {
	      "id": "free/model",
	      "pricing": {"prompt": 0, "completion": 0}
	    }
	  ]
	}`)
	if err := p.ReplaceOpenRouterCatalog(body, "live", time.Unix(10, 0), ""); err != nil {
		t.Fatal(err)
	}
	quote := p.Quote("openrouter", "free/model")
	if !quote.Priced {
		t.Fatal("explicit zero rates became unpriced")
	}
	rates, err := quote.NanoUSDRates()
	if err != nil {
		t.Fatal(err)
	}
	if rates != (NanoUSDRates{}) {
		t.Fatalf("zero rates = %+v", rates)
	}
	cost, err := rates.CostNanoUSD(100, 200, 300, 400, 0)
	if err != nil || cost != 0 {
		t.Fatalf("priced-zero cost = %d, err=%v", cost, err)
	}
}

type catalogModel struct {
	id         string
	prompt     float64
	completion float64
}

func catalogJSON(models ...catalogModel) []byte {
	out := `{"data":[`
	for i, model := range models {
		if i > 0 {
			out += ","
		}
		out += fmt.Sprintf(
			`{"id":%q,"pricing":{"prompt":%g,"completion":%g}}`,
			model.id,
			model.prompt,
			model.completion,
		)
	}
	out += "]}"
	return []byte(out)
}
