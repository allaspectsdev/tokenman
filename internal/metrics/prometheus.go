package metrics

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
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

		// --- Labeled metrics ---

		// Error counters.
		writeCounterVec(w, "tokenman_errors_total",
			"Total number of errors by type, provider, and status code.",
			collector.Errors())

		// Latency histograms.
		writeHistogramVec(w, "tokenman_request_duration_seconds",
			"Request duration in seconds by provider, model, and streaming.",
			collector.Latency())

		// Provider request counters.
		writeCounterVec(w, "tokenman_provider_requests_total",
			"Total requests per provider and outcome status.",
			collector.ProviderRequests())

		// Circuit breaker state gauges.
		writeGaugeVec(w, "tokenman_provider_circuit_state",
			"Circuit breaker state per provider (0=closed, 1=open, 2=half-open).",
			collector.CircuitState())

		// Middleware timing histograms.
		writeHistogramVec(w, "tokenman_middleware_duration_seconds",
			"Per-middleware execution time in seconds.",
			collector.MiddlewareTime())
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

// formatLabels formats a label map as Prometheus label string, e.g. {type="foo",provider="bar"}.
func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%s=%q", k, labels[k])
	}
	b.WriteByte('}')
	return b.String()
}

// writeCounterVec writes a labeled counter vec in Prometheus text format.
func writeCounterVec(w http.ResponseWriter, name, help string, cv *counterVec) {
	entries := cv.snapshot()
	if len(entries) == 0 {
		return
	}
	fmt.Fprintf(w, "# HELP %s %s\n", name, help)
	fmt.Fprintf(w, "# TYPE %s counter\n", name)
	for _, e := range entries {
		fmt.Fprintf(w, "%s%s %d\n", name, formatLabels(e.labels), e.value)
	}
}

// writeHistogramVec writes a labeled histogram vec in Prometheus text format.
func writeHistogramVec(w http.ResponseWriter, name, help string, hv *histogramVec) {
	histograms := hv.snapshot()
	if len(histograms) == 0 {
		return
	}
	fmt.Fprintf(w, "# HELP %s %s\n", name, help)
	fmt.Fprintf(w, "# TYPE %s histogram\n", name)
	for _, h := range histograms {
		labels := formatLabels(h.labels)
		// Cumulative bucket counts.
		var cumulative int64
		for i, bound := range h.buckets {
			cumulative += h.counts[i]
			le := fmt.Sprintf("%g", bound)
			if len(h.labels) == 0 {
				fmt.Fprintf(w, "%s_bucket{le=%q} %d\n", name, le, cumulative)
			} else {
				// Insert le into existing labels.
				lbl := formatLabelsWithLe(h.labels, le)
				fmt.Fprintf(w, "%s_bucket%s %d\n", name, lbl, cumulative)
			}
		}
		// +Inf bucket.
		if len(h.labels) == 0 {
			fmt.Fprintf(w, "%s_bucket{le=\"+Inf\"} %d\n", name, h.count)
		} else {
			lbl := formatLabelsWithLe(h.labels, "+Inf")
			fmt.Fprintf(w, "%s_bucket%s %d\n", name, lbl, h.count)
		}
		fmt.Fprintf(w, "%s_sum%s %g\n", name, labels, h.sum)
		fmt.Fprintf(w, "%s_count%s %d\n", name, labels, h.count)
	}
}

// formatLabelsWithLe formats labels with an additional "le" label for histogram buckets.
func formatLabelsWithLe(labels map[string]string, le string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%s=%q", k, labels[k])
	}
	fmt.Fprintf(&b, ",le=%q", le)
	b.WriteByte('}')
	return b.String()
}

// writeGaugeVec writes a labeled gauge vec in Prometheus text format.
func writeGaugeVec(w http.ResponseWriter, name, help string, gv *gaugeVec) {
	entries := gv.snapshot()
	if len(entries) == 0 {
		return
	}
	fmt.Fprintf(w, "# HELP %s %s\n", name, help)
	fmt.Fprintf(w, "# TYPE %s gauge\n", name)
	for _, e := range entries {
		fmt.Fprintf(w, "%s%s %g\n", name, formatLabels(e.labels), e.value)
	}
}
