package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog/log"

	"github.com/allaspects/tokenman/internal/config"
	"github.com/allaspects/tokenman/internal/store"
	"github.com/allaspects/tokenman/web"
)

// DashboardServer serves the web dashboard and JSON API endpoints for
// live metrics, request history, security logs, and configuration.
type DashboardServer struct {
	router    chi.Router
	collector *Collector
	store     *store.Store
	cfg       *config.Config
	addr      string
	server    *http.Server
}

// NewDashboardServer creates a new DashboardServer wired to the given
// collector, store, config, and listen address.
func NewDashboardServer(collector *Collector, st *store.Store, cfg *config.Config, addr string) *DashboardServer {
	d := &DashboardServer{
		collector: collector,
		store:     st,
		cfg:       cfg,
		addr:      addr,
	}

	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.RealIP)
	r.Use(corsMiddleware)

	// API routes.
	r.Get("/api/stats", d.handleStats)
	r.Get("/api/stats/history", d.handleStatsHistory)
	r.Get("/api/requests", d.handleListRequests)
	r.Get("/api/requests/{id}", d.handleGetRequest)
	r.Get("/api/config", d.handleGetConfig)
	r.Post("/api/config", d.handleUpdateConfig)
	r.Get("/api/providers", d.handleProviders)
	r.Get("/api/security/pii", d.handlePIILog)
	r.Get("/api/security/budget", d.handleBudget)
	r.Get("/api/health", d.handleHealth)
	r.Get("/api/projects", d.handleProjects)
	r.Get("/api/plugins", d.handlePlugins)

	// Prometheus metrics endpoint.
	r.Get("/metrics", PrometheusHandler(collector))

	// Static file serving from embedded filesystem.
	staticFS := http.FileServer(http.FS(web.StaticFS()))
	r.Handle("/static/*", http.StripPrefix("/static/", staticFS))

	// Dashboard HTML (catch-all).
	r.Get("/", d.handleDashboard)
	r.Get("/*", d.handleDashboard)

	d.router = r
	return d
}

// Start begins listening on the configured address. It blocks until the
// server is shut down or an error occurs.
func (d *DashboardServer) Start() error {
	d.server = &http.Server{
		Addr:         d.addr,
		Handler:      d.router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Info().Str("addr", d.addr).Msg("dashboard server starting")
	if err := d.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("dashboard server: %w", err)
	}
	return nil
}

// Shutdown gracefully shuts down the dashboard server.
func (d *DashboardServer) Shutdown(ctx context.Context) error {
	if d.server == nil {
		return nil
	}
	return d.server.Shutdown(ctx)
}

// handleHealth returns a simple health check response.
func (d *DashboardServer) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handlePlugins returns the list of registered plugins.
// The plugin registry integration will be done later; for now this
// returns an empty list.
func (d *DashboardServer) handlePlugins(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, []interface{}{})
}

// handleStats returns the current in-memory collector statistics.
func (d *DashboardServer) handleStats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, d.collector.Stats())
}

// handleStatsHistory returns time-series data from the store.
// Accepts ?range=1d, 7d, 30d (default 7d).
func (d *DashboardServer) handleStatsHistory(w http.ResponseWriter, r *http.Request) {
	rangeParam := r.URL.Query().Get("range")
	if rangeParam == "" {
		rangeParam = "7d"
	}

	since, err := parseDurationParam(rangeParam)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid range parameter"})
		return
	}

	sinceTime := time.Now().Add(-since)

	// Query store for daily aggregates.
	type historyPoint struct {
		Timestamp string  `json:"timestamp"`
		Requests  int64   `json:"requests"`
		TokensIn  int64   `json:"tokens_in"`
		TokensOut int64   `json:"tokens_out"`
		Cost      float64 `json:"cost"`
		Savings   float64 `json:"savings"`
	}

	rows, err := d.store.Reader().Query(`
		SELECT
			DATE(timestamp) as day,
			COUNT(*) as requests,
			COALESCE(SUM(tokens_in), 0) as tokens_in,
			COALESCE(SUM(tokens_out), 0) as tokens_out,
			COALESCE(SUM(cost_usd), 0.0) as cost,
			COALESCE(SUM(savings_usd), 0.0) as savings
		FROM requests
		WHERE timestamp >= ?
		GROUP BY DATE(timestamp)
		ORDER BY day ASC`,
		sinceTime.UTC().Format(time.RFC3339),
	)
	if err != nil {
		log.Error().Err(err).Msg("failed to query stats history")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "database error"})
		return
	}
	defer rows.Close()

	var points []historyPoint
	for rows.Next() {
		var p historyPoint
		if err := rows.Scan(&p.Timestamp, &p.Requests, &p.TokensIn, &p.TokensOut, &p.Cost, &p.Savings); err != nil {
			log.Error().Err(err).Msg("failed to scan history row")
			continue
		}
		points = append(points, p)
	}

	if err := rows.Err(); err != nil {
		log.Error().Err(err).Msg("history rows iteration error")
	}

	if points == nil {
		points = []historyPoint{}
	}

	writeJSON(w, http.StatusOK, points)
}

