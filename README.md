# llmux

`llmux` is a small Go prototype of an OpenAI-compatible chat-completion gateway. It accepts `POST /v1/chat/completions`, routes a requested model to an ordered provider list, and tries the fallback when an upstream request fails. It demonstrates provider interfaces, request-scoped cancellation, deadlines, response-size limits and bounded in-flight work.

This is a prototype in progress, not a production gateway. It currently supports non-streaming JSON chat-completion requests only. It has no tenant authentication, persistent budgets, rate limiting, caching, semantic retrieval, circuit breaker, retry policy, distributed coordination, telemetry backend or provider-specific adapters beyond generic OpenAI-compatible HTTP endpoints. It has no measured performance or cost results. The existing project specification describes future directions, not implemented features. This prototype was created with AI coding assistance and still needs owner review before the code or design is presented as accepted personal work.

The server binds to `127.0.0.1:8080` by default because it has no client authentication. Do not expose it on a network interface until authentication and access controls are implemented.

## Run the offline demo

Go 1.25 or newer is required. The demo uses an in-process deterministic provider and makes no network calls:

```powershell
go run ./cmd/llmux -demo
```

In another terminal:

```powershell
curl.exe -s http://localhost:8080/v1/chat/completions `
  -H "Content-Type: application/json" `
  -d '{"model":"demo-model","messages":[{"role":"user","content":"hello"}]}'
```

The response content is always `llmux offline demo response`.

## Use an OpenAI-compatible upstream

```powershell
$env:LLMUX_PRIMARY_URL = "http://127.0.0.1:9000"
$env:LLMUX_PRIMARY_MODEL = "provider-model-name"
$env:LLMUX_PRIMARY_KEY = "replace-with-provider-key"
$env:LLMUX_FALLBACK_URL = "http://127.0.0.1:9001"
$env:LLMUX_FALLBACK_MODEL = "fallback-provider-model"
go run ./cmd/llmux -listen 127.0.0.1:8080 -timeout 20s -max-concurrent 16
```

The public model name is `LLMUX_PRIMARY_MODEL`; the gateway rewrites it to the provider model for both configured upstreams. Provider URLs are base URLs; the gateway appends `/v1/chat/completions`. Provider keys are optional and sent as bearer tokens. Avoid putting real credentials in shell history or source control.

## Verify

```powershell
go test ./...
go test -race ./...
go vet ./...
```

Tests use `httptest` fake upstreams and in-memory provider stubs; they do not call external model services. They cover model rewriting and auth forwarding, fallback after a provider error, deadline handling, the concurrency cap, cancellation and unknown-model rejection.

## Design notes

- `Provider` is defined at the router's point of use and returns a response value rather than exposing provider transport details.
- Model routing is explicit: one public model name maps to an ordered provider list. Fallback occurs on any request error, including non-2xx responses. There is no retry or failure classification yet.
- The handler uses a non-blocking semaphore. When all slots are occupied it returns `503` immediately. This keeps the queue bounded and makes overload visible to callers.
- The request context is passed through to the HTTP provider, so client cancellation and the gateway deadline can stop upstream work.
- Responses are bounded to 8 MiB; inbound JSON bodies are bounded to 1 MiB. Streaming is deferred because partial-stream failures and cancellation need a separate protocol design.

## Next implementation steps

1. Add API-key authentication and tenant/model allowlists.
2. Add per-tenant request/token rate limits with explicit local-vs-distributed semantics.
3. Define usage accounting and idempotent budget reservations before adding spend ceilings.
4. Add SSE pass-through with disconnect tests before considering cache writes.
5. Add observability and benchmark methodology only after behavior and resource limits are stable.

These are planned steps, not current capabilities.
