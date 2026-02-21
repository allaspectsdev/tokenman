# TokenMan

Local reverse proxy for LLM API calls. Token compression, caching, PII scrubbing, budget enforcement, resilience, distributed tracing, and full observability — in a single Go binary.

Works with **Claude Code**, **Cursor**, **OpenClaw**, and any OpenAI/Anthropic-compatible client.

```
┌─────────────┐     ┌──────────────────────────────────────────┐     ┌──────────────┐
│  Your App    │────▶│  TokenMan (localhost:7677)                │────▶│  Anthropic   │
│  Claude Code │     │                                          │     │  OpenAI      │
│  Cursor      │     │  Cache ─▶ Security ─▶ Compress ─▶ Route  │     │  etc.        │
└─────────────┘     └──────────────────────────────────────────┘     └──────────────┘
                              │
                              ▼
                    Dashboard (localhost:7678)
                    Prometheus /metrics
                    OpenTelemetry traces
```

## Why

Every request to an LLM API costs tokens. Most development workflows send the same system prompts, tool definitions, and boilerplate context on every call. TokenMan sits between your client and the provider to:

- **Cache** identical requests and return stored responses instantly
- **Compress** repeated content (system prompts, tool defs, conversation history) using provider-native cache hints
- **Enforce budgets** with hourly/daily/monthly spend limits and alerts
- **Detect & redact PII** before it leaves your machine
- **Rate limit** per-provider to stay within API quotas
- **Retry with backoff** — automatic retries with exponential backoff and jitter for transient upstream failures
- **Circuit break** — per-provider circuit breakers prevent cascading failures when a provider is down
- **Trace requests** — OpenTelemetry distributed tracing with W3C trace context propagation
- **Track everything** — tokens, cost, savings, latency — in a local SQLite database with a web dashboard and Prometheus metrics

All of this runs locally. Nothing is sent to any third party beyond the LLM providers you configure.

## Install

### From source

```bash
git clone https://github.com/allaspectsdev/tokenman.git
cd tokenman
make build
```

The binary lands at `bin/tokenman`.

### Docker

```bash
make docker-build
make docker-run
```

This builds a multi-stage image (Go 1.25 builder + Alpine 3.21 runtime) with a non-root user, health checks, and a `/data` volume for persistence.

### Requirements

- Go 1.25+ (from source)
- Docker (container deployment)

## Quick Start

```bash
# 1. Generate default config
tokenman init-config

# 2. Store your API key in the OS keychain
tokenman keys set anthropic

# 3. Start the proxy
tokenman start --foreground
```

TokenMan is now listening:

| Port | Purpose |
|------|---------|
| `7677` | Proxy — point your LLM clients here |
| `7678` | Dashboard, JSON API, and Prometheus metrics |

### Point your client at TokenMan

**Claude Code:**
```bash
export ANTHROPIC_BASE_URL=http://localhost:7677
```

**Cursor:** Set the API base URL to `http://localhost:7677` in Cursor settings.

**curl (Anthropic format):**
```bash
curl -X POST http://localhost:7677/v1/messages \
  -H "Content-Type: application/json" \
  -H "x-api-key: your-key" \
  -d '{
    "model": "claude-sonnet-4-20250514",
    "max_tokens": 256,
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

**curl (OpenAI format):**
```bash
curl -X POST http://localhost:7677/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer your-key" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

## Features

### Caching

Two-tier cache (in-memory LRU + SQLite) with configurable TTL. Identical requests return instantly with zero upstream cost. Cache hits are tagged with `X-Tokenman-Cache: HIT`. The cache TTL can be updated at runtime via hot-reload.

### Token Compression

| Technique | What it does |
|-----------|-------------|
| **Content dedup** | Fingerprints system prompts and tool definitions. When repeated, annotates with `cache_control: ephemeral` for Anthropic prompt caching or reorders for OpenAI prefix matching. |
| **Whitespace collapse** | Strips redundant whitespace from text content |
| **JSON/XML minification** | Minifies embedded structured data in messages |
| **History windowing** | Keeps only the N most recent conversation turns, discarding older context |
| **Heartbeat dedup** | Collapses near-identical polling requests within a time window |
| **Conversation summarization** | Summarizes older messages using a lightweight LLM call when conversations exceed a threshold |

