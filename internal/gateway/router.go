package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

var ErrUnknownModel = errors.New("unknown model")

type routeRequest struct {
	Model string `json:"model"`
}

// Router maps requested model names to ordered providers. It tries the next
// provider only after a request-level failure and stops immediately on
// cancellation or deadline expiry.
type Router struct {
	routes map[string][]Provider
}

func NewRouter(routes map[string][]Provider) (*Router, error) {
	copyRoutes := make(map[string][]Provider, len(routes))
	for model, providers := range routes {
		if model == "" || len(providers) == 0 {
			return nil, fmt.Errorf("route model and at least one provider are required")
		}
		copyRoutes[model] = append([]Provider(nil), providers...)
		for _, provider := range providers {
			if provider == nil {
				return nil, fmt.Errorf("route %q contains a nil provider", model)
			}
		}
	}
	return &Router{routes: copyRoutes}, nil
}

func (r *Router) Complete(ctx context.Context, body []byte) ([]byte, error) {
	var request routeRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, fmt.Errorf("decode request: %w", err)
	}
	providers := r.routes[request.Model]
	if len(providers) == 0 {
		return nil, fmt.Errorf("%w: %q", ErrUnknownModel, request.Model)
	}
	var failures []error
	for _, provider := range providers {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		response, err := provider.Complete(ctx, body)
		if err == nil {
			return response, nil
		}
		failures = append(failures, fmt.Errorf("%s: %w", provider.Name(), err))
	}
	return nil, fmt.Errorf("all providers failed: %w", errors.Join(failures...))
}
