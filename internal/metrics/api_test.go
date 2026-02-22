package metrics

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/allaspectsdev/tokenman/internal/config"
	"github.com/allaspectsdev/tokenman/internal/store"
)

func setupDashboard(t *testing.T) (*DashboardServer, *Collector) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	collector := NewCollector()
	cfg := config.DefaultConfig()
	cfg.Server.DataDir = t.TempDir()

	dash := NewDashboardServer(collector, st, cfg, ":0")
	return dash, collector
}

func TestDashboard_HealthEndpoint(t *testing.T) {
	dash, _ := setupDashboard(t)

	req := httptest.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()
	dash.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusOK)
	}

	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status: got %q, want %q", body["status"], "ok")
	}
}

func TestDashboard_StatsEndpoint(t *testing.T) {
	dash, collector := setupDashboard(t)

	// Record some data.
	collector.IncrementActive()

	req := httptest.NewRequest("GET", "/api/stats", nil)
	w := httptest.NewRecorder()
	dash.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusOK)
	}

	var stats Stats
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if stats.ActiveRequests != 1 {
		t.Errorf("ActiveRequests: got %d, want 1", stats.ActiveRequests)
	}
}

func TestDashboard_ProvidersEndpoint(t *testing.T) {
	dash, _ := setupDashboard(t)

	req := httptest.NewRequest("GET", "/api/providers", nil)
	w := httptest.NewRecorder()
	dash.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusOK)
	}

	var providers []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &providers); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	// Default config has 2 providers.
	if len(providers) < 1 {
		t.Errorf("expected at least 1 provider, got %d", len(providers))
	}
}

func TestDashboard_PluginsEndpoint(t *testing.T) {
	dash, _ := setupDashboard(t)

	req := httptest.NewRequest("GET", "/api/plugins", nil)
	w := httptest.NewRecorder()
	dash.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusOK)
	}
}

func TestDashboard_RequestsEndpoint_Empty(t *testing.T) {
	dash, _ := setupDashboard(t)

	req := httptest.NewRequest("GET", "/api/requests", nil)
	w := httptest.NewRecorder()
	dash.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusOK)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if body["page"] != float64(1) {
		t.Errorf("page: got %v, want 1", body["page"])
	}
}

func TestDashboard_ConfigEndpoint(t *testing.T) {
	dash, _ := setupDashboard(t)

	req := httptest.NewRequest("GET", "/api/config", nil)
	w := httptest.NewRecorder()
	dash.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusOK)
	}

	// Verify sensitive keys are redacted.
	body := w.Body.String()
	if strings.Contains(body, "keyring://") {
		t.Error("config response should redact key_ref values")
	}
}

func TestDashboard_MetricsEndpoint(t *testing.T) {
	dash, collector := setupDashboard(t)

	// Record some metrics.
	collector.RecordError("test", "anthropic", 400)

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	dash.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusOK)
	}

	body := w.Body.String()
	if !strings.Contains(body, "tokenman_") {
		t.Error("metrics endpoint should contain tokenman_ prefix metrics")
	}
}

func TestDashboard_StatsHistoryEndpoint(t *testing.T) {
	dash, _ := setupDashboard(t)

	req := httptest.NewRequest("GET", "/api/stats/history?range=7d", nil)
	w := httptest.NewRecorder()
	dash.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusOK)
	}
}

func TestDashboard_StatsHistoryBadRange(t *testing.T) {
	dash, _ := setupDashboard(t)

	req := httptest.NewRequest("GET", "/api/stats/history?range=abc", nil)
	w := httptest.NewRecorder()
	dash.router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestDashboard_ProjectsEndpoint(t *testing.T) {
	dash, _ := setupDashboard(t)

	req := httptest.NewRequest("GET", "/api/projects", nil)
	w := httptest.NewRecorder()
	dash.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusOK)
	}
}

func TestDashboard_BudgetEndpoint(t *testing.T) {
	dash, _ := setupDashboard(t)

	req := httptest.NewRequest("GET", "/api/security/budget", nil)
	w := httptest.NewRecorder()
	dash.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusOK)
	}
}

func TestDashboard_AuthMiddleware(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	collector := NewCollector()
	cfg := config.DefaultConfig()
	cfg.Server.DataDir = t.TempDir()
	cfg.Auth.Enabled = true
	cfg.Auth.Token = "secret-token"

	dash := NewDashboardServer(collector, st, cfg, ":0")

	// Request without auth should get 401.
	req := httptest.NewRequest("GET", "/api/stats", nil)
	w := httptest.NewRecorder()
	dash.router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("no auth: got %d, want %d", w.Code, http.StatusUnauthorized)
	}

	// Request with correct auth should succeed.
	req = httptest.NewRequest("GET", "/api/stats", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	w = httptest.NewRecorder()
	dash.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("valid auth: got %d, want %d", w.Code, http.StatusOK)
	}

	// Request with wrong token should get 403.
	req = httptest.NewRequest("GET", "/api/stats", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	w = httptest.NewRecorder()
	dash.router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("wrong token: got %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestDashboard_CORS_DefaultOrigins(t *testing.T) {
	dash, _ := setupDashboard(t)

	// Allowed origin (localhost:7678) should be reflected.
	req := httptest.NewRequest("OPTIONS", "/api/health", nil)
	req.Header.Set("Origin", "http://localhost:7678")
	w := httptest.NewRecorder()
	dash.router.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:7678" {
		t.Errorf("CORS allowed origin: got %q, want %q", got, "http://localhost:7678")
	}

	// Unknown origin should be rejected on preflight.
	req = httptest.NewRequest("OPTIONS", "/api/health", nil)
	req.Header.Set("Origin", "https://example.com")
	w = httptest.NewRecorder()
	dash.router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("CORS unknown origin preflight: got %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestParseDurationParam(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"7d", false},
		{"1d", false},
		{"30d", false},
		{"24h", false},
		{"abc", true},
	}

	for _, tt := range tests {
		_, err := parseDurationParam(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseDurationParam(%q): err=%v, wantErr=%v", tt.input, err, tt.wantErr)
		}
	}
}