// handleListRequests returns a paginated list of request logs.
func (d *DashboardServer) handleListRequests(w http.ResponseWriter, r *http.Request) {
	page := queryInt(r, "page", 1)
	limit := queryInt(r, "limit", 50)
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 500 {
		limit = 50
	}
	offset := (page - 1) * limit

	requests, err := d.store.ListRequests(limit, offset)
	if err != nil {
		log.Error().Err(err).Msg("failed to list requests")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "database error"})
		return
	}

	type requestEntry struct {
		ID          string  `json:"id"`
		Timestamp   string  `json:"timestamp"`
		Model       string  `json:"model"`
		TokensIn    int64   `json:"tokens_in"`
		TokensOut   int64   `json:"tokens_out"`
		TokensSaved int64   `json:"tokens_saved"`
		CostUSD     float64 `json:"cost_usd"`
		SavingsUSD  float64 `json:"savings_usd"`
		LatencyMs   int64   `json:"latency_ms"`
		StatusCode  int     `json:"status_code"`
		CacheHit    bool    `json:"cache_hit"`
		RequestType string  `json:"request_type"`
		Provider    string  `json:"provider"`
	}

	entries := make([]requestEntry, 0, len(requests))
	for _, req := range requests {
		entries = append(entries, requestEntry{
			ID:          req.ID,
			Timestamp:   req.Timestamp,
			Model:       req.Model,
			TokensIn:    req.TokensIn,
			TokensOut:   req.TokensOut,
			TokensSaved: req.TokensSaved,
			CostUSD:     req.CostUSD,
			SavingsUSD:  req.SavingsUSD,
			LatencyMs:   req.LatencyMs,
			StatusCode:  req.StatusCode,
			CacheHit:    req.CacheHit,
			RequestType: req.RequestType,
			Provider:    req.Provider,
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"page":     page,
		"limit":    limit,
		"requests": entries,
	})
}

// handleGetRequest returns a single request by ID.
func (d *DashboardServer) handleGetRequest(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing request id"})
		return
	}

	req, err := d.store.GetRequest(id)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "request not found"})
			return
		}
		log.Error().Err(err).Str("id", id).Msg("failed to get request")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "database error"})
		return
	}

	// Build a response that includes request and response bodies for debugging.
	type requestDetail struct {
		ID           string  `json:"id"`
		Timestamp    string  `json:"timestamp"`
		Method       string  `json:"method"`
		Path         string  `json:"path"`
		Format       string  `json:"format"`
		Model        string  `json:"model"`
		TokensIn     int64   `json:"tokens_in"`
		TokensOut    int64   `json:"tokens_out"`
		TokensCached int64   `json:"tokens_cached"`
		TokensSaved  int64   `json:"tokens_saved"`
		CostUSD      float64 `json:"cost_usd"`
		SavingsUSD   float64 `json:"savings_usd"`
		LatencyMs    int64   `json:"latency_ms"`
		StatusCode   int     `json:"status_code"`
		CacheHit     bool    `json:"cache_hit"`
		RequestType  string  `json:"request_type"`
		Provider     string  `json:"provider"`
		ErrorMessage string  `json:"error_message"`
		RequestBody  string  `json:"request_body"`
		ResponseBody string  `json:"response_body"`
	}

	detail := requestDetail{
		ID:           req.ID,
		Timestamp:    req.Timestamp,
		Method:       req.Method,
		Path:         req.Path,
		Format:       req.Format,
		Model:        req.Model,
		TokensIn:     req.TokensIn,
		TokensOut:    req.TokensOut,
		TokensCached: req.TokensCached,
		TokensSaved:  req.TokensSaved,
		CostUSD:      req.CostUSD,
		SavingsUSD:   req.SavingsUSD,
		LatencyMs:    req.LatencyMs,
		StatusCode:   req.StatusCode,
		CacheHit:     req.CacheHit,
		RequestType:  req.RequestType,
		Provider:     req.Provider,
		ErrorMessage: req.ErrorMessage,
		RequestBody:  req.RequestBody,
		ResponseBody: req.ResponseBody,
	}

	writeJSON(w, http.StatusOK, detail)
}

