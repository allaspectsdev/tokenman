# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Development Commands

```bash
make build          # Compile to bin/tokenman (injects version via ldflags)
make test           # Run tests with race detector and coverage
make lint           # Static analysis with golangci-lint
make run            # Build and run daemon in foreground
make clean          # Remove binaries and Go cache
```

Single test: `go test -race -run TestName ./internal/package/`

## Architecture

TokenMan is an LLM token management proxy that sits between clients and LLM providers (Anthropic, OpenAI). It provides caching, token compression, security features, and metrics via a middleware pipeline.

### Request Flow

```
HTTP Request → Proxy Handler (format detection: Anthropic/OpenAI)
  → Pipeline Chain (ordered middleware):
      Cache → Injection → PII → Budget → RateLimit → Heartbeat → Dedup → Rules → History
  → RawBody rebuild → Upstream Client → LLM Provider
  → Response Pipeline (reverse middleware order)
  → Metrics Collection → SQLite Persistence
  → HTTP Response (with X-Tokenman-Cache: HIT/MISS header)
```

### Key Packages

- **`cmd/tokenman/`** — CLI entry point. Commands: start, stop, status, setup, keys, init-config, install-service, config-export, config-import.
- **`internal/pipeline/`** — Core middleware chain. `Middleware` interface has `ProcessRequest` and `ProcessResponse` methods. Chain executes forward on request, reverse on response.
- **`internal/proxy/`** — HTTP handler (chi router), upstream forwarding, SSE streaming, Anthropic/OpenAI format detection, session-based stream manager.
- **`internal/compress/`** — Token compression: content dedup with fingerprinting, whitespace/JSON/XML minification, conversation history windowing, heartbeat dedup, LLM-based conversation summarization.
- **`internal/cache/`** — Two-tier cache: in-memory LRU (hashicorp/golang-lru) + persistent SQLite. TTL-based expiration with background purger.
- **`internal/security/`** — PII detection/redaction with bidirectional placeholder mapping, prompt injection detection, spend budget enforcement, per-provider token-bucket rate limiting.
- **`internal/store/`** — SQLite with dual-connection pattern: single writer + 4-connection reader pool, WAL mode. Versioned schema migrations. Store adapters bridge middleware interfaces.
- **`internal/router/`** — Provider routing with priority-based fallback. Resolution: explicit model map → provider model list → default provider.
- **`internal/config/`** — Viper-based TOML config with env var overrides (`TOKENMAN_` prefix), hot-reload via fsnotify, thread-safe atomic pointer. Export/import support.
- **`internal/vault/`** — API key storage via OS keychain (go-keyring) with env var fallback. Keys referenced as `keyring://tokenman/provider`.
- **`internal/metrics/`** — Lock-free atomic counters, Prometheus `/metrics` endpoint, JSON API endpoints (`/api/stats`, `/api/requests`, `/api/projects`, `/api/plugins`), embedded web dashboard.
- **`internal/tokenizer/`** — Token counting via tiktoken with cached encodings (sync.Once).
- **`internal/daemon/`** — Process lifecycle, PID file management, graceful shutdown (30s timeout), periodic data pruning. Full proxy stack wiring.
- **`internal/plugin/`** — Plugin system with Registry. Plugin interfaces: MiddlewarePlugin, TransformPlugin, HookPlugin.
- **`web/`** — Embedded static assets (HTML/CSS/JS) for the metrics dashboard.

### Design Patterns

- **Pipeline/Chain**: Middleware executes sequentially with per-middleware timing. Short-circuits on cache hits.
- **Dual-connection SQLite**: Writer serializes mutations; reader pool serves concurrent queries with query-only pragmas.
- **Atomic metrics**: Float64 values stored as uint64 via `math.Float64bits` for lock-free updates.
- **Config hot-reload**: fsnotify watcher swaps config via atomic pointer.

### Ports

- `7677` — Proxy API (client-facing)
- `7678` — Dashboard/metrics API

### Config

Default location: `~/.tokenman/tokenman.toml`. See `configs/tokenman.example.toml` for all options.
