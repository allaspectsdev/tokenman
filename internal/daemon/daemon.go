package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/allaspects/tokenman/internal/cache"
	"github.com/allaspects/tokenman/internal/compress"
	"github.com/allaspects/tokenman/internal/config"
	"github.com/allaspects/tokenman/internal/metrics"
	"github.com/allaspects/tokenman/internal/pipeline"
	"github.com/allaspects/tokenman/internal/proxy"
	"github.com/allaspects/tokenman/internal/router"
	"github.com/allaspects/tokenman/internal/security"
	"github.com/allaspects/tokenman/internal/store"
	"github.com/allaspects/tokenman/internal/tokenizer"
	"github.com/allaspects/tokenman/internal/vault"
	"github.com/allaspects/tokenman/internal/version"
)

// Run is the main daemon orchestrator. It initialises all subsystems,
// starts proxy and dashboard servers, and blocks until a shutdown signal
// is received.
func Run(cfg *config.Config, foreground bool) error {
	// 1. Set up zerolog logger.
	dataDir := expandHome(cfg.Server.DataDir)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("creating data directory %s: %w", dataDir, err)
	}

	logLevel := parseLogLevel(cfg.Server.LogLevel)
	zerolog.SetGlobalLevel(logLevel)

	writers := []io.Writer{}

	// Always log to file.
	logPath := filepath.Join(dataDir, "tokenman.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("opening log file %s: %w", logPath, err)
	}
	defer logFile.Close()
	writers = append(writers, logFile)

	// If foreground, also write to stdout with console formatting.
	if foreground {
		consoleWriter := zerolog.ConsoleWriter{
			Out:        os.Stdout,
			TimeFormat: "15:04:05",
		}
		writers = append(writers, consoleWriter)
	}

	multi := zerolog.MultiLevelWriter(writers...)
	log.Logger = zerolog.New(multi).With().Timestamp().Str("service", "tokenman").Logger()

	log.Info().
		Str("version", version.Version).
		Str("data_dir", dataDir).
		Bool("foreground", foreground).
		Msg("tokenman starting")

	// 2. Check if already running.
	if IsRunning(dataDir) {
		return fmt.Errorf("tokenman is already running (PID file exists at %s)", filepath.Join(dataDir, pidFilename))
	}

	// 3. Open store.
	dbPath := filepath.Join(dataDir, "tokenman.db")
	st, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer st.Close()

	log.Info().Str("db_path", dbPath).Msg("store opened")

	// 4. Create metrics collector.
	collector := metrics.NewCollector()

	// 5. Write PID file.
	if err := WritePID(dataDir); err != nil {
		return fmt.Errorf("writing PID file: %w", err)
	}
	defer func() {
		if err := RemovePID(dataDir); err != nil {
			log.Error().Err(err).Msg("failed to remove PID file")
		}
	}()

	log.Info().Int("pid", os.Getpid()).Msg("PID file written")

	// 6. Start config watcher.
	configFile := config.ConfigFilePath()
	if configFile == "" {
		configFile = filepath.Join(dataDir, config.DefaultConfigFilename)
	}

	var watcher *config.Watcher
	if _, statErr := os.Stat(configFile); statErr == nil {
		w, watchErr := config.Watch(configFile)
		if watchErr != nil {
			log.Warn().Err(watchErr).Msg("failed to start config watcher; continuing without hot-reload")
		} else {
			watcher = w
			defer watcher.Close()
			watcher.OnChange(func(old, newCfg *config.Config) {
				log.Info().Msg("configuration reloaded")
				newLevel := parseLogLevel(newCfg.Server.LogLevel)
				zerolog.SetGlobalLevel(newLevel)
			})
			log.Info().Str("file", configFile).Msg("config watcher started")
		}
	}

	// 7. Start periodic data pruning.
	pruneCtx, pruneCancel := context.WithCancel(context.Background())
	defer pruneCancel()
	prunerDone := make(chan struct{})
	go func() {
		defer close(prunerDone)
		runPruner(pruneCtx, st, cfg.Metrics.RetentionDays)
	}()

	// ---------------------------------------------------------------
	// 8. Wire up the V2 proxy stack.
	// ---------------------------------------------------------------

	// 8a. Create store adapters.
	fingerprintAdapter := store.NewFingerprintAdapter(st)
	cacheAdapter := store.NewCacheAdapter(st)
	budgetAdapter := store.NewBudgetAdapter(st)

	// 8b. Init vault and resolve API keys for enabled providers.
	v := vault.New()
	providerConfigs := make(map[string]*router.ProviderConfig)
	providerMap := make(map[string]proxy.ProviderConfig)

	for name, pcfg := range cfg.Providers {
		if !pcfg.Enabled {
			continue
		}
		apiKey := ""
		if pcfg.KeyRef != "" {
			key, err := v.ResolveKeyRef(pcfg.KeyRef)
			if err != nil {
				log.Warn().Err(err).Str("provider", name).Msg("failed to resolve API key; provider will be unavailable")
				continue
			}
			apiKey = key
		}

		format := pipeline.FormatAnthropic
		// Determine format from provider name.
		if strings.Contains(strings.ToLower(name), "openai") {
			format = pipeline.FormatOpenAI
		}

		providerConfigs[name] = &router.ProviderConfig{
			Name:     pcfg.Name,
			BaseURL:  pcfg.APIBase,
			APIKey:   apiKey,
			Format:   format,
			Models:   pcfg.Models,
			Enabled:  true,
			Priority: pcfg.Priority,
			Timeout:  pcfg.TimeoutDuration(),
		}

		// Build provider map for proxy handler -- register each model.
		for _, model := range pcfg.Models {
			providerMap[model] = proxy.ProviderConfig{
				BaseURL: pcfg.APIBase,
				APIKey:  apiKey,
				Format:  format,
			}
		}
	}

	// 8c. Create router.
	rtr := router.NewRouter(providerConfigs, cfg.Routing.ModelMap, cfg.Routing.DefaultProvider, cfg.Routing.FallbackEnabled)
	models := rtr.ListModels()
	log.Info().Int("providers", len(providerConfigs)).Int("models", len(models)).Msg("router initialized")

	// 8d. Build the middleware chain.
	injectionMW := security.NewInjectionMiddleware(cfg.Security.Injection.Action, cfg.Security.Injection.Enabled)
	piiMW := security.NewPIIMiddleware(cfg.Security.PII.Action, cfg.Security.PII.AllowList, cfg.Security.PII.Enabled)

	thresholds := make([]float64, len(cfg.Security.Budget.AlertThresholds))
	for i, t := range cfg.Security.Budget.AlertThresholds {
		thresholds[i] = t / 100.0 // convert from percentage to fraction
	}
	budgetMW := security.NewBudgetMiddleware(budgetAdapter, float64(cfg.Security.Budget.HourlyLimit), float64(cfg.Security.Budget.DailyLimit), float64(cfg.Security.Budget.MonthlyLimit), thresholds, cfg.Security.Budget.Enabled)

	rateLimitMW := security.NewRateLimitMiddleware(cfg.Security.RateLimit.DefaultRate, cfg.Security.RateLimit.DefaultBurst, cfg.Security.RateLimit.ProviderLimits, cfg.Security.RateLimit.Enabled)

	heartbeatMW := compress.NewHeartbeatMiddleware(cfg.Compression.Heartbeat.Enabled, cfg.Compression.Heartbeat.DedupWindowSeconds, cfg.Compression.Heartbeat.HeartbeatModel)
	dedupMW := compress.NewDedupMiddleware(fingerprintAdapter, cfg.Compression.Dedup.TTLSeconds, cfg.Compression.Dedup.Enabled)
	rulesCfg := compress.RulesConfig{
		CollapseWhitespace: cfg.Compression.Rules.CollapseWhitespace,
		MinifyJSON:         cfg.Compression.Rules.MinifyJSON,
		MinifyXML:          cfg.Compression.Rules.MinifyXML,
		DedupInstructions:  cfg.Compression.Rules.DedupInstructions,
		StripMarkdown:      cfg.Compression.Rules.StripMarkdown,
	}
	rulesMW := compress.NewRulesMiddleware(rulesCfg)
	historyMW := compress.NewHistoryMiddleware(cfg.Compression.History.WindowSize, cfg.Compression.History.Enabled)

	cacheMW, err := cache.NewCacheMiddleware(cacheAdapter, cfg.Metrics.CacheTTLSeconds, 1000, true)
	if err != nil {
		return fmt.Errorf("creating cache middleware: %w", err)
	}

	chain := pipeline.NewChain(
		cacheMW,      // check cache first
		injectionMW,  // security: injection detection
		piiMW,        // security: PII detection
		budgetMW,     // security: budget enforcement
		rateLimitMW,  // security: per-provider rate limiting
		heartbeatMW,  // compression: heartbeat dedup
		dedupMW,      // compression: content dedup
		rulesMW,      // compression: text rules
		historyMW,    // compression: history windowing
	)

	// 8e. Create proxy server.
	upstreamClient := proxy.NewUpstreamClient()
	tok := tokenizer.New()

	// Build retry config and circuit breaker registry from resilience settings.
	retryConfig := proxy.RetryConfig{
		MaxAttempts: cfg.Resilience.RetryMaxAttempts,
		BaseDelay:   time.Duration(cfg.Resilience.RetryBaseDelayMs) * time.Millisecond,
		MaxDelay:    time.Duration(cfg.Resilience.RetryMaxDelayMs) * time.Millisecond,
	}

	var cbRegistry *proxy.CircuitBreakerRegistry
	if cfg.Resilience.CBEnabled {
		cbRegistry = proxy.NewCircuitBreakerRegistry(
			cfg.Resilience.CBFailureThreshold,
			time.Duration(cfg.Resilience.CBResetTimeoutSec)*time.Second,
			cfg.Resilience.CBHalfOpenMax,
		)
	}

	streamTimeout := time.Duration(cfg.Server.StreamTimeout) * time.Second

	proxyHandler := proxy.NewProxyHandler(
		chain, upstreamClient, log.Logger, collector, tok, st,
		cfg.Server.MaxBodySize,
		cfg.Server.MaxResponseSize,
		streamTimeout,
		cbRegistry,
		retryConfig,
	)
	proxyHandler.SetProviders(providerMap)

	proxyAddr := fmt.Sprintf(":%d", cfg.Server.ProxyPort)
	readTimeout := time.Duration(cfg.Server.ReadTimeout) * time.Second
	writeTimeout := time.Duration(cfg.Server.WriteTimeout) * time.Second
	idleTimeout := time.Duration(cfg.Server.IdleTimeout) * time.Second
	proxyServer := proxy.NewServer(proxyHandler, proxyAddr, readTimeout, writeTimeout, idleTimeout)

	// Start cache purger (reuses the pruneCtx).
	purgerDone := cacheMW.StartPurger(pruneCtx)

	// Channel to collect server startup errors.
	errCh := make(chan error, 2)

	go func() {
		if cfg.Server.TLSEnabled {
			log.Info().Str("addr", proxyAddr).Msg("proxy server starting (TLS)")
			if err := proxyServer.StartTLS(cfg.Server.CertFile, cfg.Server.KeyFile); err != nil {
				errCh <- fmt.Errorf("proxy server: %w", err)
			}
		} else {
			log.Info().Str("addr", proxyAddr).Msg("proxy server starting")
			if err := proxyServer.Start(); err != nil && err != http.ErrServerClosed {
				errCh <- fmt.Errorf("proxy server: %w", err)
			}
		}
	}()

	// 9. Create and start dashboard server (if enabled).
	var dashServer *metrics.DashboardServer
	if cfg.Dashboard.Enabled {
		dashAddr := fmt.Sprintf(":%d", cfg.Server.DashboardPort)
		dashServer = metrics.NewDashboardServer(collector, st, cfg, dashAddr)

		go func() {
			if cfg.Server.TLSEnabled {
				if err := dashServer.StartTLS(cfg.Server.CertFile, cfg.Server.KeyFile); err != nil {
					errCh <- fmt.Errorf("dashboard server: %w", err)
				}
			} else {
				if err := dashServer.Start(); err != nil {
					errCh <- fmt.Errorf("dashboard server: %w", err)
				}
			}
		}()

		scheme := "http"
		if cfg.Server.TLSEnabled {
			scheme = "https"
		}

		log.Info().
			Int("proxy_port", cfg.Server.ProxyPort).
			Int("dashboard_port", cfg.Server.DashboardPort).
			Bool("tls", cfg.Server.TLSEnabled).
			Msg("tokenman is ready")

		if foreground {
			fmt.Printf("\n  TokenMan is running!\n")
			fmt.Printf("  Proxy:     %s://localhost:%d\n", scheme, cfg.Server.ProxyPort)
			fmt.Printf("  Dashboard: %s://localhost:%d\n\n", scheme, cfg.Server.DashboardPort)
		}
	} else {
		scheme := "http"
		if cfg.Server.TLSEnabled {
			scheme = "https"
		}

		log.Info().
			Int("proxy_port", cfg.Server.ProxyPort).
			Bool("tls", cfg.Server.TLSEnabled).
			Msg("tokenman is ready (dashboard disabled)")

		if foreground {
			fmt.Printf("\n  TokenMan is running!\n")
			fmt.Printf("  Proxy: %s://localhost:%d\n\n", scheme, cfg.Server.ProxyPort)
		}
	}

	// 10. Wait for shutdown signal or fatal error.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		log.Info().Str("signal", sig.String()).Msg("shutdown signal received")
	case err := <-errCh:
		log.Error().Err(err).Msg("fatal server error")
		return err
	}

	// 11. Graceful shutdown with 30-second timeout.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	log.Info().Msg("shutting down servers...")

	if dashServer != nil {
		if err := dashServer.Shutdown(shutdownCtx); err != nil {
			log.Error().Err(err).Msg("dashboard server shutdown error")
		}
	}

	if err := proxyServer.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("proxy server shutdown error")
	}

	// 12. Clean up — wait for background goroutines before closing the store.
	pruneCancel()
	<-purgerDone
	<-prunerDone
	st.Close()
	if err := RemovePID(dataDir); err != nil {
		log.Error().Err(err).Msg("failed to remove PID file during shutdown")
	}

	log.Info().Msg("tokenman stopped")
	return nil
}

