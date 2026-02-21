package metrics

import (
	"math"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/allaspectsdev/tokenman/internal/pipeline"
)

// labeledCounter tracks a counter value for a specific label combination.
type labeledCounter struct {
	labels map[string]string
	value  int64
}

// histogram tracks a distribution of observed values using pre-defined buckets.
type histogram struct {
	mu      sync.Mutex
	labels  map[string]string
	buckets []float64 // upper bounds, sorted ascending
	counts  []int64   // count per bucket
	sum     float64
	count   int64
}

func newHistogram(labels map[string]string, buckets []float64) *histogram {
	sorted := make([]float64, len(buckets))
	copy(sorted, buckets)
	sort.Float64s(sorted)
	return &histogram{
		labels:  labels,
		buckets: sorted,
		counts:  make([]int64, len(sorted)),
	}
}

func (h *histogram) observe(v float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sum += v
	h.count++
	for i, bound := range h.buckets {
		if v <= bound {
			h.counts[i]++
		}
	}
}

// counterVec is a thread-safe collection of labeled counters.
type counterVec struct {
	mu       sync.RWMutex
	counters map[string]*labeledCounter
}

func newCounterVec() *counterVec {
	return &counterVec{counters: make(map[string]*labeledCounter)}
}

func (cv *counterVec) inc(labels map[string]string) {
	key := labelsKey(labels)
	cv.mu.Lock()
	c, ok := cv.counters[key]
	if !ok {
		c = &labeledCounter{labels: copyLabels(labels)}
		cv.counters[key] = c
	}
	cv.mu.Unlock()
	atomic.AddInt64(&c.value, 1)
}

func (cv *counterVec) snapshot() []labeledCounter {
	cv.mu.RLock()
	defer cv.mu.RUnlock()
	result := make([]labeledCounter, 0, len(cv.counters))
	for _, c := range cv.counters {
		result = append(result, labeledCounter{
			labels: copyLabels(c.labels),
			value:  atomic.LoadInt64(&c.value),
		})
	}
	return result
}

// histogramVec is a thread-safe collection of labeled histograms.
type histogramVec struct {
	mu         sync.RWMutex
	histograms map[string]*histogram
	buckets    []float64
}

func newHistogramVec(buckets []float64) *histogramVec {
	return &histogramVec{
		histograms: make(map[string]*histogram),
		buckets:    buckets,
	}
}

func (hv *histogramVec) observe(labels map[string]string, v float64) {
	key := labelsKey(labels)
	hv.mu.RLock()
	h, ok := hv.histograms[key]
	hv.mu.RUnlock()
	if !ok {
		hv.mu.Lock()
		h, ok = hv.histograms[key]
		if !ok {
			h = newHistogram(copyLabels(labels), hv.buckets)
			hv.histograms[key] = h
		}
		hv.mu.Unlock()
	}
	h.observe(v)
}

func (hv *histogramVec) snapshot() []*histogram {
	hv.mu.RLock()
	defer hv.mu.RUnlock()
	result := make([]*histogram, 0, len(hv.histograms))
	for _, h := range hv.histograms {
		h.mu.Lock()
		snap := &histogram{
			labels:  copyLabels(h.labels),
			buckets: h.buckets,
			counts:  make([]int64, len(h.counts)),
			sum:     h.sum,
			count:   h.count,
		}
		copy(snap.counts, h.counts)
		h.mu.Unlock()
		result = append(result, snap)
	}
	return result
}

// gaugeVec tracks a set of labeled gauges that can be set to any value.
type gaugeVec struct {
	mu     sync.RWMutex
	gauges map[string]*labeledGauge
}

type labeledGauge struct {
	labels map[string]string
	value  uint64 // float64 stored via math.Float64bits
}

func newGaugeVec() *gaugeVec {
	return &gaugeVec{gauges: make(map[string]*labeledGauge)}
}

func (gv *gaugeVec) set(labels map[string]string, v float64) {
	key := labelsKey(labels)
	gv.mu.Lock()
	g, ok := gv.gauges[key]
	if !ok {
		g = &labeledGauge{labels: copyLabels(labels)}
		gv.gauges[key] = g
	}
	gv.mu.Unlock()
	atomic.StoreUint64(&g.value, math.Float64bits(v))
}

func (gv *gaugeVec) snapshot() []struct {
	labels map[string]string
	value  float64
} {
	gv.mu.RLock()
	defer gv.mu.RUnlock()
	result := make([]struct {
		labels map[string]string
		value  float64
	}, 0, len(gv.gauges))
	for _, g := range gv.gauges {
		result = append(result, struct {
			labels map[string]string
			value  float64
		}{
			labels: copyLabels(g.labels),
			value:  math.Float64frombits(atomic.LoadUint64(&g.value)),
		})
	}
	return result
}

