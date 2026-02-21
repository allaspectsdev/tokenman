package metrics

import (
	"math"
	"sync/atomic"
	"time"

	"github.com/allaspects/tokenman/internal/pipeline"
)

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

// NewCollector creates a new Collector with all counters initialised to zero
// and the start time set to now.
func NewCollector() *Collector {
	return &Collector{
		startTime:       time.Now(),
		totalCostUSD:    math.Float64bits(0),
		totalSavingsUSD: math.Float64bits(0),
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