### Header & Body Passthrough

TokenMan forwards `anthropic-version` and `anthropic-beta` headers from the client to the upstream API. This ensures compatibility with the latest Anthropic features (extended thinking, prompt caching variants, etc.) without requiring TokenMan updates. Unknown request body fields (e.g. `thinking`, `tool_choice`, `top_k`, `stop_sequences`) are also preserved through the pipeline — TokenMan only modifies fields it manages and passes everything else through unchanged.

### Security

- **PII detection** — Identifies emails, phone numbers, SSNs, credit card numbers, and API keys. Actions: `redact`, `hash`, `log`, or `block`.
- **Prompt injection detection** — Flags suspicious patterns in user messages. Actions: `log`, `block`, `warn`, or `sanitize`.
- **Budget enforcement** — Hourly, daily, and monthly spend caps. Returns `429 Too Many Requests` when limits are hit, with structured error bodies and `Retry-After` headers.
- **Per-provider rate limiting** — Token-bucket rate limiter per provider to respect API quotas. Reconfigurable at runtime via hot-reload.
- **TLS support** — Optional HTTPS for both proxy and dashboard servers.
- **Dashboard auth** — Bearer token authentication with constant-time comparison.
- **Request body limits** — Configurable `max_body_size` and `max_response_size` to prevent memory exhaustion.
- **Sanitized errors** — Error responses to clients never leak internal details.

### Resilience

- **Retry with exponential backoff** — Transient upstream failures (429, 502, 503, 504) are retried automatically with exponential backoff and full jitter. Configurable max attempts, base delay, and max delay.
- **Per-provider circuit breaker** — Closed/Open/HalfOpen state machine prevents repeated calls to a failing provider. Configurable failure threshold, reset timeout, and half-open success count.
- **Upstream status propagation** — 4xx/5xx responses from providers are forwarded to clients with the original status code. `Retry-After` headers on 429s are passed through.
- **Panic recovery** — Panics in middleware, the cache purger, and the data pruner are caught and logged without crashing the process.
- **Response size limits** — Upstream responses are bounded by `max_response_size`. Streaming accumulator caps allow graceful degradation (client gets the full stream, internal accounting is capped).
- **Streaming timeout** — Context deadline on streaming connections prevents indefinitely hung connections.
- **Graceful shutdown** — 30-second timeout for in-flight requests, synchronized cache purger and data pruner shutdown, PID file cleanup.

### Observability

