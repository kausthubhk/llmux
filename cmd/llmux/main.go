package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kausthubhk/llmux/internal/gateway"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8080", "HTTP listen address (loopback by default; the gateway has no client authentication)")
	primaryURL := flag.String("primary-url", os.Getenv("LLMUX_PRIMARY_URL"), "OpenAI-compatible primary base URL")
	fallbackURL := flag.String("fallback-url", os.Getenv("LLMUX_FALLBACK_URL"), "optional OpenAI-compatible fallback base URL")
	primaryModel := flag.String("primary-model", envOr("LLMUX_PRIMARY_MODEL", "demo-model"), "upstream primary model name")
	fallbackModel := flag.String("fallback-model", envOr("LLMUX_FALLBACK_MODEL", "fallback-model"), "upstream fallback model name")
	primaryKey := flag.String("primary-key", os.Getenv("LLMUX_PRIMARY_KEY"), "optional primary provider API key")
	fallbackKey := flag.String("fallback-key", os.Getenv("LLMUX_FALLBACK_KEY"), "optional fallback provider API key")
	timeout := flag.Duration("timeout", 30*time.Second, "per-request upstream deadline")
	maxConcurrent := flag.Int("max-concurrent", 32, "maximum active upstream requests")
	demo := flag.Bool("demo", false, "use a deterministic local provider; no network calls")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	routes, err := buildRoutes(*demo, *primaryURL, *fallbackURL, *primaryModel, *fallbackModel, *primaryKey, *fallbackKey)
	if err != nil {
		logger.Error("configure gateway", "error", err)
		os.Exit(2)
	}
	router, err := gateway.NewRouter(routes)
	if err != nil {
		logger.Error("configure router", "error", err)
		os.Exit(2)
	}
	server := &http.Server{
		Addr:              *listen,
		Handler:           gateway.NewHandler(router, *maxConcurrent, *timeout),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		logger.Info("llmux listening", "address", *listen, "demo", *demo)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
}

func buildRoutes(demo bool, primaryURL, fallbackURL, primaryModel, fallbackModel, primaryKey, fallbackKey string) (map[string][]gateway.Provider, error) {
	if demo {
		return map[string][]gateway.Provider{"demo-model": {gateway.DemoProvider{}}}, nil
	}
	if strings.TrimSpace(primaryURL) == "" {
		return nil, errors.New("set -primary-url or LLMUX_PRIMARY_URL, or use -demo")
	}
	primary, err := gateway.NewOpenAIProvider("primary", primaryURL, primaryModel, primaryKey, &http.Client{})
	if err != nil {
		return nil, err
	}
	providers := []gateway.Provider{primary}
	if strings.TrimSpace(fallbackURL) != "" {
		fallback, err := gateway.NewOpenAIProvider("fallback", fallbackURL, fallbackModel, fallbackKey, &http.Client{})
		if err != nil {
			return nil, err
		}
		providers = append(providers, fallback)
	}
	return map[string][]gateway.Provider{primaryModel: providers}, nil
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
