package gateway

import (
	"github.com/ckorhonen/openrouter-switch/gateway/internal/config"
	"github.com/ckorhonen/openrouter-switch/gateway/internal/requestcapability"
)

type requestMultimodalState struct {
	hasImage bool
	stateful bool
}

func inspectMultimodalRequest(
	kind string,
	body []byte,
) requestMultimodalState {
	var endpoint requestcapability.Endpoint
	switch kind {
	case "messages":
		endpoint = requestcapability.AnthropicMessages
	case "chat":
		endpoint = requestcapability.OpenAIChat
	case "responses":
		endpoint = requestcapability.OpenAIResponses
	default:
		return requestMultimodalState{}
	}
	inspection := requestcapability.Inspect(endpoint, body)
	if inspection.Malformed {
		return requestMultimodalState{}
	}
	return requestMultimodalState{
		hasImage: inspection.HasImage,
		stateful: inspection.Stateful,
	}
}

func applyMultimodalStateForRequest(
	cl *clientListener,
	attempts []upstreamAttempt,
	kind string,
	body []byte,
) {
	if len(attempts) < 2 ||
		attempts[0].route != "openrouter" ||
		attempts[1].route != config.NativeRoute(cl.cfg.ProtocolShape) {
		return
	}
	state := inspectMultimodalRequest(kind, body)
	for i := range attempts {
		attempts[i].imageInput = state.hasImage
		attempts[i].providerStateful = state.stateful
	}
}
