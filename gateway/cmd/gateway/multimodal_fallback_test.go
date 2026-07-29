package gateway

import "testing"

func TestInspectMultimodalRequest(t *testing.T) {
	state := inspectMultimodalRequest(
		"messages",
		[]byte(`{"messages":[{"role":"user","content":[
			{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aW1hZ2U="}}
		]}]}`),
	)
	if !state.hasImage || state.stateful {
		t.Fatalf("state = %+v", state)
	}
}

func TestApplyMultimodalStateRequiresOpenRouterThenNative(t *testing.T) {
	client := &clientListener{cfg: resolvedClientConfig{
		ProtocolShape: "anthropic",
	}}
	attempts := []upstreamAttempt{
		{route: "openrouter"},
		{route: "anthropic"},
	}
	applyMultimodalStateForRequest(
		client,
		attempts,
		"messages",
		[]byte(`{"messages":[{"role":"user","content":[
			{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aW1hZ2U="}}
		]}]}`),
	)
	if !attempts[0].imageInput || !attempts[1].imageInput {
		t.Fatalf("attempts = %+v", attempts)
	}
}
