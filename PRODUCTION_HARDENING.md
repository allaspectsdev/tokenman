# TokenMan Production Hardening Plan

## Verdict: Not Yet Production Ready

The architecture is solid — pipeline pattern, dual-connection SQLite, atomic metrics, structured logging — but there are critical gaps in security, resilience, and testing that need to be addressed before going live.

---

## CRITICAL Issues (Must Fix)

### 1. No Authentication on Dashboard API
`internal/metrics/api.go:46-60` — All dashboard routes (`/api/config`, `/api/security/pii`, `/api/security/budget`, `/api/requests`) are completely open. The `POST /api/config` endpoint allows unauthenticated config modification. Auth config exists but defaults to `false` and has no enforcing middleware.

### 2. No Request Body Size Limit on Proxy
`internal/proxy/handler.go:130` — `io.ReadAll(r.Body)` with no limit. The dashboard API correctly uses `io.LimitReader` (1MB), but the main proxy handler does not. Enables memory exhaustion DoS.

### 3. No HTTP Server Timeouts on Proxy
`internal/proxy/server.go:47-50` — The proxy `http.Server` has zero `ReadTimeout`, `WriteTimeout`, or `IdleTimeout`. The dashboard server correctly sets all three. Without these, the proxy is vulnerable to Slowloris attacks and zombie connections.

### 4. No Retry/Circuit Breaker for Upstream Providers
`internal/proxy/upstream.go` — Single-attempt, fail-fast model. No retry with backoff, no circuit breaker, no provider failover. If Anthropic is temporarily down, every request fails instantly with a generic 502 — no attempt to try OpenAI as a fallback despite priority-based routing existing in `internal/router/`.

### 5. Upstream HTTP Status Codes Swallowed
`internal/proxy/handler.go:245-250` — All upstream failures become HTTP 502 regardless of actual status. A 429 from Anthropic (rate limited), 401 (bad key), or 503 (overloaded) all look the same to the client.

### 6. No Panic Recovery in Middleware Chain
`internal/pipeline/chain.go:44-82` — The pipeline has no `defer recover()`. A panic in any middleware crashes the entire request. Chi's `middleware.Recoverer` covers the HTTP layer but not the pipeline internals.

### 7. Permissive CORS (`Access-Control-Allow-Origin: *`)
`internal/metrics/api.go:608` — Dashboard API allows requests from any origin, enabling cross-site attacks against sensitive metrics/PII/budget data.

### 8. No TLS Support
Neither `internal/proxy/server.go` nor `internal/metrics/api.go` support TLS. No `CertFile`/`KeyFile` config options exist. All traffic including API keys travels in cleartext.

---

## HIGH Issues (Should Fix Before Production)

### 9. Timeout Config Type Mismatch
`internal/config/config.go:73` — `Timeout` field is `int` but `configs/tokenman.example.toml:54` uses `"30s"` string format. The `StringToTimeDurationHookFunc` converts to `time.Duration`, not `int`. Using the example config will fail on startup.

### 10. Unbounded Response Body Reads
`internal/proxy/handler.go:310,453` — `io.ReadAll(upstreamResp.Body)` without limits. A misbehaving upstream could exhaust memory.

### 11. Streaming Content Accumulator Unbounded
`internal/proxy/streaming.go:27` — `strings.Builder` accumulates all stream deltas with no cap. Long-running streams could consume unlimited memory.

### 12. Streaming Client Leaks
`internal/proxy/upstream.go:81-84` — New `http.Client` per streaming request with zero timeout (no deadline at all). Stalled streams can hang indefinitely.

### 13. Database Persistence Errors Silently Dropped
`internal/proxy/handler.go:278-299`, `internal/cache/cache.go:192-195`, `internal/security/budget.go:163-166` — `InsertRequest()` and similar calls have no error handling. Silent data loss means metrics and audit trails become unreliable without any operational visibility.

### 14. Background Goroutines Have No Panic Recovery
Cache purger (`internal/cache/cache.go:204-216`), data pruner (`internal/daemon/daemon.go:407-428`), config watcher callbacks — all run without `defer recover()`. A panic silently kills the subsystem.

### 15. Cache Purger Races Shutdown
`internal/daemon/daemon.go:314-315` — `pruneCancel()` and `st.Close()` execute back-to-back with no sync. The purger goroutine may attempt a DB query after the store is closed.

### 16. RemovePID Error Ignored
`internal/daemon/daemon.go:316` — If PID file removal fails, the service won't restart without manual cleanup.

