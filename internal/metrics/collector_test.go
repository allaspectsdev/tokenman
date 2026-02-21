package metrics

import (
	"sync"
	"testing"
	"time"

	"github.com/allaspectsdev/tokenman/internal/pipeline"
)

func TestNewCollector_Defaults(t *testing.T) {
	c := NewCollector()

	stats := c.Stats()
	if stats.TotalRequests != 0 {
		t.Errorf("TotalRequests: got %d, want 0", stats.TotalRequests)
	}
	if stats.CostUSD != 0 {
		t.Errorf("CostUSD: got %f, want 0", stats.CostUSD)
	}
	if stats.ActiveRequests != 0 {
		t.Errorf("ActiveRequests: got %d, want 0", stats.ActiveRequests)
	}
}

func TestCollector_Record(t *testing.T) {
	c := NewCollector()

	req := &pipeline.Request{TokensIn: 100}
	resp := &pipeline.Response{
		TokensOut:   200,
		TokensSaved: 50,
		CostUSD:     0.01,
		SavingsUSD:  0.005,
		CacheHit:    false,
	}

	c.Record(req, resp)

	stats := c.Stats()
	if stats.TotalRequests != 1 {
		t.Errorf("TotalRequests: got %d, want 1", stats.TotalRequests)
	}
	if stats.TokensIn != 100 {
		t.Errorf("TokensIn: got %d, want 100", stats.TokensIn)
	}
	if stats.TokensOut != 200 {
		t.Errorf("TokensOut: got %d, want 200", stats.TokensOut)
	}
	if stats.TokensSaved != 50 {
		t.Errorf("TokensSaved: got %d, want 50", stats.TokensSaved)
	}
	if stats.CacheMisses != 1 {
		t.Errorf("CacheMisses: got %d, want 1", stats.CacheMisses)
	}
}

func TestCollector_CacheHit(t *testing.T) {
	c := NewCollector()

	req := &pipeline.Request{TokensIn: 100}
	resp := &pipeline.Response{CacheHit: true}

	c.Record(req, resp)

	stats := c.Stats()
	if stats.CacheHits != 1 {
		t.Errorf("CacheHits: got %d, want 1", stats.CacheHits)
	}
	if stats.CacheHitRate != 100 {
		t.Errorf("CacheHitRate: got %f, want 100", stats.CacheHitRate)
	}
}

func TestCollector_ActiveRequests(t *testing.T) {
	c := NewCollector()

	c.IncrementActive()
	c.IncrementActive()

	stats := c.Stats()
	if stats.ActiveRequests != 2 {
		t.Errorf("ActiveRequests after 2 increments: got %d, want 2", stats.ActiveRequests)
	}

	c.DecrementActive()

	stats = c.Stats()
	if stats.ActiveRequests != 1 {
		t.Errorf("ActiveRequests after decrement: got %d, want 1", stats.ActiveRequests)
	}
}

func TestCollector_SavingsPercent(t *testing.T) {
	c := NewCollector()

	req := &pipeline.Request{}
	resp := &pipeline.Response{
		CostUSD:    0.75,
		SavingsUSD: 0.25,
	}

	c.Record(req, resp)

	stats := c.Stats()
	if stats.SavingsPercent != 25 {
		t.Errorf("SavingsPercent: got %f, want 25", stats.SavingsPercent)
	}
}

func TestCollector_Uptime(t *testing.T) {
	c := NewCollector()
	// Just check the uptime is a non-empty string.
	stats := c.Stats()
	if stats.Uptime == "" {
		t.Error("Uptime is empty")
	}
}

func TestCollector_ConcurrentRecords(t *testing.T) {
	c := NewCollector()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := &pipeline.Request{TokensIn: 10}
			resp := &pipeline.Response{TokensOut: 20, CostUSD: 0.001}
			c.Record(req, resp)
		}()
	}
	wg.Wait()

	stats := c.Stats()
	if stats.TotalRequests != 100 {
		t.Errorf("TotalRequests after 100 concurrent: got %d, want 100", stats.TotalRequests)
	}
}

func TestCollector_RecordError(t *testing.T) {
	c := NewCollector()

	c.RecordError("parse", "anthropic", 400)
	c.RecordError("parse", "anthropic", 400)
	c.RecordError("upstream", "openai", 502)

	snap := c.Errors().snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected 2 error label combos, got %d", len(snap))
	}

	for _, entry := range snap {
		if entry.labels["type"] == "parse" && entry.labels["provider"] == "anthropic" {
			if entry.value != 2 {
				t.Errorf("parse/anthropic errors: got %d, want 2", entry.value)
			}
		}
	}
}

func TestCollector_ObserveLatency(t *testing.T) {
	c := NewCollector()

	c.ObserveLatency("anthropic", "claude-sonnet-4-20250514", false, 1.5)
	c.ObserveLatency("anthropic", "claude-sonnet-4-20250514", false, 2.5)

	snap := c.Latency().snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected 1 latency series, got %d", len(snap))
	}

	h := snap[0]
	if h.count != 2 {
		t.Errorf("count: got %d, want 2", h.count)
	}
	if h.sum != 4.0 {
		t.Errorf("sum: got %f, want 4.0", h.sum)
	}
}

func TestCollector_RecordProviderRequest(t *testing.T) {
	c := NewCollector()

	c.RecordProviderRequest("anthropic", "success")
	c.RecordProviderRequest("anthropic", "success")
	c.RecordProviderRequest("anthropic", "error")

	snap := c.ProviderRequests().snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected 2 provider request combos, got %d", len(snap))
	}
}

func TestCollector_SetCircuitState(t *testing.T) {
	c := NewCollector()

	c.SetCircuitState("anthropic", 0) // closed
	c.SetCircuitState("anthropic", 1) // open

	snap := c.CircuitState().snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected 1 circuit state entry, got %d", len(snap))
	}
	if snap[0].value != 1 {
		t.Errorf("circuit state: got %f, want 1", snap[0].value)
	}
}

func TestCollector_ObserveMiddlewareTime(t *testing.T) {
	c := NewCollector()

	c.ObserveMiddlewareTime("cache", "request", 0.001)
	c.ObserveMiddlewareTime("cache", "response", 0.002)

	snap := c.MiddlewareTime().snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected 2 middleware time series, got %d", len(snap))
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{5 * time.Minute, "5m"},
		{2*time.Hour + 30*time.Minute, "2h 30m"},
		{25*time.Hour + 15*time.Minute, "1d 1h 15m"},
	}

	for _, tt := range tests {
		got := formatDuration(tt.d)
		if got != tt.want {
			t.Errorf("formatDuration(%v): got %q, want %q", tt.d, got, tt.want)
		}
	}
}