// handleGetConfig returns the current configuration with sensitive keys redacted.
func (d *DashboardServer) handleGetConfig(w http.ResponseWriter, _ *http.Request) {
	cfg := config.Get()

	// Serialise to map then redact keys.
	data, err := json.Marshal(cfg)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "serialisation error"})
		return
	}

	var cfgMap map[string]interface{}
	if err := json.Unmarshal(data, &cfgMap); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "serialisation error"})
		return
	}

	redactKeys(cfgMap)
	writeJSON(w, http.StatusOK, cfgMap)
}

// handleUpdateConfig accepts a JSON body and updates the running configuration.
func (d *DashboardServer) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB max
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read body"})
		return
	}
	defer r.Body.Close()

	var updates map[string]interface{}
	if err := json.Unmarshal(body, &updates); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	// For now, log the update request. Full config hot-reload integration
	// would merge updates into the current config and persist to disk.
	log.Info().Interface("updates", updates).Msg("config update requested via API")

	writeJSON(w, http.StatusOK, map[string]string{"status": "accepted", "message": "config update received; restart may be required for some settings"})
}

// handleProviders returns a list of configured providers and their status.
func (d *DashboardServer) handleProviders(w http.ResponseWriter, _ *http.Request) {
	cfg := config.Get()

	type providerInfo struct {
		Name     string   `json:"name"`
		Enabled  bool     `json:"enabled"`
		Models   []string `json:"models"`
		Priority int      `json:"priority"`
		APIBase  string   `json:"api_base"`
	}

	providers := make([]providerInfo, 0, len(cfg.Providers))
	for key, p := range cfg.Providers {
		providers = append(providers, providerInfo{
			Name:     key,
			Enabled:  p.Enabled,
			Models:   p.Models,
			Priority: p.Priority,
			APIBase:  p.APIBase,
		})
	}

	writeJSON(w, http.StatusOK, providers)
}

// handlePIILog returns paginated PII detection logs.
func (d *DashboardServer) handlePIILog(w http.ResponseWriter, r *http.Request) {
	page := queryInt(r, "page", 1)
	limit := queryInt(r, "limit", 50)
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 500 {
		limit = 50
	}
	offset := (page - 1) * limit

	entries, err := d.store.ListPIILog(limit, offset)
	if err != nil {
		log.Error().Err(err).Msg("failed to list PII log")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "database error"})
		return
	}

	type piiEntry struct {
		ID        int64  `json:"id"`
		RequestID string `json:"request_id"`
		Timestamp string `json:"timestamp"`
		PIIType   string `json:"pii_type"`
		Action    string `json:"action"`
		FieldPath string `json:"field_path"`
		Context   string `json:"context"`
	}

	results := make([]piiEntry, 0, len(entries))
	for _, e := range entries {
		results = append(results, piiEntry{
			ID:        e.ID,
			RequestID: e.RequestID,
			Timestamp: e.Timestamp,
			PIIType:   e.PIIType,
			Action:    e.Action,
			FieldPath: e.FieldPath,
			Context:   e.Context,
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"page":    page,
		"limit":   limit,
		"entries": results,
	})
}