### 17. Hot-Reload Doesn't Refresh Rate Limit Buckets or Cache TTLs
`internal/security/ratelimit.go` — Existing token buckets retain stale rates after config reload. `internal/cache/cache.go` — Existing cache entries keep old TTLs. Config changes require a restart to take effect.

---

## MEDIUM Issues

### 18. Providers Map Not Thread-Safe
`internal/proxy/handler.go:49,72-74` — `providers` map has no mutex. Safe today (only set at init), but a latent race condition if hot-reload ever updates providers.

### 19. Error Channel Partial Drain
`internal/daemon/daemon.go:243-250` — Buffered channel of 2, but only one error is read. If both servers fail, one error is silently dropped and a goroutine may block.

### 20. Error Messages Expose Internals
`internal/proxy/handler.go:151` — `fmt.Sprintf("failed to parse request: %v", err)` leaks parse error details to clients.

### 21. No Readiness Probe
`/health` returns `{"status":"ok"}` without checking database connectivity, upstream provider reachability, or cache health. Kubernetes can't detect an unready state.

### 22. No Error Rate or Latency Histogram Metrics
Prometheus metrics cover counts and gauges but lack `_errors_total` counters by type and p50/p95/p99 latency histograms — essential for SLO enforcement.

### 23. No Distributed Tracing
No OpenTelemetry integration. No correlation ID propagation. Debugging multi-step request flows in production will be difficult.

### 24. No Dockerfile
No container build target in the Makefile. Teams will create ad-hoc Dockerfiles with inconsistent security posture.

---

## Test Coverage Gaps

| Package | Status |
|---------|--------|
| `internal/pipeline/` | Good |
| `internal/cache/` | Good |
| `internal/compress/` | Good (4 test files) |
| `internal/security/` | Good (budget + PII) |
| `internal/router/` | Good |
| `internal/proxy/` | Weak — 7 tests, no streaming tests |
| `internal/store/` | Weak — only adapter tests, no core DB tests |
| `internal/vault/` | **No tests** |
| `internal/daemon/` | **No tests** |
| `internal/metrics/` | **No tests** |
| `internal/config/` | **No tests** (validate.go untested) |
| `internal/plugin/` | **No tests** |
| Integration tests | **None** |

---

## What's Already Good

- Structured logging with zerolog (file + console, configurable levels)
- Parameterized SQL queries everywhere (no injection risk)
- Dual-connection SQLite with WAL mode (well-implemented)
- Lock-free atomic metrics using `math.Float64bits` CAS loops
- Config hot-reload with fsnotify debouncing
- PII detection/redaction with bidirectional placeholder mapping
- Prompt injection detection with multiple pattern categories
- Token-bucket rate limiting (correct implementation)
- Graceful shutdown with 30s timeout and signal handling
- Version injection via ldflags in build

---

## Phase 1: Security

1. Add auth middleware to dashboard routes (Bearer token or API key)
2. Add `http.MaxBytesReader` to proxy handler (e.g., 10MB limit)
3. Add `ReadTimeout`/`WriteTimeout`/`IdleTimeout` to proxy server
4. Restrict CORS to configured origins
5. Add TLS support with `CertFile`/`KeyFile` config options
6. Sanitize error messages returned to clients

## Phase 2: Resilience

7. Add retry with exponential backoff for upstream calls
8. Implement circuit breaker per provider with automatic fallback
9. Propagate upstream HTTP status codes to clients
10. Add panic recovery to pipeline chain and background goroutines
11. Add size limits to response body reads and streaming accumulator
12. Fix streaming client to use context deadlines instead of no timeout
13. Synchronize cache purger shutdown before store close
14. Log PID file removal errors

## Phase 3: Observability

15. Add error rate counter metrics (`tokenman_errors_total{type="..."}`)
16. Add latency histogram metrics (p50/p95/p99)
17. Add per-provider health and error metrics
18. Implement readiness probe that checks DB + upstream connectivity
19. Add OpenTelemetry tracing (optional but recommended)
20. Expose per-middleware timing as Prometheus metrics

## Phase 4: Testing & Deployment

21. Add integration tests for full request flow (proxy -> pipeline -> upstream mock)
22. Add tests for untested packages (daemon, vault, config, metrics, plugin, store core)
23. Add streaming tests
24. Fix timeout config type mismatch
25. Fix hot-reload to refresh rate limit buckets and cache TTLs
26. Create production Dockerfile (multi-stage, non-root, health check)
27. Add Makefile `docker-build` target
