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

---

## Phase 3: Observability

### 3.1 Error rate counter metrics

**Issue:** No `tokenman_errors_total` counter by error type. Operators can't alert on error rate spikes.

**Plan:**
- Add a Prometheus `CounterVec` to `internal/metrics/collector.go` with labels `{type, provider, status_code}`
- Increment in `handler.go` on upstream errors, pipeline errors, parse errors, and timeout errors
- Register the counter in `PrometheusHandler`

**Files:** `internal/metrics/collector.go`, `internal/metrics/prometheus.go`, `internal/proxy/handler.go`

### 3.2 Latency histogram metrics

**Issue:** No p50/p95/p99 latency data. Essential for SLO enforcement.

**Plan:**
- Add a Prometheus `HistogramVec` with labels `{provider, model, streaming}` and buckets tuned for LLM latency (100ms to 120s)
- Observe in `handler.go` at request completion (both streaming and non-streaming paths)
- Register in `PrometheusHandler`

**Files:** `internal/metrics/collector.go`, `internal/metrics/prometheus.go`, `internal/proxy/handler.go`

### 3.3 Per-provider health and error metrics

**Issue:** No visibility into which providers are healthy vs degraded.

**Plan:**
- Add `tokenman_provider_requests_total{provider, status}` and `tokenman_provider_circuit_state{provider}` gauges
- Emit provider name from `forwardWithRetry` and the simple forward path
- Expose circuit breaker state as a labeled gauge (0=closed, 1=open, 2=half-open)

**Files:** `internal/metrics/collector.go`, `internal/proxy/handler.go`, `internal/proxy/circuitbreaker.go`

### 3.4 Readiness probe

**Issue:** `/health` returns `{"status":"ok"}` without checking DB or upstream connectivity. Kubernetes can't detect an unready state.

**Plan:**
- Add `GET /health/ready` endpoint to `proxy/server.go`
- Check: SQLite writer connection alive (simple `SELECT 1`), at least one provider configured
- Return 200 if all checks pass, 503 with details if any fail
- Keep `/health` as a liveness probe (always 200 if process is running)

**Files:** `internal/proxy/handler.go`, `internal/proxy/server.go`, `internal/store/store.go` (add `Ping()` method)

### 3.5 Per-middleware timing as Prometheus metrics

**Issue:** Pipeline timing is tracked internally but not exposed to Prometheus.

**Plan:**
- After `ProcessRequest` and `ProcessResponse`, read `chain.Timings()` and observe into a `HistogramVec` with label `{middleware, phase}`
- Only do this if a Prometheus collector is configured (avoid allocation in hot path when metrics are off)

**Files:** `internal/proxy/handler.go`, `internal/metrics/collector.go`

### 3.6 OpenTelemetry tracing (optional)

**Issue:** No distributed tracing or correlation ID propagation.

**Plan:**
- Add optional `otel` dependency behind a build tag or config flag
- Inject trace context via chi middleware
- Create spans for: pipeline processing, upstream forward, each middleware
- Propagate `X-Request-Id` / `traceparent` headers to upstream
- This is lower priority than the other observability items

**Files:** New `internal/tracing/` package, `internal/proxy/handler.go`, `internal/daemon/daemon.go`, `go.mod`

---

## Phase 4: Testing & Deployment

### 4.1 Integration tests for full request flow

**Issue:** No end-to-end tests that exercise proxy -> pipeline -> upstream mock -> response.

**Plan:**
- Create `internal/proxy/integration_test.go` with `TestIntegration_` prefix
- Test scenarios: normal request, cache hit, streaming, budget exceeded, provider failover, circuit breaker trip, retry success, upstream 429 propagation
- Use `httptest.Server` as upstream mock, real pipeline chain with real middleware instances, in-memory SQLite store

**Files:** `internal/proxy/integration_test.go`

### 4.2 Tests for untested packages

**Package test gaps and plan for each:**

| Package | Plan |
|---------|------|
| `internal/config/` | Test `Load()` with temp TOML files, env var overrides, validation edge cases (bad ports, missing TLS files, invalid resilience values) |
| `internal/daemon/` | Test `WritePID`/`ReadPID`/`RemovePID`/`IsRunning` with temp directories. Test `runPruner` cancellation. |
| `internal/vault/` | Test `ResolveKeyRef` with `env://` refs (mock env vars), error cases for missing keys |
| `internal/metrics/` | Test `Collector` atomic operations, `Stats` aggregation, dashboard API endpoints with `httptest` |
| `internal/plugin/` | Test `Registry` lifecycle: register, get, list, enable/disable |
| `internal/store/` (core) | Test `Open`/`Close`, `InsertRequest`/`GetRequests`, `Prune`, concurrent read/write, WAL mode verification |

