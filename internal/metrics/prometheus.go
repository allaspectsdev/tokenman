package metrics

import (
	"fmt"
	"net/http"
	"time"
)

// PrometheusHandler returns an http.HandlerFunc that writes metrics in
// Prometheus text exposition format (version 0.0.4). It does not require the
// Prometheus client library; metrics are formatted manually.
func PrometheusHandler(collector *Collector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stats := collector.Stats()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

		// Uptime in seconds.
		uptimeSeconds := time.Since(collector.startTime).Seconds()

		writeMetric(w, "tokenman_requests_total",
			"Total number of proxied requests.",
			"counter", stats.TotalRequests)

		writeMetric(w, "tokenman_tokens_in_total",
			"Total number of input tokens processed.",
			"counter", stats.TokensIn)

		writeMetric(w, "tokenman_tokens_out_total",
			"Total number of output tokens received.",
			"counter", stats.TokensOut)

		writeMetric(w, "tokenman_tokens_saved_total",
			"Total number of tokens saved by compression and caching.",
			"counter", stats.TokensSaved)

		writeMetricFloat(w, "tokenman_cost_usd_total",
			"Total cost in USD.",
			"counter", stats.CostUSD)

		writeMetricFloat(w, "tokenman_savings_usd_total",
			"Total savings in USD.",
			"counter", stats.SavingsUSD)

		writeMetricFloat(w, "tokenman_savings_percent",
			"Percentage of total spend that was saved.",
			"gauge", stats.SavingsPercent)

		writeMetric(w, "tokenman_cache_hits_total",
			"Total number of cache hits.",
			"counter", stats.CacheHits)

		writeMetric(w, "tokenman_cache_misses_total",
			"Total number of cache misses.",
			"counter", stats.CacheMisses)

		writeMetricFloat(w, "tokenman_cache_hit_rate",
			"Cache hit rate percentage.",
			"gauge", stats.CacheHitRate)

		writeMetric(w, "tokenman_active_requests",
			"Number of requests currently being processed.",
			"gauge", stats.ActiveRequests)

		writeMetricFloat(w, "tokenman_uptime_seconds",
			"Number of seconds since the service started.",
			"gauge", uptimeSeconds)
	}
}

// writeMetric writes a single integer metric in Prometheus text format.
func writeMetric(w http.ResponseWriter, name, help, metricType string, value int64) {
	fmt.Fprintf(w, "# HELP %s %s\n", name, help)
	fmt.Fprintf(w, "# TYPE %s %s\n", name, metricType)
	fmt.Fprintf(w, "%s %d\n", name, value)
}

// writeMetricFloat writes a single float64 metric in Prometheus text format.
func writeMetricFloat(w http.ResponseWriter, name, help, metricType string, value float64) {
	fmt.Fprintf(w, "# HELP %s %s\n", name, help)
	fmt.Fprintf(w, "# TYPE %s %s\n", name, metricType)
	fmt.Fprintf(w, "%s %g\n", name, value)
}
