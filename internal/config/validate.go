package config

import (
	"fmt"
	"strings"
)

// validate checks the Config for invalid or out-of-range values.
// It returns a combined error if any checks fail.
func validate(cfg *Config) error {
	var errs []string

	// Server validation
	if cfg.Server.ProxyPort < 1 || cfg.Server.ProxyPort > 65535 {
		errs = append(errs, fmt.Sprintf("server.proxy_port must be between 1 and 65535, got %d", cfg.Server.ProxyPort))
	}
	if cfg.Server.DashboardPort < 1 || cfg.Server.DashboardPort > 65535 {
		errs = append(errs, fmt.Sprintf("server.dashboard_port must be between 1 and 65535, got %d", cfg.Server.DashboardPort))
	}
	if cfg.Server.ProxyPort == cfg.Server.DashboardPort {
		errs = append(errs, fmt.Sprintf("server.proxy_port and server.dashboard_port must differ, both are %d", cfg.Server.ProxyPort))
	}
	if !isValidEnum(cfg.Server.LogLevel, ValidLogLevels) {
		errs = append(errs, fmt.Sprintf("server.log_level must be one of %v, got %q", ValidLogLevels, cfg.Server.LogLevel))
	}
	if cfg.Server.DataDir == "" {
		errs = append(errs, "server.data_dir must not be empty")
	}

	// Auth validation
	if cfg.Auth.Enabled && cfg.Auth.Token == "" {
		errs = append(errs, "auth.token must be set when auth.enabled is true")
	}

	// Provider validation
	for name, p := range cfg.Providers {
		if p.APIBase == "" {
			errs = append(errs, fmt.Sprintf("providers.%s.api_base must not be empty", name))
		}
		if p.Priority < 0 {
			errs = append(errs, fmt.Sprintf("providers.%s.priority must be non-negative, got %d", name, p.Priority))
		}
		if p.Timeout < 0 {
			errs = append(errs, fmt.Sprintf("providers.%s.timeout must be non-negative", name))
		}
	}

	// Routing validation
	if cfg.Routing.DefaultProvider != "" {
		if _, ok := cfg.Providers[cfg.Routing.DefaultProvider]; !ok {
			errs = append(errs, fmt.Sprintf("routing.default_provider %q is not a configured provider", cfg.Routing.DefaultProvider))
		}
	}
	for model, provider := range cfg.Routing.ModelMap {
		if _, ok := cfg.Providers[provider]; !ok {
			errs = append(errs, fmt.Sprintf("routing.model_map[%q] references unknown provider %q", model, provider))
		}
	}

	// Compression validation
	if cfg.Compression.Dedup.TTLSeconds < 0 {
		errs = append(errs, fmt.Sprintf("compression.dedup.ttl_seconds must be non-negative, got %d", cfg.Compression.Dedup.TTLSeconds))
	}
	if cfg.Compression.Heartbeat.DedupWindowSeconds < 0 {
		errs = append(errs, fmt.Sprintf("compression.heartbeat.dedup_window_seconds must be non-negative, got %d", cfg.Compression.Heartbeat.DedupWindowSeconds))
	}
	if cfg.Compression.History.WindowSize < 0 {
		errs = append(errs, fmt.Sprintf("compression.history.window_size must be non-negative, got %d", cfg.Compression.History.WindowSize))
	}

	// Security validation
	if !isValidEnum(cfg.Security.PII.Action, ValidPIIActions) {
		errs = append(errs, fmt.Sprintf("security.pii.action must be one of %v, got %q", ValidPIIActions, cfg.Security.PII.Action))
	}
	if !isValidEnum(cfg.Security.Injection.Action, ValidInjectionActions) {
		errs = append(errs, fmt.Sprintf("security.injection.action must be one of %v, got %q", ValidInjectionActions, cfg.Security.Injection.Action))
	}
	if cfg.Security.Budget.HourlyLimit < 0 {
		errs = append(errs, fmt.Sprintf("security.budget.hourly_limit must be non-negative, got %d", cfg.Security.Budget.HourlyLimit))
	}
	if cfg.Security.Budget.DailyLimit < 0 {
		errs = append(errs, fmt.Sprintf("security.budget.daily_limit must be non-negative, got %d", cfg.Security.Budget.DailyLimit))
	}
	if cfg.Security.Budget.MonthlyLimit < 0 {
		errs = append(errs, fmt.Sprintf("security.budget.monthly_limit must be non-negative, got %d", cfg.Security.Budget.MonthlyLimit))
	}
	for i, threshold := range cfg.Security.Budget.AlertThresholds {
		if threshold < 0 || threshold > 100 {
			errs = append(errs, fmt.Sprintf("security.budget.alert_thresholds[%d] must be between 0 and 100, got %.1f", i, threshold))
		}
	}

	// Metrics validation
	if cfg.Metrics.RetentionDays < 1 {
		errs = append(errs, fmt.Sprintf("metrics.retention_days must be at least 1, got %d", cfg.Metrics.RetentionDays))
	}
	if cfg.Metrics.CacheTTLSeconds < 0 {
		errs = append(errs, fmt.Sprintf("metrics.cache_ttl_seconds must be non-negative, got %d", cfg.Metrics.CacheTTLSeconds))
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

// isValidEnum returns true if val is in the allowed list (case-insensitive).
func isValidEnum(val string, allowed []string) bool {
	lower := strings.ToLower(val)
	for _, a := range allowed {
		if strings.ToLower(a) == lower {
			return true
		}
	}
	return false
}
