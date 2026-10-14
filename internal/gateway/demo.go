package gateway

import (
	"context"
	"encoding/json"
)

// DemoProvider returns a stable, local response for smoke-testing the API.
type DemoProvider struct{}

func (DemoProvider) Name() string { return "offline-demo" }

func (DemoProvider) Complete(_ context.Context, _ []byte) ([]byte, error) {
	response := map[string]any{
		"id":     "chatcmpl-llmux-demo",
		"object": "chat.completion",
		"choices": []any{map[string]any{
			"index":         0,
			"message":       map[string]string{"role": "assistant", "content": "llmux offline demo response"},
			"finish_reason": "stop",
		}},
		"usage": map[string]int{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0},
	}
	return json.Marshal(response)
}
