package gateway

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const maxProviderResponseBytes = 8 << 20

// Provider is the narrow interface consumed by the router. Implementations
// return the provider's OpenAI-compatible response body unchanged.
type Provider interface {
	Name() string
	Complete(context.Context, []byte) ([]byte, error)
}

// OpenAIProvider sends chat-completion requests to an OpenAI-compatible API.
type OpenAIProvider struct {
	name   string
	url    string
	model  string
	key    string
	client *http.Client
}

func NewOpenAIProvider(name, baseURL, model, key string, client *http.Client) (*OpenAIProvider, error) {
	baseURL = strings.TrimSpace(baseURL)
	parsed, err := url.Parse(baseURL)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return nil, fmt.Errorf("provider %q: invalid base URL", name)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("provider %q: base URL must use http or https", name)
	}
	if name == "" || model == "" {
		return nil, fmt.Errorf("provider name and model are required")
	}
	endpoint, err := url.JoinPath(parsed.String(), "v1", "chat", "completions")
	if err != nil {
		return nil, fmt.Errorf("provider %q: invalid base URL path", name)
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &OpenAIProvider{name: name, url: endpoint, model: model, key: key, client: client}, nil
}

func (p *OpenAIProvider) Name() string { return p.name }

func (p *OpenAIProvider) Complete(ctx context.Context, requestBody []byte) ([]byte, error) {
	body, err := rewriteModel(requestBody, p.model)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build provider request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if p.key != "" {
		req.Header.Set("Authorization", "Bearer "+p.key)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("provider %s request: %w", p.name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("provider %s returned HTTP %d", p.name, resp.StatusCode)
	}
	response, err := io.ReadAll(io.LimitReader(resp.Body, maxProviderResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read provider %s response: %w", p.name, err)
	}
	if len(response) > maxProviderResponseBytes {
		return nil, fmt.Errorf("provider %s response exceeds %d bytes", p.name, maxProviderResponseBytes)
	}
	return response, nil
}
