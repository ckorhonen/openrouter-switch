package gateway

import (
	"github.com/ckorhonen/openrouter-switch/gateway/internal/pricing"
	"github.com/ckorhonen/openrouter-switch/gateway/internal/reasoning"
)

type reasoningTelemetryV1 struct {
	policyMode       *string
	effectiveEnabled *bool
	effectiveEffort  *string
	policySource     *string
	catalogRevision  *string
}

func applyOpenRouterReasoningPolicy(
	snapshot *pricing.Snapshot,
	rc resolvedClientConfig,
	body []byte,
	targetModel string,
	kind string,
	upShape string,
	requested reasoning.RequestedReasoning,
) ([]byte, reasoningTelemetryV1, error) {
	wireShape := reasoning.WireOpenAIChat
	switch {
	case kind == "messages" && upShape == "anthropic":
		wireShape = reasoning.WireAnthropicMessages
	case kind == "messages" && upShape == "openai":
		wireShape = reasoning.WireTranslatedChat
	case kind == "responses":
		wireShape = reasoning.WireOpenAIResponses
	}

	capability := catalogReasoningInput(
		snapshot,
		pricing.ProviderOpenRouter,
		targetModel,
	)
	catalogRevision := ""
	if snapshot != nil {
		if catalogCapability, ok := snapshot.ModelReasoning(
			pricing.ProviderOpenRouter,
			targetModel,
		); ok {
			catalogRevision = catalogCapability.Provenance.Revision
		}
	}

	stored := storedReasoningPolicy(
		rc,
		pricing.ProviderOpenRouter,
		targetModel,
	)
	decision, err := reasoning.Resolve(reasoning.Input{
		Provider:         pricing.ProviderOpenRouter,
		CanonicalModelID: targetModel,
		WireShape:        wireShape,
		Capability:       capability,
		Stored:           stored,
		Requested:        requested,
	})
	if err != nil {
		return nil, reasoningTelemetryV1{}, err
	}
	mode := string(decision.Mode)
	source := string(decision.Source)
	telemetry := reasoningTelemetryV1{
		policyMode:   &mode,
		policySource: &source,
	}
	if catalogRevision != "" {
		telemetry.catalogRevision = &catalogRevision
	}
	switch decision.Mode {
	case reasoning.ModeOff:
		enabled := false
		telemetry.effectiveEnabled = &enabled
	case reasoning.ModeFollowHarness, reasoning.ModePassthrough:
		if requested.Present && requested.Recognized {
			enabled := !requested.Disabled
			telemetry.effectiveEnabled = &enabled
		}
		if requested.Effort != "" {
			effort := requested.Effort
			telemetry.effectiveEffort = &effort
		}
	}
	if wireShape == reasoning.WireAnthropicMessages {
		transformed, err := reasoning.ApplyAnthropicMessages(body, decision)
		if err != nil {
			return nil, reasoningTelemetryV1{}, err
		}
		return transformed, telemetry, nil
	}
	return body, telemetry, nil
}
