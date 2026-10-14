package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
)

func rewriteModel(body []byte, providerModel string) ([]byte, error) {
	var request map[string]json.RawMessage
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, fmt.Errorf("decode request for provider: %w", err)
	}
	modelJSON, err := json.Marshal(providerModel)
	if err != nil {
		return nil, fmt.Errorf("encode provider model: %w", err)
	}
	request["model"] = modelJSON
	var out bytes.Buffer
	if err := json.NewEncoder(&out).Encode(request); err != nil {
		return nil, fmt.Errorf("encode provider request: %w", err)
	}
	return out.Bytes(), nil
}
