# TokenMan — Remaining Production Hardening Work

## Completed Phases

### Phase 1: Security (Done)

- Dashboard auth middleware (Bearer token, constant-time comparison)
- `http.MaxBytesReader` on proxy handler (configurable `max_body_size`)
- `ReadTimeout`/`WriteTimeout`/`IdleTimeout` on proxy server
- CORS restricted to configured `allowed_origins` list
- TLS support via `tls_enabled`/`cert_file`/`key_file` config
- Sanitized error messages returned to clients

### Phase 2: Resilience (Done)

- Retry with exponential backoff + full jitter for upstream calls
- Per-provider circuit breaker (Closed/Open/HalfOpen state machine) with registry
- Upstream HTTP status code propagation (4xx/5xx forwarded, Retry-After for 429s)
- Panic recovery in pipeline chain (`recoverMiddleware` wrapper)
- Panic recovery in cache purger and data pruner goroutines
- Response size limits (`max_response_size`, `io.LimitReader`)
- Streaming accumulator cap (graceful degradation — client gets full stream, internal accounting is capped)
- Streaming timeout via context deadline (`stream_timeout` config)
- Cache purger shutdown synchronization (returns `<-chan struct{}`, daemon waits before `st.Close()`)
- Data pruner shutdown synchronization (WaitGroup-style channel)
- PID file removal errors logged at all three call sites
- `ResilienceConfig` with retry/circuit-breaker tuning knobs
- Full test coverage: pipeline panic tests, circuit breaker state machine tests, retry/backoff tests, handler retry/exhaustion/status-propagation tests

### Phase 3: Observability (Done)

- `tokenman_errors_total{type, provider, status_code}` counter — incremented for parse, budget, pipeline, upstream, and timeout errors
- `tokenman_request_duration_seconds{provider, model, streaming}` histogram — buckets tuned for LLM latency (100ms to 120s), observed on all request paths
- `tokenman_provider_requests_total{provider, status}` counter — tracks success, error, circuit_open per provider
- `tokenman_provider_circuit_state{provider}` gauge — 0=closed, 1=open, 2=half-open
- `tokenman_middleware_duration_seconds{middleware, phase}` histogram — per-middleware request/response phase timing
- `GET /health/ready` readiness probe — checks DB connectivity (`Ping()`) and provider availability, returns 200/503 with JSON details
- `Store.Ping()` method for database health checks
- Custom Prometheus text exposition: labeled counters, histograms (with cumulative bucket counts, sum, count), and gauges — no external Prometheus client library dependency
- All existing tests pass, plus new readiness probe tests

### Phase 4: Testing & Deployment (Done)

- **4.1 Integration tests** — Full request flow tests in `integration_test.go`: Anthropic/OpenAI normal requests, streaming, upstream 429/500 propagation, retry success, circuit breaker trip, request persistence, project header, max body size, response size limit, models endpoint, health/readiness endpoints
- **4.2 Package tests** — New test files for all previously untested packages:
  - `config/config_test.go` — Load with temp TOML, env var overrides, export/import, defaults
  - `config/validate_test.go` — Validation edge cases: bad ports, log levels, TLS, resilience, budgets, PII/injection actions
  - `daemon/pidfile_test.go` — WritePID/ReadPID/RemovePID/IsRunning with temp directories
  - `vault/vault_test.go` — ResolveKeyRef with env:/keyring:/keychain: formats, error cases
  - `metrics/collector_test.go` — Atomic operations, Stats aggregation, concurrent records, labeled metrics
  - `metrics/api_test.go` — All dashboard API endpoints via httptest, auth middleware, CORS
  - `plugin/registry_test.go` — Register/Unregister/List, duplicate detection, middleware categorization, CloseAll
  - `store/store_test.go` — Open/Close, InsertRequest/GetRequest, ListRequests, GetRequestStats, Prune, concurrent read/write, WAL mode, migrations
- **4.3 Streaming tests** — `streaming_test.go`: Anthropic/OpenAI SSE formats, accumulator cap, context cancellation, empty stream, extractDelta for all formats
- **4.4 Timeout config fix** — Changed example TOML `timeout = "30s"` to `timeout = 30` (integer seconds matching the `int` field type)
- **4.5 Hot-reload refresh** — `RateLimitMiddleware.Reconfigure()` rebuilds token buckets, `CacheMiddleware.SetTTL()` updates TTL; both wired into daemon's config watcher `OnChange` callback
- **4.6 Production Dockerfile** — Multi-stage build (Go 1.25 builder + Alpine 3.21 runtime), non-root `tokenman` user, `/data` volume, HEALTHCHECK, exposed ports 7677/7678, `docker-build` and `docker-run` Makefile targets, `.dockerignore`

---

## Remaining (Optional / Low Priority)

### OpenTelemetry tracing (deferred)

**Issue:** No distributed tracing or correlation ID propagation.

**Plan:**
- Add optional `otel` dependency behind a build tag or config flag
- Inject trace context via chi middleware
- Create spans for: pipeline processing, upstream forward, each middleware
- Propagate `X-Request-Id` / `traceparent` headers to upstream

**Files:** New `internal/tracing/` package, `internal/proxy/handler.go`, `internal/daemon/daemon.go`, `go.mod`

---

## Medium Issues (Address Opportunistically)

### M1. Providers map not thread-safe (#18)

**Risk:** Latent race if hot-reload ever updates providers at runtime.

**Fix:** Wrap `h.providers` with `sync.RWMutex` or use `atomic.Pointer`. Low urgency since providers are only set once at startup today, but becomes critical if hot-reload extends to provider config.

**Files:** `internal/proxy/handler.go`

### M2. Error channel partial drain (#19)

**Risk:** If both proxy and dashboard servers fail simultaneously, one error is dropped and a goroutine may block.

**Fix:** Drain the error channel in a loop after shutdown, or switch to `errgroup`.

**Files:** `internal/daemon/daemon.go`

### M3. Database persistence errors silently dropped (#13)

**Risk:** `InsertRequest()` failures mean silent data loss for metrics and audit.

**Fix:** Log errors from `InsertRequest` calls. Consider a buffered retry queue for transient DB errors.

**Files:** `internal/proxy/handler.go`, `internal/cache/cache.go`, `internal/security/budget.go`