func labelsKey(labels map[string]string) string {
	// Build a deterministic key from sorted label pairs.
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	key := ""
	for _, k := range keys {
		key += k + "=" + labels[k] + ","
	}
	return key
}

func copyLabels(labels map[string]string) map[string]string {
	cp := make(map[string]string, len(labels))
	for k, v := range labels {
		cp[k] = v
	}
	return cp
}

// Collector tracks live metrics using atomic counters for lock-free,
// concurrent-safe updates. It provides an in-memory real-time view of
// request throughput, token usage, cost, savings, and cache performance.
type Collector struct {
	totalRequests  int64
	totalTokensIn  int64
	totalTokensOut int64
	totalTokensSaved int64

	// Float64 counters stored as uint64 via math.Float64bits/Float64frombits.
	totalCostUSD    uint64
	totalSavingsUSD uint64

	cacheHits   int64
	cacheMisses int64

	activeRequests int64

	startTime time.Time

	// Labeled Prometheus-style metrics.
	errors           *counterVec   // labels: type, provider, status_code
	latency          *histogramVec // labels: provider, model, streaming
	providerRequests *counterVec   // labels: provider, status
	circuitState     *gaugeVec     // labels: provider
	middlewareTime   *histogramVec // labels: middleware, phase
}

// Stats is a point-in-time snapshot of the collector's counters,
// suitable for JSON serialisation and display on the dashboard.
type Stats struct {
	Uptime         string  `json:"uptime"`
	TotalRequests  int64   `json:"total_requests"`
	TokensIn       int64   `json:"tokens_in"`
	TokensOut      int64   `json:"tokens_out"`
	TokensSaved    int64   `json:"tokens_saved"`
	CostUSD        float64 `json:"cost_usd"`
	SavingsUSD     float64 `json:"savings_usd"`
	SavingsPercent float64 `json:"savings_percent"`
	CacheHitRate   float64 `json:"cache_hit_rate"`
	CacheHits      int64   `json:"cache_hits"`
	CacheMisses    int64   `json:"cache_misses"`
	ActiveRequests int64   `json:"active_requests"`
}

// latencyBuckets are tuned for LLM API call durations.
var latencyBuckets = []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120}

// middlewareBuckets are tuned for per-middleware execution times (smaller).
var middlewareBuckets = []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5}

// NewCollector creates a new Collector with all counters initialised to zero
// and the start time set to now.
func NewCollector() *Collector {
	return &Collector{
		startTime:        time.Now(),
		totalCostUSD:     math.Float64bits(0),
		totalSavingsUSD:  math.Float64bits(0),
		errors:           newCounterVec(),
		latency:          newHistogramVec(latencyBuckets),
		providerRequests: newCounterVec(),
		circuitState:     newGaugeVec(),
		middlewareTime:   newHistogramVec(middlewareBuckets),
	}
}

// Record atomically updates all counters from the completed request/response pair.
func (c *Collector) Record(req *pipeline.Request, resp *pipeline.Response) {
	atomic.AddInt64(&c.totalRequests, 1)
	atomic.AddInt64(&c.totalTokensIn, int64(req.TokensIn))
	atomic.AddInt64(&c.totalTokensOut, int64(resp.TokensOut))
	atomic.AddInt64(&c.totalTokensSaved, int64(resp.TokensSaved))

	addFloat64(&c.totalCostUSD, resp.CostUSD)
	addFloat64(&c.totalSavingsUSD, resp.SavingsUSD)

	if resp.CacheHit {
		atomic.AddInt64(&c.cacheHits, 1)
	} else {
		atomic.AddInt64(&c.cacheMisses, 1)
	}
}

// IncrementActive increments the active request counter. Call this when a
// request enters the pipeline.
func (c *Collector) IncrementActive() {
	atomic.AddInt64(&c.activeRequests, 1)
}

// DecrementActive decrements the active request counter. Call this when a
// request leaves the pipeline (regardless of success or failure).
func (c *Collector) DecrementActive() {
	atomic.AddInt64(&c.activeRequests, -1)
}

// Stats returns a point-in-time snapshot of all metrics.
func (c *Collector) Stats() *Stats {
	totalReqs := atomic.LoadInt64(&c.totalRequests)
	hits := atomic.LoadInt64(&c.cacheHits)
	misses := atomic.LoadInt64(&c.cacheMisses)
	costUSD := loadFloat64(&c.totalCostUSD)
	savingsUSD := loadFloat64(&c.totalSavingsUSD)

	var hitRate float64
	totalCacheOps := hits + misses
	if totalCacheOps > 0 {
		hitRate = float64(hits) / float64(totalCacheOps) * 100
	}

	var savingsPercent float64
	totalSpend := costUSD + savingsUSD
	if totalSpend > 0 {
		savingsPercent = savingsUSD / totalSpend * 100
	}

	return &Stats{
		Uptime:         formatDuration(time.Since(c.startTime)),
		TotalRequests:  totalReqs,
		TokensIn:       atomic.LoadInt64(&c.totalTokensIn),
		TokensOut:      atomic.LoadInt64(&c.totalTokensOut),
		TokensSaved:    atomic.LoadInt64(&c.totalTokensSaved),
		CostUSD:        costUSD,
		SavingsUSD:     savingsUSD,
		SavingsPercent: savingsPercent,
		CacheHitRate:   hitRate,
		CacheHits:      hits,
		CacheMisses:    misses,
		ActiveRequests: atomic.LoadInt64(&c.activeRequests),
	}
}

