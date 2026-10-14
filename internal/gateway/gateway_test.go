package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testRequest = `{"model":"public-model","messages":[{"role":"user","content":"hello"}]}`

type providerFunc struct {
	name string
	call func(context.Context, []byte) ([]byte, error)
}

func (p providerFunc) Name() string { return p.name }
func (p providerFunc) Complete(ctx context.Context, body []byte) ([]byte, error) {
	return p.call(ctx, body)
}

func TestOpenAIProviderRewritesModelAndForwardsAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/v1/chat/completions"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		if got, want := r.Header.Get("Authorization"), "Bearer secret"; got != want {
			t.Errorf("authorization = %q, want %q", got, want)
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode upstream request: %v", err)
		}
		if got, want := request["model"], "provider-model"; got != want {
			t.Errorf("model = %v, want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"ok"}`)
	}))
	defer server.Close()

	provider, err := NewOpenAIProvider("test", server.URL, "provider-model", "secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	response, err := provider.Complete(context.Background(), []byte(testRequest))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(response); got != `{"id":"ok"}` {
		t.Fatalf("response = %s", got)
	}
}

func TestOpenAIProviderAppendsEndpointToBasePath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/gateway/v1/chat/completions"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		_, _ = io.WriteString(w, `{"id":"ok"}`)
	}))
	defer server.Close()

	provider, err := NewOpenAIProvider("test", server.URL+"/gateway/", "provider-model", "", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Complete(context.Background(), []byte(testRequest)); err != nil {
		t.Fatal(err)
	}
}

func TestNewOpenAIProviderRejectsInvalidBaseURLs(t *testing.T) {
	for _, baseURL := range []string{
		"/relative/path",
		"ftp://example.com",
		"http://user:password@example.com",
		"http://example.com?token=secret",
		"http://example.com#fragment",
	} {
		t.Run(baseURL, func(t *testing.T) {
			if _, err := NewOpenAIProvider("test", baseURL, "model", "", nil); err == nil {
				t.Fatalf("NewOpenAIProvider(%q) unexpectedly succeeded", baseURL)
			}
		})
	}
}

func TestHandlerValidatesContentType(t *testing.T) {
	provider := providerFunc{name: "ok", call: func(context.Context, []byte) ([]byte, error) {
		return []byte(`{"id":"ok"}`), nil
	}}
	router, err := NewRouter(map[string][]Provider{"public-model": {provider}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(router, 1, time.Second)
	for _, test := range []struct {
		name        string
		contentType string
		wantStatus  int
	}{
		{name: "json with charset", contentType: "application/json; charset=utf-8", wantStatus: http.StatusOK},
		{name: "jsonp is rejected", contentType: "application/jsonp", wantStatus: http.StatusUnsupportedMediaType},
		{name: "invalid media type is rejected", contentType: "application/json; charset", wantStatus: http.StatusUnsupportedMediaType},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(testRequest))
			request.Header.Set("Content-Type", test.contentType)
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, test.wantStatus)
			}
		})
	}
}

func TestRouterFallsBackAfterProviderFailure(t *testing.T) {
	var fallbackCalls atomic.Int32
	primary := providerFunc{name: "primary", call: func(context.Context, []byte) ([]byte, error) {
		return nil, errors.New("upstream unavailable")
	}}
	fallback := providerFunc{name: "fallback", call: func(context.Context, []byte) ([]byte, error) {
		fallbackCalls.Add(1)
		return []byte(`{"id":"fallback"}`), nil
	}}
	router, err := NewRouter(map[string][]Provider{"public-model": {primary, fallback}})
	if err != nil {
		t.Fatal(err)
	}
	response, err := router.Complete(context.Background(), []byte(testRequest))
	if err != nil {
		t.Fatal(err)
	}
	if string(response) != `{"id":"fallback"}` || fallbackCalls.Load() != 1 {
		t.Fatalf("response=%s fallback calls=%d", response, fallbackCalls.Load())
	}
}

func TestHandlerReturnsGatewayTimeoutOnDeadline(t *testing.T) {
	provider := providerFunc{name: "slow", call: func(ctx context.Context, _ []byte) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	router, _ := NewRouter(map[string][]Provider{"public-model": {provider}})
	handler := NewHandler(router, 1, 20*time.Millisecond)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(testRequest)))
	if recorder.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusGatewayTimeout)
	}
}

func TestHandlerBoundsConcurrentRequests(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	provider := providerFunc{name: "blocking", call: func(context.Context, []byte) ([]byte, error) {
		entered <- struct{}{}
		<-release
		return []byte(`{"id":"ok"}`), nil
	}}
	router, _ := NewRouter(map[string][]Provider{"public-model": {provider}})
	handler := NewHandler(router, 1, time.Second)
	first := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(testRequest)))
		close(done)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first request did not reach provider")
	}
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(testRequest)))
	if second.Code != http.StatusServiceUnavailable {
		t.Fatalf("second status = %d, want %d", second.Code, http.StatusServiceUnavailable)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("first request did not finish")
	}
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want %d", first.Code, http.StatusOK)
	}
}

func TestRouterStopsWhenContextIsCanceled(t *testing.T) {
	var calls atomic.Int32
	provider := providerFunc{name: "blocked", call: func(ctx context.Context, _ []byte) ([]byte, error) {
		calls.Add(1)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	router, _ := NewRouter(map[string][]Provider{"public-model": {provider, provider}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := router.Complete(ctx, []byte(testRequest))
	if !errors.Is(err, context.Canceled) || calls.Load() != 0 {
		t.Fatalf("error=%v calls=%d", err, calls.Load())
	}
}

func TestHandlerRejectsUnknownModel(t *testing.T) {
	router, _ := NewRouter(map[string][]Provider{})
	handler := NewHandler(router, 1, time.Second)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(testRequest)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}