- **Web dashboard** at `localhost:7678` — live stats, request history, cost breakdowns
- **JSON API** — `/api/stats`, `/api/requests`, `/api/projects`, `/api/providers`, `/api/security/pii`, `/api/security/budget`
- **Prometheus metrics** at `/metrics` — scrape-ready for Grafana/Alertmanager (see [Metrics](#prometheus-metrics) below)
- **OpenTelemetry tracing** — Distributed tracing with W3C trace context propagation (see [Tracing](#opentelemetry-tracing) below)
- **Health endpoints** — `GET /health` (liveness) and `GET /health/ready` (readiness with DB and provider checks)
- **Per-project tracking** — Tag requests with `X-Tokenman-Project: my-project` to track usage across projects
- **Request body inspection** — Full request/response bodies stored for debugging (configurable, capped at 1MB)
- **Per-middleware timing** — Every middleware in the pipeline is individually timed for both request and response phases

### Multi-Provider Routing

- Configure multiple providers with priority-based fallback
- Explicit model-to-provider mapping
- Automatic format detection (Anthropic vs OpenAI)
- Circuit-breaker-aware routing — open circuits are skipped automatically

### Plugin System

Extend TokenMan with custom plugins:

```go
type Plugin interface {
    Name() string
    Version() string
    Init(config map[string]interface{}) error
    Close() error
}
```

Three plugin types:
- **MiddlewarePlugin** — Full pipeline middleware (request + response processing)
- **TransformPlugin** — Simple request/response transformations
- **HookPlugin** — Lifecycle notifications (request start, complete, error)

### Hot Reload

Config changes to `~/.tokenman/tokenman.toml` are picked up automatically via filesystem watching. The following settings take effect without restart:

- Log level
- Rate limiter settings (default rate/burst, per-provider limits)
- Cache TTL

## Prometheus Metrics

TokenMan exposes a `/metrics` endpoint on the dashboard port (7678) using the Prometheus text exposition format. No external Prometheus client library is required — the metrics are rendered natively.

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `tokenman_requests_total` | counter | — | Total proxy requests |
| `tokenman_tokens_in_total` | counter | — | Total input tokens |
| `tokenman_tokens_out_total` | counter | — | Total output tokens |
| `tokenman_tokens_saved_total` | counter | — | Tokens saved by compression/caching |
| `tokenman_cost_usd_total` | counter | — | Cumulative cost in USD |
| `tokenman_savings_usd_total` | counter | — | Cumulative savings in USD |
| `tokenman_savings_percent` | gauge | — | Current savings percentage |
| `tokenman_cache_hits_total` | counter | — | Cache hits |
| `tokenman_cache_misses_total` | counter | — | Cache misses |
| `tokenman_cache_hit_rate` | gauge | — | Cache hit rate (0-100) |
| `tokenman_active_requests` | gauge | — | Currently in-flight requests |
| `tokenman_uptime_seconds` | gauge | — | Process uptime |
| `tokenman_errors_total` | counter | `type`, `provider`, `status_code` | Error counts by category |
| `tokenman_request_duration_seconds` | histogram | `provider`, `model`, `streaming` | Request latency (100ms–120s buckets) |
| `tokenman_provider_requests_total` | counter | `provider`, `status` | Per-provider request outcomes |
| `tokenman_provider_circuit_state` | gauge | `provider` | Circuit state (0=closed, 1=open, 2=half-open) |
| `tokenman_middleware_duration_seconds` | histogram | `middleware`, `phase` | Per-middleware timing |

## OpenTelemetry Tracing

TokenMan supports [OpenTelemetry](https://opentelemetry.io/) distributed tracing for end-to-end visibility across your LLM request pipeline.

OpenTelemetry is a vendor-neutral open-source observability framework for generating, collecting, and exporting telemetry data (traces, metrics, logs). It provides a standard way to instrument applications so you can trace a single request as it flows through multiple services. In TokenMan's case, traces show the full lifecycle of each LLM request: from the incoming HTTP call, through each middleware in the pipeline, to the upstream provider call and back.

### What you get

When tracing is enabled, TokenMan creates spans for:

- **Root HTTP span** — The full request lifecycle, with method, path, status code
- **Per-middleware spans** — Individual spans for each middleware's `ProcessRequest` and `ProcessResponse` phases (cache, PII, budget, rate limit, dedup, etc.)
- **Upstream span** — The HTTP call to the LLM provider, tagged with URL and provider name
- **W3C trace context propagation** — `traceparent` and `tracestate` headers are extracted from incoming requests and injected into upstream calls, so traces span from your application through TokenMan to the LLM provider

Each span carries attributes:

| Attribute | Description |
|-----------|-------------|
| `request.id` | TokenMan request UUID |
| `request.model` | Model name (e.g. `claude-sonnet-4-20250514`) |
| `request.format` | API format (`anthropic` or `openai`) |
| `request.stream` | Whether this is a streaming request |
| `response.status_code` | HTTP status code |
| `response.tokens_in` | Input token count |
| `response.tokens_out` | Output token count |
| `response.cache_hit` | Whether the response came from cache |
| `response.provider` | Provider that served the request |
| `middleware.name` | Middleware name (on middleware spans) |
| `middleware.phase` | `request` or `response` (on middleware spans) |
| `upstream.url` | Upstream provider URL (on upstream spans) |
| `upstream.provider` | Provider format (on upstream spans) |

### Configuration

Tracing is disabled by default. Enable it in `tokenman.toml`:

```toml
[tracing]
enabled = true
exporter = "otlp-grpc"        # "stdout", "otlp-grpc", or "otlp-http"
endpoint = "localhost:4317"    # OTLP collector address
service_name = "tokenman"
sample_rate = 1.0              # 0.0 to 1.0
insecure = true                # skip TLS for local dev
```

Or via environment variables:

```bash
TOKENMAN_TRACING_ENABLED=true
TOKENMAN_TRACING_EXPORTER=otlp-grpc
TOKENMAN_TRACING_ENDPOINT=localhost:4317
TOKENMAN_TRACING_INSECURE=true
```

### Exporters

| Exporter | Protocol | Default Endpoint | Use Case |
|----------|----------|------------------|----------|
| `stdout` | — | — | Development, debugging |
| `otlp-grpc` | gRPC | `localhost:4317` | Production (Jaeger, Tempo, etc.) |
| `otlp-http` | HTTP/protobuf | `localhost:4318` | When gRPC is not available |

### Quick start with Jaeger

```bash
# Run Jaeger all-in-one with OTLP support
docker run -d --name jaeger \
  -p 16686:16686 \
  -p 4317:4317 \
  jaegertracing/all-in-one:latest

# Enable tracing in TokenMan
export TOKENMAN_TRACING_ENABLED=true
export TOKENMAN_TRACING_INSECURE=true
tokenman start --foreground

# View traces at http://localhost:16686
```

### Zero overhead when disabled

When `tracing.enabled = false` (the default), no tracer provider is initialized, no spans are created, and no trace context is propagated. The tracing code paths use the OpenTelemetry no-op implementation, which has negligible overhead.

## API Endpoints

### Proxy (port 7677)

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1/messages` | Anthropic-format proxy |
| `POST` | `/v1/chat/completions` | OpenAI-format proxy |
| `GET` | `/v1/models` | List available models from upstream |
| `POST` | `/v1/stream/create` | Create a bidirectional stream session |
| `POST` | `/v1/stream/{id}/send` | Send a message to a stream |
| `GET` | `/v1/stream/{id}/events` | SSE event stream |
| `DELETE` | `/v1/stream/{id}` | Close a stream session |
| `GET` | `/health` | Liveness probe — returns `{"status":"ok"}` |
| `GET` | `/health/ready` | Readiness probe — checks DB and provider availability |

### Dashboard (port 7678)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/stats` | Aggregate statistics (tokens, cost, savings, cache rates) |
| `GET` | `/api/requests` | Request history with pagination |
| `GET` | `/api/projects` | Per-project usage breakdown |
| `GET` | `/api/providers` | Provider status and metrics |
| `GET` | `/api/plugins` | Loaded plugins |
| `GET` | `/api/config` | Current configuration (sensitive fields redacted) |
| `GET` | `/api/stats/history` | Time-series stats |
| `GET` | `/api/security/budget` | Budget usage |
| `GET` | `/metrics` | Prometheus text exposition |

## CLI Reference

```
tokenman <command> [options]

Commands:
  start              Start the daemon
  stop               Stop the running daemon
  status             Show status and live stats
  setup              Interactive setup wizard
  keys               Manage API keys (list|set|delete <provider>)
  init-config        Generate default config file
  config-export      Export current config to file
  config-import      Import config from file
  install-service    Install as system service (launchd on macOS)
  version            Print version

Options:
  --foreground       Run in foreground (with 'start')
  --non-interactive  Skip interactive prompts (with 'setup')
```

## Configuration

Config file: `~/.tokenman/tokenman.toml`

Environment variables override config file values using the `TOKENMAN_` prefix with underscores for nesting:

```bash
TOKENMAN_SERVER_PROXY_PORT=8080
TOKENMAN_SERVER_LOG_LEVEL=debug
TOKENMAN_SECURITY_BUDGET_DAILY_LIMIT=500
```

See [`configs/tokenman.example.toml`](configs/tokenman.example.toml) for all options with documentation.

### API Keys

TokenMan resolves API keys from (in order):

1. **OS keychain** — `keyring://tokenman/<provider>` (recommended)
2. **Environment variable** — `env:TOKENMAN_KEY_ANTHROPIC`
3. **Legacy keychain format** — `keychain:tokenman/<provider>`

Store keys securely:

```bash
tokenman keys set anthropic    # prompts for key, stores in OS keychain
tokenman keys set openai
tokenman keys list             # shows which providers have keys stored
```

## Architecture

```
cmd/tokenman/           CLI entry point
internal/
  pipeline/             Middleware chain (request → response)
  proxy/                HTTP handler, upstream client, SSE streaming,
                        retry logic, circuit breaker
  cache/                Two-tier LRU + SQLite cache
  compress/             Dedup, rules, history, heartbeat, summarization
  security/             PII, injection, budget, rate limiting
  store/                SQLite (dual-connection: writer + reader pool)
  router/               Provider routing with fallback
  config/               TOML config, env vars, hot-reload (fsnotify)
  vault/                OS keychain integration
  metrics/              Atomic counters, dashboard API, Prometheus
  tracing/              OpenTelemetry tracer, HTTP middleware, span helpers
  tokenizer/            Token counting (tiktoken)
  plugin/               Plugin interfaces and registry
  daemon/               Process lifecycle, PID management, graceful shutdown
web/                    Embedded dashboard assets
```

### Request Flow

```
HTTP Request
  │
  ├─ [OTel] Extract trace context (traceparent)
  ├─ Generate request ID
  ├─ Detect format (Anthropic / OpenAI)
  ├─ Parse body, count input tokens
  │
  ▼
Pipeline Chain (request phase, in order):
  Cache → Injection → PII → Budget → RateLimit → Heartbeat → Dedup → Rules → History
  │  (each middleware is individually traced and timed)
  │
  ├─ Cache HIT? → Return cached response immediately
  │
  ▼
Upstream Forward (with retry + circuit breaker):
  ├─ [OTel] Inject traceparent into upstream request
  ├─ Check circuit breaker state per provider
  ├─ Retry on 429/502/503/504 with exponential backoff + jitter
  ├─ Fall back to next provider if current is exhausted
  │
  ▼
Pipeline Chain (response phase, reverse order):
  History → Rules → Dedup → Heartbeat → RateLimit → Budget → PII → Injection → Cache
  │
  ▼
Record metrics → Persist to SQLite → Write HTTP response
```

### Storage

SQLite with WAL mode. Single writer connection serializes all mutations; 4-connection reader pool handles concurrent queries. Schema is version-managed with incremental migrations. Automatic data pruning based on configurable retention days.

## Docker

### Build

```bash
make docker-build
# or manually:
docker build \
  --build-arg VERSION=$(git describe --tags --always) \
  --build-arg COMMIT=$(git rev-parse --short HEAD) \
  --build-arg DATE=$(date -u '+%Y-%m-%dT%H:%M:%SZ') \
  -t tokenman:latest .
```

### Run

```bash
make docker-run
# or manually:
docker run --rm -it \
  -p 7677:7677 -p 7678:7678 \
  -v tokenman-data:/data \
  tokenman:latest
```

The image:
- Uses a non-root `tokenman` user
- Persists data to `/data` (mount a volume)
- Includes a `HEALTHCHECK` that polls `/health`
- Exposes ports 7677 (proxy) and 7678 (dashboard)

Pass configuration via environment variables:

```bash
docker run --rm -it \
  -p 7677:7677 -p 7678:7678 \
  -v tokenman-data:/data \
  -e TOKENMAN_TRACING_ENABLED=true \
  -e TOKENMAN_TRACING_ENDPOINT=jaeger:4317 \
  -e TOKENMAN_TRACING_INSECURE=true \
  tokenman:latest
```

## Development

```bash
make build    # compile to bin/tokenman
make test     # tests with race detector
make lint     # golangci-lint
make run      # build + run in foreground
make clean    # remove binaries and Go cache
```

Run a single test:
```bash
go test -race -run TestName ./internal/package/
```

## License

MIT