// RecordError increments the error counter for the given error type, provider, and status code.
func (c *Collector) RecordError(errType, provider string, statusCode int) {
	c.errors.inc(map[string]string{
		"type":        errType,
		"provider":    provider,
		"status_code": strconv.Itoa(statusCode),
	})
}

// ObserveLatency records a request latency observation in seconds.
func (c *Collector) ObserveLatency(provider, model string, streaming bool, seconds float64) {
	streamStr := "false"
	if streaming {
		streamStr = "true"
	}
	c.latency.observe(map[string]string{
		"provider":  provider,
		"model":     model,
		"streaming": streamStr,
	}, seconds)
}

// RecordProviderRequest increments the provider request counter.
// status should be "success", "error", or "circuit_open".
func (c *Collector) RecordProviderRequest(provider, status string) {
	c.providerRequests.inc(map[string]string{
		"provider": provider,
		"status":   status,
	})
}

// SetCircuitState sets the current circuit breaker state gauge for a provider.
// 0=closed, 1=open, 2=half-open.
func (c *Collector) SetCircuitState(provider string, state float64) {
	c.circuitState.set(map[string]string{
		"provider": provider,
	}, state)
}

// ObserveMiddlewareTime records a middleware execution time in seconds.
func (c *Collector) ObserveMiddlewareTime(middleware, phase string, seconds float64) {
	c.middlewareTime.observe(map[string]string{
		"middleware": middleware,
		"phase":     phase,
	}, seconds)
}

// Errors returns the error counter vec for Prometheus export.
func (c *Collector) Errors() *counterVec { return c.errors }

// Latency returns the latency histogram vec for Prometheus export.
func (c *Collector) Latency() *histogramVec { return c.latency }

// ProviderRequests returns the provider request counter vec for Prometheus export.
func (c *Collector) ProviderRequests() *counterVec { return c.providerRequests }

// CircuitState returns the circuit state gauge vec for Prometheus export.
func (c *Collector) CircuitState() *gaugeVec { return c.circuitState }

// MiddlewareTime returns the middleware timing histogram vec for Prometheus export.
func (c *Collector) MiddlewareTime() *histogramVec { return c.middlewareTime }

// addFloat64 atomically adds delta to the float64 stored in addr using a CAS loop.
func addFloat64(addr *uint64, delta float64) {
	for {
		old := atomic.LoadUint64(addr)
		newVal := math.Float64frombits(old) + delta
		if atomic.CompareAndSwapUint64(addr, old, math.Float64bits(newVal)) {
			return
		}
	}
}

// loadFloat64 atomically loads a float64 stored in addr.
func loadFloat64(addr *uint64) float64 {
	return math.Float64frombits(atomic.LoadUint64(addr))
}

// formatDuration produces a human-readable duration string like "2d 5h 32m".
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return d.Round(time.Second).String()
	}

	days := int(d.Hours() / 24)
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60

	if days > 0 {
		return formatWithUnits(days, "d", hours, "h", minutes, "m")
	}
	if hours > 0 {
		return formatWithUnits(hours, "h", minutes, "m", 0, "")
	}
	return formatWithUnits(minutes, "m", 0, "", 0, "")
}

// formatWithUnits builds a compact duration string from up to three components.
func formatWithUnits(v1 int, u1 string, v2 int, u2 string, v3 int, u3 string) string {
	s := ""
	if v1 > 0 {
		s += intStr(v1) + u1
	}
	if v2 > 0 {
		if s != "" {
			s += " "
		}
		s += intStr(v2) + u2
	}
	if v3 > 0 && u3 != "" {
		if s != "" {
			s += " "
		}
		s += intStr(v3) + u3
	}
	if s == "" {
		return "0m"
	}
	return s
}

// intStr converts an int to its string representation without importing strconv.
func intStr(n int) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
		return "-" + intStr(-n)
	}
	digits := make([]byte, 0, 10)
	for n > 0 {
		digits = append(digits, byte('0'+n%10))
		n /= 10
	}
	// reverse
	for i, j := 0, len(digits)-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}
	return string(digits)
}