// handleBudget returns budget usage versus configured limits for each period.
func (d *DashboardServer) handleBudget(w http.ResponseWriter, _ *http.Request) {
	cfg := config.Get()
	now := time.Now().UTC()

	type budgetEntry struct {
		Period string  `json:"period"`
		Spent  float64 `json:"spent"`
		Limit  float64 `json:"limit"`
		Pct    float64 `json:"pct"`
	}

	periods := []struct {
		name  string
		start string
		limit int
	}{
		{"hourly", now.Truncate(time.Hour).Format(time.RFC3339), cfg.Security.Budget.HourlyLimit},
		{"daily", now.Truncate(24 * time.Hour).Format(time.RFC3339), cfg.Security.Budget.DailyLimit},
		{"monthly", time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339), cfg.Security.Budget.MonthlyLimit},
	}

	budgets := make([]budgetEntry, 0, len(periods))
	for _, p := range periods {
		limitF := float64(p.limit)
		entry := budgetEntry{
			Period: p.name,
			Limit:  limitF,
		}

		b, err := d.store.GetBudget(p.name, p.start)
		if err == nil {
			entry.Spent = b.AmountUSD
			if limitF > 0 {
				entry.Pct = (b.AmountUSD / limitF) * 100
				if entry.Pct > 100 {
					entry.Pct = 100
				}
			}
		}

		budgets = append(budgets, entry)
	}

	writeJSON(w, http.StatusOK, budgets)
}

// handleProjects returns per-project aggregate statistics.
func (d *DashboardServer) handleProjects(w http.ResponseWriter, _ *http.Request) {
	type projectEntry struct {
		Project    string  `json:"project"`
		Requests   int64   `json:"requests"`
		TokensIn   int64   `json:"tokens_in"`
		TokensOut  int64   `json:"tokens_out"`
		CostUSD    float64 `json:"cost_usd"`
		SavingsUSD float64 `json:"savings_usd"`
	}

	rows, err := d.store.Reader().Query(`
		SELECT project, COUNT(*), COALESCE(SUM(tokens_in), 0),
		       COALESCE(SUM(tokens_out), 0), COALESCE(SUM(cost_usd), 0.0),
		       COALESCE(SUM(savings_usd), 0.0)
		FROM requests
		WHERE project != ''
		GROUP BY project
		ORDER BY COUNT(*) DESC`)
	if err != nil {
		log.Error().Err(err).Msg("failed to query projects")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "database error"})
		return
	}
	defer rows.Close()

	var projects []projectEntry
	for rows.Next() {
		var p projectEntry
		if err := rows.Scan(&p.Project, &p.Requests, &p.TokensIn, &p.TokensOut, &p.CostUSD, &p.SavingsUSD); err != nil {
			log.Error().Err(err).Msg("failed to scan project row")
			continue
		}
		projects = append(projects, p)
	}

	if err := rows.Err(); err != nil {
		log.Error().Err(err).Msg("projects rows iteration error")
	}

	if projects == nil {
		projects = []projectEntry{}
	}

	writeJSON(w, http.StatusOK, projects)
}

// handleDashboard serves the embedded HTML dashboard.
func (d *DashboardServer) handleDashboard(w http.ResponseWriter, _ *http.Request) {
	data, err := web.Assets.ReadFile("templates/index.html")
	if err != nil {
		http.Error(w, "dashboard not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

// --- helpers ---

// writeJSON serialises v as JSON and writes it to w with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		log.Error().Err(err).Msg("failed to write JSON response")
	}
}

// queryInt reads an integer query parameter with a default fallback.
func queryInt(r *http.Request, key string, defaultVal int) int {
	s := r.URL.Query().Get(key)
	if s == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return defaultVal
	}
	return n
}

// parseDurationParam converts a shorthand like "7d" or "24h" to a time.Duration.
func parseDurationParam(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "d") {
		numStr := strings.TrimSuffix(s, "d")
		days, err := strconv.Atoi(numStr)
		if err != nil {
			return 0, err
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

// redactKeys recursively walks a map and replaces any string value whose
// key contains "key", "secret", or "token" (case-insensitive) with "****".
func redactKeys(m map[string]interface{}) {
	for k, v := range m {
		lower := strings.ToLower(k)
		if strings.Contains(lower, "key") || strings.Contains(lower, "secret") || strings.Contains(lower, "token") {
			if _, ok := v.(string); ok {
				m[k] = "****"
				continue
			}
		}
		switch child := v.(type) {
		case map[string]interface{}:
			redactKeys(child)
		case []interface{}:
			for _, item := range child {
				if sub, ok := item.(map[string]interface{}); ok {
					redactKeys(sub)
				}
			}
		}
	}
}

// corsMiddleware adds permissive CORS headers for local development.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