**Files:** `internal/config/config_test.go`, `internal/config/validate_test.go`, `internal/daemon/pidfile_test.go`, `internal/vault/vault_test.go`, `internal/metrics/collector_test.go`, `internal/metrics/api_test.go`, `internal/plugin/registry_test.go`, `internal/store/store_test.go`

### 4.3 Streaming tests

**Issue:** No tests for SSE streaming path.

**Plan:**
- Test `HandleStreaming` with mock SSE responses (Anthropic and OpenAI formats)
- Test accumulator cap behavior
- Test context cancellation (stream timeout)
- Test upstream error response before SSE parsing

**Files:** `internal/proxy/streaming_test.go`

### 4.4 Fix timeout config type mismatch

**Issue (#9):** `ProviderConfig.Timeout` is `int` (seconds) but `tokenman.example.toml` uses `"30s"` string format. The `StringToTimeDurationHookFunc` in viper converts strings to `time.Duration`, not `int`. Using the example config as-is will fail on startup.

**Plan:**
- Change `ProviderConfig.Timeout` from `int` to `int` and fix example config to use `30` (integer seconds), OR
- Change the field to `time.Duration` and update `TimeoutDuration()` accordingly
- Simplest fix: change the example TOML from `"30s"` to `30` since the field is documented as seconds

**Files:** `configs/tokenman.example.toml` (or `internal/config/config.go` if changing the type)

### 4.5 Hot-reload refresh for rate limit buckets and cache TTLs

**Issue (#17):** Config hot-reload updates the atomic config pointer but doesn't refresh existing rate limiter token buckets or cache TTL values. Changes require a restart.

**Plan:**
- Add a `Reconfigure(rate, burst)` method to `RateLimitMiddleware` that rebuilds the token buckets
- Add a `SetTTL(ttl)` method to `CacheMiddleware`
- In the `watcher.OnChange` callback in `daemon.go`, call these methods with the new config values
- Consider which other middleware might benefit from hot-reload (budget limits, PII settings)

**Files:** `internal/security/ratelimit.go`, `internal/cache/cache.go`, `internal/daemon/daemon.go`

### 4.6 Production Dockerfile

**Issue (#24):** No container build target.

**Plan:**
- Multi-stage Dockerfile: Go builder stage + minimal `scratch` or `alpine` runtime stage
- Non-root user, read-only filesystem (except data dir volume)
- `HEALTHCHECK` instruction using `/health`
- Expose ports 7677 and 7678
- Add `docker-build` and `docker-run` targets to Makefile

**Files:** `Dockerfile`, `Makefile`, `.dockerignore`

---

## Medium Issues (Address Opportunistically)

### M1. Providers map not thread-safe (#18)

**Risk:** Latent race if hot-reload ever updates providers at runtime.

**Fix:** Wrap `h.providers` with `sync.RWMutex` or use `atomic.Pointer`. Low urgency since providers are only set once at startup today, but becomes critical if Phase 4.5 (hot-reload) extends to provider config.

**Files:** `internal/proxy/handler.go`

### M2. Error channel partial drain (#19)

**Risk:** If both proxy and dashboard servers fail simultaneously, one error is dropped and a goroutine may block.

**Fix:** Drain the error channel in a loop after shutdown, or switch to `errgroup`.

**Files:** `internal/daemon/daemon.go`

### M3. Database persistence errors silently dropped (#13)

**Risk:** `InsertRequest()` failures mean silent data loss for metrics and audit.

**Fix:** Log errors from `InsertRequest` calls. Consider a buffered retry queue for transient DB errors.

**Files:** `internal/proxy/handler.go`, `internal/cache/cache.go`, `internal/security/budget.go`

---

## Recommended Priority Order

1. **Phase 3.1 + 3.2** — Error counters and latency histograms (highest operational impact)
2. **Phase 4.4** — Fix timeout config type mismatch (correctness bug)
3. **Phase 4.2 + 4.3** — Test coverage (confidence for all changes)
4. **Phase 3.4** — Readiness probe (required for Kubernetes)
5. **Phase 4.1** — Integration tests
6. **Phase 3.3 + 3.5** — Provider health metrics and middleware timing
7. **Phase 4.5** — Hot-reload refresh
8. **Phase 4.6** — Dockerfile
9. **Phase 3.6** — OpenTelemetry (nice-to-have)
10. **M1-M3** — Address as encountered
