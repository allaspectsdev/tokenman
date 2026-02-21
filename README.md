# TokenMan

Local reverse proxy for LLM API calls. Token compression, PII scrubbing, budget enforcement, and full observability — in a single Go binary.

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
```

## Why

Every request to an LLM API costs tokens. Most development workflows send the same system prompts, tool definitions, and boilerplate context on every call. TokenMan sits between your client and the provider to:

- **Cache** identical requests and return stored responses instantly
- **Compress** repeated content (system prompts, tool defs, conversation history) using provider-native cache hints
- **Enforce budgets** with hourly/daily/monthly spend limits and alerts
- **Detect & redact PII** before it leaves your machine
- **Rate limit** per-provider to stay within API quotas
- **Track everything** — tokens, cost, savings, latency — in a local SQLite database with a web dashboard

All of this runs locally. Nothing is sent to any third party beyond the LLM providers you configure.

## Install

### From source

```bash
git clone https://github.com/allaspectsdev/tokenman.git
cd tokenman
make build
```

The binary lands at `bin/tokenman`.

### Requirements

- Go 1.25+

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
| `7678` | Dashboard & API |

### Point your client at TokenMan

**Claude Code:**
```bash
export ANTHROPIC_BASE_URL=http://localhost:7677
```

**Cursor:** Set the API base URL to `http://localhost:7677` in Cursor settings.

**curl:**
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

**OpenAI-compatible clients:**
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

Two-tier cache (in-memory LRU + SQLite) with configurable TTL. Identical requests return instantly with zero upstream cost. Cache hits are tagged with `X-Tokenman-Cache: HIT`.

### Token Compression

| Technique | What it does |
|-----------|-------------|
| **Content dedup** | Fingerprints system prompts and tool definitions. When repeated, annotates with `cache_control: ephemeral` for Anthropic prompt caching or reorders for OpenAI prefix matching. |
| **Whitespace collapse** | Strips redundant whitespace from text content |
| **JSON/XML minification** | Minifies embedded structured data in messages |
| **History windowing** | Keeps only the N most recent conversation turns, discarding older context |
| **Heartbeat dedup** | Collapses near-identical polling requests within a time window |
| **Conversation summarization** | Summarizes older messages using a lightweight LLM call when conversations exceed a threshold |

### Security

- **PII detection** — Identifies emails, phone numbers, SSNs, credit card numbers, and API keys. Actions: `redact`, `hash`, `log`, or `block`.
- **Prompt injection detection** — Flags suspicious patterns in user messages. Actions: `log`, `block`, `warn`, or `sanitize`.
- **Budget enforcement** — Hourly, daily, and monthly spend caps. Returns `429 Too Many Requests` when limits are hit, with structured error bodies.
- **Per-provider rate limiting** — Token-bucket rate limiter per provider to respect API quotas.

### Observability

- **Web dashboard** at `localhost:7678` — live stats, request history, cost breakdowns
- **JSON API** — `/api/stats`, `/api/requests`, `/api/projects`, `/api/providers`, `/api/security/pii`, `/api/security/budget`
- **Prometheus metrics** at `/metrics` — scrape-ready for Grafana/Alertmanager
- **Per-project tracking** — Tag requests with `X-Tokenman-Project: my-project` to track usage across projects
- **Request body inspection** — Full request/response bodies stored for debugging (configurable, capped at 1MB)

### Multi-Provider Routing

- Configure multiple providers with priority-based fallback
- Explicit model-to-provider mapping
- Automatic format detection (Anthropic vs OpenAI)

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
  proxy/                HTTP handler, upstream client, SSE streaming
  cache/                Two-tier LRU + SQLite cache
  compress/             Dedup, rules, history, heartbeat, summarization
  security/             PII, injection, budget, rate limiting
  store/                SQLite (dual-connection: writer + reader pool)
  router/               Provider routing with fallback
  config/               TOML config, env vars, hot-reload
  vault/                OS keychain integration
  metrics/              Atomic counters, dashboard API, Prometheus
  tokenizer/            Token counting (tiktoken)
  plugin/               Plugin interfaces and registry
  daemon/               Process lifecycle, PID management
web/                    Embedded dashboard assets
```

### Pipeline

Requests flow through an ordered middleware chain. Each middleware can inspect/modify the request, short-circuit with a cached response, or reject with an error:

```
Cache → Injection → PII → Budget → RateLimit → Heartbeat → Dedup → Rules → History
```

Responses flow back in reverse order. The cache middleware stores successful responses for future hits.

### Storage

SQLite with WAL mode. Single writer connection serializes all mutations; 4-connection reader pool handles concurrent queries. Schema is version-managed with incremental migrations.

## Development

```bash
make build    # compile to bin/tokenman
make test     # tests with race detector
make lint     # golangci-lint
make run      # build + run in foreground
```

Run a single test:
```bash
go test -race -run TestName ./internal/package/
```

## License

MIT
