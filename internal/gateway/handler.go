package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"
)

const maxRequestBytes = 1 << 20

type Handler struct {
	router      *Router
	semaphore   chan struct{}
	timeout     time.Duration
	requestPath string
}

func NewHandler(router *Router, maxConcurrent int, timeout time.Duration) *Handler {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &Handler{router: router, semaphore: make(chan struct{}, maxConcurrent), timeout: timeout, requestPath: "/v1/chat/completions"}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != h.requestPath {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if contentType := r.Header.Get("Content-Type"); contentType != "" {
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || mediaType != "application/json" {
			http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
			return
		}
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if isMaxBytesError(err, &maxBytesErr) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "could not read request body", http.StatusBadRequest)
		}
		return
	}
	var request routeRequest
	if err := json.Unmarshal(body, &request); err != nil || request.Model == "" {
		http.Error(w, "request must be JSON with a model", http.StatusBadRequest)
		return
	}
	select {
	case h.semaphore <- struct{}{}:
		defer func() { <-h.semaphore }()
	case <-r.Context().Done():
		return
	default:
		http.Error(w, "gateway concurrency limit reached", http.StatusServiceUnavailable)
		return
	}

	ctx := r.Context()
	var cancel context.CancelFunc
	if h.timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, h.timeout)
		defer cancel()
	}
	response, err := h.router.Complete(ctx, body)
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		if ctx.Err() == context.DeadlineExceeded {
			http.Error(w, "upstream request timed out", http.StatusGatewayTimeout)
			return
		}
		if errors.Is(err, ErrUnknownModel) {
			http.Error(w, "unknown model", http.StatusBadRequest)
			return
		}
		http.Error(w, "all configured providers failed", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(response)
}

func isMaxBytesError(err error, target **http.MaxBytesError) bool {
	return errors.As(err, target)
}