// Stop reads the PID file and sends SIGTERM to the running daemon.
func Stop() error {
	dataDir := expandHome(config.Get().Server.DataDir)

	pid, err := ReadPID(dataDir)
	if err != nil {
		return fmt.Errorf("tokenman does not appear to be running: %w", err)
	}

	if !isProcessAlive(pid) {
		// Stale PID file; clean it up.
		if rmErr := RemovePID(dataDir); rmErr != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to remove stale PID file: %v\n", rmErr)
		}
		return fmt.Errorf("tokenman is not running (stale PID file removed)")
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("finding process %d: %w", pid, err)
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("sending SIGTERM to process %d: %w", pid, err)
	}

	fmt.Printf("Sent SIGTERM to tokenman (PID %d)\n", pid)

	// Wait briefly for the process to exit.
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		if !isProcessAlive(pid) {
			return nil
		}
	}

	return nil
}

// Status checks if the daemon is running and prints a summary.
func Status() error {
	cfg := config.Get()
	dataDir := expandHome(cfg.Server.DataDir)

	if !IsRunning(dataDir) {
		fmt.Println("tokenman is not running")
		return nil
	}

	pid, _ := ReadPID(dataDir)
	fmt.Printf("tokenman is running (PID %d)\n", pid)

	// Try to fetch stats from the dashboard API.
	dashURL := fmt.Sprintf("http://localhost:%d/api/stats", cfg.Server.DashboardPort)
	client := &http.Client{Timeout: 3 * time.Second}

	resp, err := client.Get(dashURL)
	if err != nil {
		fmt.Println("  (dashboard unreachable)")
		return nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}

	var stats metrics.Stats
	if err := json.Unmarshal(body, &stats); err != nil {
		return nil
	}

	fmt.Printf("\n  Uptime:         %s\n", stats.Uptime)
	fmt.Printf("  Total Requests: %d\n", stats.TotalRequests)
	fmt.Printf("  Tokens In:      %d\n", stats.TokensIn)
	fmt.Printf("  Tokens Out:     %d\n", stats.TokensOut)
	fmt.Printf("  Tokens Saved:   %d\n", stats.TokensSaved)
	fmt.Printf("  Cost:           $%.4f\n", stats.CostUSD)
	fmt.Printf("  Savings:        $%.4f (%.1f%%)\n", stats.SavingsUSD, stats.SavingsPercent)
	fmt.Printf("  Cache Hit Rate: %.1f%% (%d hits / %d misses)\n", stats.CacheHitRate, stats.CacheHits, stats.CacheMisses)
	fmt.Printf("  Active:         %d\n", stats.ActiveRequests)

	return nil
}

// runPruner periodically prunes old data from the store.
func runPruner(ctx context.Context, st *store.Store, retentionDays int) {
	if retentionDays <= 0 {
		return
	}

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Error().Interface("panic", r).Msg("data pruner: recovered from panic")
					}
				}()
				n, err := st.Prune(retentionDays)
				if err != nil {
					log.Error().Err(err).Msg("data pruning failed")
				} else if n > 0 {
					log.Info().Int64("rows", n).Int("retention_days", retentionDays).Msg("pruned old data")
				}
			}()
		}
	}
}

// parseLogLevel converts a string log level to a zerolog.Level.
func parseLogLevel(level string) zerolog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "trace":
		return zerolog.TraceLevel
	case "debug":
		return zerolog.DebugLevel
	case "info":
		return zerolog.InfoLevel
	case "warn", "warning":
		return zerolog.WarnLevel
	case "error":
		return zerolog.ErrorLevel
	case "fatal":
		return zerolog.FatalLevel
	default:
		return zerolog.InfoLevel
	}
}

// expandHome replaces a leading ~ with the user's home directory.
func expandHome(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}
