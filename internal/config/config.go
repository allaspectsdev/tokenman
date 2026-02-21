package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/viper"
)

// configPtr holds the current config for thread-safe access.
var configPtr atomic.Pointer[Config]

// loadedConfigFile stores the path of the config file used by the last successful Load.
var loadedConfigFile atomic.Value

// Get returns the current Config. It is safe for concurrent use.
// If no config has been loaded yet, it returns the default config.
func Get() *Config {
	if c := configPtr.Load(); c != nil {
		return c
	}
	d := DefaultConfig()
	configPtr.Store(d)
	return d
}

// set stores a new Config atomically.
func set(cfg *Config) {
	configPtr.Store(cfg)
}

// Config is the top-level configuration for TokenMan.
type Config struct {
	Server      ServerConfig              `mapstructure:"server"      toml:"server"`
	Auth        AuthConfig                `mapstructure:"auth"        toml:"auth"`
	Providers   map[string]ProviderConfig `mapstructure:"providers"   toml:"providers"`
	Routing     RoutingConfig             `mapstructure:"routing"     toml:"routing"`
	Compression CompressionConfig         `mapstructure:"compression" toml:"compression"`
	Security    SecurityConfig            `mapstructure:"security"    toml:"security"`
	Resilience  ResilienceConfig          `mapstructure:"resilience"  toml:"resilience"`
	Tracing     TracingConfig             `mapstructure:"tracing"     toml:"tracing"`
	Dashboard   DashboardConfig           `mapstructure:"dashboard"   toml:"dashboard"`
	Metrics     MetricsConfig             `mapstructure:"metrics"     toml:"metrics"`
	Plugins     PluginConfig              `mapstructure:"plugins"     toml:"plugins"`
}

// ServerConfig holds the core server settings.
type ServerConfig struct {
	ProxyPort       int    `mapstructure:"proxy_port"        toml:"proxy_port"`
	DashboardPort   int    `mapstructure:"dashboard_port"    toml:"dashboard_port"`
	LogLevel        string `mapstructure:"log_level"          toml:"log_level"`
	DataDir         string `mapstructure:"data_dir"           toml:"data_dir"`
	TLSEnabled      bool   `mapstructure:"tls_enabled"       toml:"tls_enabled"`
	CertFile        string `mapstructure:"cert_file"          toml:"cert_file"`
	KeyFile         string `mapstructure:"key_file"           toml:"key_file"`
	ReadTimeout     int    `mapstructure:"read_timeout"      toml:"read_timeout"`
	WriteTimeout    int    `mapstructure:"write_timeout"     toml:"write_timeout"`
	IdleTimeout     int    `mapstructure:"idle_timeout"      toml:"idle_timeout"`
	MaxBodySize     int64  `mapstructure:"max_body_size"     toml:"max_body_size"`
	MaxResponseSize int64  `mapstructure:"max_response_size" toml:"max_response_size"`
	StreamTimeout   int    `mapstructure:"stream_timeout"    toml:"stream_timeout"`
}

// AuthConfig holds the dashboard authentication settings.
type AuthConfig struct {
	Enabled bool   `mapstructure:"enabled" toml:"enabled"`
	Token   string `mapstructure:"token"   toml:"token"`
}

// ProviderConfig describes a single LLM provider.
type ProviderConfig struct {
	Name     string `mapstructure:"name"     toml:"name"`
	APIBase  string `mapstructure:"api_base" toml:"api_base"`
	KeyRef   string `mapstructure:"key_ref"  toml:"key_ref"`
	Models   []string `mapstructure:"models"   toml:"models"`
	Enabled  bool   `mapstructure:"enabled"  toml:"enabled"`
	Priority int    `mapstructure:"priority" toml:"priority"`
	Timeout  int    `mapstructure:"timeout"  toml:"timeout"` // seconds
}

// TimeoutDuration returns the provider timeout as a time.Duration.
func (p ProviderConfig) TimeoutDuration() time.Duration {
	if p.Timeout <= 0 {
		return 30 * time.Second
	}
	return time.Duration(p.Timeout) * time.Second
}

// RoutingConfig controls how requests are dispatched to providers.
type RoutingConfig struct {
	DefaultProvider string            `mapstructure:"default_provider" toml:"default_provider"`
	ModelMap        map[string]string `mapstructure:"model_map"        toml:"model_map"`
	HeartbeatModel  string            `mapstructure:"heartbeat_model"  toml:"heartbeat_model"`
	FallbackEnabled bool              `mapstructure:"fallback_enabled" toml:"fallback_enabled"`
}

// CompressionConfig groups the token-compression sub-sections.
type CompressionConfig struct {
	Dedup          DedupConfig          `mapstructure:"dedup"          toml:"dedup"`
	Rules          RulesConfig          `mapstructure:"rules"          toml:"rules"`
	Heartbeat      HeartbeatConfig      `mapstructure:"heartbeat"      toml:"heartbeat"`
	History        HistoryConfig        `mapstructure:"history"        toml:"history"`
	Summarization  SummarizationConfig  `mapstructure:"summarization"  toml:"summarization"`
}

// DedupConfig controls the deduplication cache.
type DedupConfig struct {
	Enabled    bool `mapstructure:"enabled"     toml:"enabled"`
	TTLSeconds int  `mapstructure:"ttl_seconds" toml:"ttl_seconds"`
}

// RulesConfig controls per-request compression rules.
type RulesConfig struct {
	CollapseWhitespace bool `mapstructure:"collapse_whitespace" toml:"collapse_whitespace"`
	MinifyJSON         bool `mapstructure:"minify_json"          toml:"minify_json"`
	MinifyXML          bool `mapstructure:"minify_xml"            toml:"minify_xml"`
	DedupInstructions  bool `mapstructure:"dedup_instructions"   toml:"dedup_instructions"`
	StripMarkdown      bool `mapstructure:"strip_markdown"        toml:"strip_markdown"`
}

// HeartbeatConfig controls heartbeat deduplication.
type HeartbeatConfig struct {
	Enabled            bool   `mapstructure:"enabled"               toml:"enabled"`
	DedupWindowSeconds int    `mapstructure:"dedup_window_seconds"  toml:"dedup_window_seconds"`
	HeartbeatModel     string `mapstructure:"heartbeat_model"       toml:"heartbeat_model"`
}

// HistoryConfig controls conversation history compression.
type HistoryConfig struct {
	Enabled    bool `mapstructure:"enabled"     toml:"enabled"`
	WindowSize int  `mapstructure:"window_size" toml:"window_size"`
}

// SummarizationConfig controls LLM-based conversation summarization.
type SummarizationConfig struct {
	Enabled          bool   `mapstructure:"enabled"            toml:"enabled"`
	MaxMessages      int    `mapstructure:"max_messages"       toml:"max_messages"`
	SummaryModel     string `mapstructure:"summary_model"      toml:"summary_model"`
	SummaryMaxTokens int    `mapstructure:"summary_max_tokens" toml:"summary_max_tokens"`
}

// PluginConfig controls the plugin system.
type PluginConfig struct {
	Enabled bool                              `mapstructure:"enabled" toml:"enabled"`
	Dir     string                            `mapstructure:"dir"     toml:"dir"`
	Configs map[string]map[string]interface{} `mapstructure:"configs" toml:"configs"`
}

// SecurityConfig groups the security sub-sections.
type SecurityConfig struct {
	PII       PIIConfig       `mapstructure:"pii"        toml:"pii"`
	Injection InjectionConfig `mapstructure:"injection"  toml:"injection"`
	Budget    BudgetConfig    `mapstructure:"budget"     toml:"budget"`
	RateLimit RateLimitConfig `mapstructure:"rate_limit" toml:"rate_limit"`
}

// RateLimitConfig controls per-provider rate limiting.
type RateLimitConfig struct {
	Enabled        bool                        `mapstructure:"enabled"         toml:"enabled"`
	DefaultRate    float64                     `mapstructure:"default_rate"    toml:"default_rate"`    // requests per second
	DefaultBurst   int                         `mapstructure:"default_burst"   toml:"default_burst"`
	ProviderLimits map[string]ProviderRateLimit `mapstructure:"provider_limits" toml:"provider_limits"`
}

// ProviderRateLimit defines rate limit settings for a specific provider.
type ProviderRateLimit struct {
	Rate  float64 `mapstructure:"rate"  toml:"rate"`
	Burst int     `mapstructure:"burst" toml:"burst"`
}

// PIIConfig controls PII detection and remediation.
type PIIConfig struct {
	Enabled   bool     `mapstructure:"enabled"    toml:"enabled"`
	Action    string   `mapstructure:"action"     toml:"action"`
	AllowList []string `mapstructure:"allow_list" toml:"allow_list"`
}

// InjectionConfig controls prompt-injection detection.
type InjectionConfig struct {
	Enabled bool   `mapstructure:"enabled" toml:"enabled"`
	Action  string `mapstructure:"action"  toml:"action"`
}

// BudgetConfig controls spend budgets and alerts.
type BudgetConfig struct {
	Enabled         bool      `mapstructure:"enabled"          toml:"enabled"`
	HourlyLimit     int       `mapstructure:"hourly_limit"     toml:"hourly_limit"`
	DailyLimit      int       `mapstructure:"daily_limit"      toml:"daily_limit"`
	MonthlyLimit    int       `mapstructure:"monthly_limit"    toml:"monthly_limit"`
	AlertThresholds []float64 `mapstructure:"alert_thresholds" toml:"alert_thresholds"`
}

// TracingConfig controls OpenTelemetry distributed tracing.
type TracingConfig struct {
	Enabled     bool    `mapstructure:"enabled"      toml:"enabled"`
	Exporter    string  `mapstructure:"exporter"     toml:"exporter"`     // "stdout", "otlp-grpc", "otlp-http"
	Endpoint    string  `mapstructure:"endpoint"     toml:"endpoint"`     // e.g. "localhost:4317"
	ServiceName string  `mapstructure:"service_name" toml:"service_name"` // defaults to "tokenman"
	SampleRate  float64 `mapstructure:"sample_rate"  toml:"sample_rate"`  // 0.0 to 1.0
	Insecure    bool    `mapstructure:"insecure"     toml:"insecure"`     // skip TLS for dev
}

// DashboardConfig controls the web dashboard.
type DashboardConfig struct {
	Enabled        bool     `mapstructure:"enabled"         toml:"enabled"`
	AutoOpen       bool     `mapstructure:"auto_open"       toml:"auto_open"`
	AllowedOrigins []string `mapstructure:"allowed_origins" toml:"allowed_origins"`
}

// MetricsConfig controls metrics storage and caching.
type MetricsConfig struct {
	RetentionDays   int `mapstructure:"retention_days"    toml:"retention_days"`
	CacheTTLSeconds int `mapstructure:"cache_ttl_seconds" toml:"cache_ttl_seconds"`
}

// ResilienceConfig controls retry, circuit breaker, and related resilience settings.
type ResilienceConfig struct {
	RetryMaxAttempts   int  `mapstructure:"retry_max_attempts"       toml:"retry_max_attempts"`
	RetryBaseDelayMs   int  `mapstructure:"retry_base_delay_ms"      toml:"retry_base_delay_ms"`
	RetryMaxDelayMs    int  `mapstructure:"retry_max_delay_ms"       toml:"retry_max_delay_ms"`
	CBEnabled          bool `mapstructure:"circuit_breaker_enabled"  toml:"circuit_breaker_enabled"`
	CBFailureThreshold int  `mapstructure:"cb_failure_threshold"     toml:"cb_failure_threshold"`
	CBResetTimeoutSec  int  `mapstructure:"cb_reset_timeout_seconds" toml:"cb_reset_timeout_seconds"`
	CBHalfOpenMax      int  `mapstructure:"cb_half_open_max_calls"   toml:"cb_half_open_max_calls"`
}

// Load reads configuration from disk with the following precedence:
//  1. Environment variables (TOKENMAN_ prefix, _ as separator)
//  2. The file at explicitPath if non-empty
//  3. ~/.tokenman/tokenman.toml
//  4. ./tokenman.toml
//  5. Built-in defaults
//
// The loaded config is validated and stored in the global atomic pointer.
func Load(explicitPath string) (*Config, error) {
	v := viper.New()
	v.SetConfigType("toml")

	// Set all defaults from the default config so viper knows every key.
	setViperDefaults(v)

	// Environment variable overlay: TOKENMAN_SERVER_PROXY_PORT etc.
	v.SetEnvPrefix("TOKENMAN")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Determine which file(s) to read.
	if explicitPath != "" {
		v.SetConfigFile(explicitPath)
	} else {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			v.AddConfigPath(filepath.Join(homeDir, ".tokenman"))
		}
		v.AddConfigPath(".")
		v.SetConfigName("tokenman")
	}

	if err := v.ReadInConfig(); err != nil {
		// If no config file exists we still proceed with defaults + env.
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("reading config: %w", err)
		}
	}

	// Store the resolved config file path.
	if cf := v.ConfigFileUsed(); cf != "" {
		loadedConfigFile.Store(cf)
	}

	cfg := DefaultConfig()
	if err := v.Unmarshal(cfg, viper.DecodeHook(
		mapstructure.ComposeDecodeHookFunc(
			mapstructure.StringToTimeDurationHookFunc(),
			mapstructure.StringToSliceHookFunc(","),
		),
	)); err != nil {
		return nil, fmt.Errorf("unmarshalling config: %w", err)
	}

	// Expand ~ in data_dir.
	cfg.Server.DataDir = expandHome(cfg.Server.DataDir)

	if err := validate(cfg); err != nil {
		return nil, err
	}

	set(cfg)
	return cfg, nil
}

// InitConfig writes the default configuration file to ~/.tokenman/tokenman.toml.
// If the file already exists it is not overwritten.
func InitConfig() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("determining home directory: %w", err)
	}

	dir := filepath.Join(homeDir, ".tokenman")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating data directory: %w", err)
	}

	path := filepath.Join(dir, DefaultConfigFilename)
	if _, err := os.Stat(path); err == nil {
		fmt.Printf("Config already exists: %s\n", path)
		return nil
	}

	cfg := DefaultConfig()
	data, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshalling default config: %w", err)
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	fmt.Printf("Config written to %s\n", path)
	return nil
}

// ExportConfig writes the current config to the given path in TOML format.
func ExportConfig(path string) error {
	cfg := Get()
	data, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshalling config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
}

// ImportConfig reads a TOML config file and merges it into the current config.
// The imported config is also persisted to the active config file so changes
// survive restarts.
func ImportConfig(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading config: %w", err)
	}
	cfg := DefaultConfig()
	if err := toml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("parsing config: %w", err)
	}
	if err := validate(cfg); err != nil {
		return err
	}
	set(cfg)

	// Persist to the active config file so changes survive restart.
	if dest := ConfigFilePath(); dest != "" {
		out, err := toml.Marshal(cfg)
		if err != nil {
			return fmt.Errorf("marshalling config for persistence: %w", err)
		}
		if err := os.WriteFile(dest, out, 0o600); err != nil {
			return fmt.Errorf("persisting imported config: %w", err)
		}
	}

	return nil
}

// ConfigFilePath returns the path of the config file that was loaded, or
// empty if no file was found.
func ConfigFilePath() string {
	if v, ok := loadedConfigFile.Load().(string); ok {
		return v
	}
	return ""
}

// setViperDefaults registers every known key with viper so that env var binding
// works for all fields even when no config file is present.
func setViperDefaults(v *viper.Viper) {
	d := DefaultConfig()

	// Server
	v.SetDefault("server.proxy_port", d.Server.ProxyPort)
	v.SetDefault("server.dashboard_port", d.Server.DashboardPort)
	v.SetDefault("server.log_level", d.Server.LogLevel)
	v.SetDefault("server.data_dir", d.Server.DataDir)

	v.SetDefault("server.tls_enabled", d.Server.TLSEnabled)
	v.SetDefault("server.cert_file", d.Server.CertFile)
	v.SetDefault("server.key_file", d.Server.KeyFile)
	v.SetDefault("server.read_timeout", d.Server.ReadTimeout)
	v.SetDefault("server.write_timeout", d.Server.WriteTimeout)
	v.SetDefault("server.idle_timeout", d.Server.IdleTimeout)
	v.SetDefault("server.max_body_size", d.Server.MaxBodySize)

	// Auth
	v.SetDefault("auth.enabled", d.Auth.Enabled)
	v.SetDefault("auth.token", d.Auth.Token)

	// Routing
	v.SetDefault("routing.default_provider", d.Routing.DefaultProvider)
	v.SetDefault("routing.heartbeat_model", d.Routing.HeartbeatModel)
	v.SetDefault("routing.fallback_enabled", d.Routing.FallbackEnabled)

	// Compression.Dedup
	v.SetDefault("compression.dedup.enabled", d.Compression.Dedup.Enabled)
	v.SetDefault("compression.dedup.ttl_seconds", d.Compression.Dedup.TTLSeconds)

	// Compression.Rules
	v.SetDefault("compression.rules.collapse_whitespace", d.Compression.Rules.CollapseWhitespace)
	v.SetDefault("compression.rules.minify_json", d.Compression.Rules.MinifyJSON)
	v.SetDefault("compression.rules.minify_xml", d.Compression.Rules.MinifyXML)
	v.SetDefault("compression.rules.dedup_instructions", d.Compression.Rules.DedupInstructions)
	v.SetDefault("compression.rules.strip_markdown", d.Compression.Rules.StripMarkdown)

	// Compression.Heartbeat
	v.SetDefault("compression.heartbeat.enabled", d.Compression.Heartbeat.Enabled)
	v.SetDefault("compression.heartbeat.dedup_window_seconds", d.Compression.Heartbeat.DedupWindowSeconds)
	v.SetDefault("compression.heartbeat.heartbeat_model", d.Compression.Heartbeat.HeartbeatModel)

	// Compression.History
	v.SetDefault("compression.history.enabled", d.Compression.History.Enabled)
	v.SetDefault("compression.history.window_size", d.Compression.History.WindowSize)

	// Compression.Summarization
	v.SetDefault("compression.summarization.enabled", d.Compression.Summarization.Enabled)
	v.SetDefault("compression.summarization.max_messages", d.Compression.Summarization.MaxMessages)
	v.SetDefault("compression.summarization.summary_model", d.Compression.Summarization.SummaryModel)
	v.SetDefault("compression.summarization.summary_max_tokens", d.Compression.Summarization.SummaryMaxTokens)

	// Security.PII
	v.SetDefault("security.pii.enabled", d.Security.PII.Enabled)
	v.SetDefault("security.pii.action", d.Security.PII.Action)
	v.SetDefault("security.pii.allow_list", d.Security.PII.AllowList)

	// Security.Injection
	v.SetDefault("security.injection.enabled", d.Security.Injection.Enabled)
	v.SetDefault("security.injection.action", d.Security.Injection.Action)

	// Security.Budget
	v.SetDefault("security.budget.enabled", d.Security.Budget.Enabled)
	v.SetDefault("security.budget.hourly_limit", d.Security.Budget.HourlyLimit)
	v.SetDefault("security.budget.daily_limit", d.Security.Budget.DailyLimit)
	v.SetDefault("security.budget.monthly_limit", d.Security.Budget.MonthlyLimit)
	v.SetDefault("security.budget.alert_thresholds", d.Security.Budget.AlertThresholds)

	// Security.RateLimit
	v.SetDefault("security.rate_limit.enabled", d.Security.RateLimit.Enabled)
	v.SetDefault("security.rate_limit.default_rate", d.Security.RateLimit.DefaultRate)
	v.SetDefault("security.rate_limit.default_burst", d.Security.RateLimit.DefaultBurst)

	// Dashboard
	v.SetDefault("dashboard.enabled", d.Dashboard.Enabled)
	v.SetDefault("dashboard.auto_open", d.Dashboard.AutoOpen)
	v.SetDefault("dashboard.allowed_origins", d.Dashboard.AllowedOrigins)

	// Metrics
	v.SetDefault("metrics.retention_days", d.Metrics.RetentionDays)
	v.SetDefault("metrics.cache_ttl_seconds", d.Metrics.CacheTTLSeconds)

	// Resilience
	v.SetDefault("resilience.retry_max_attempts", d.Resilience.RetryMaxAttempts)
	v.SetDefault("resilience.retry_base_delay_ms", d.Resilience.RetryBaseDelayMs)
	v.SetDefault("resilience.retry_max_delay_ms", d.Resilience.RetryMaxDelayMs)
	v.SetDefault("resilience.circuit_breaker_enabled", d.Resilience.CBEnabled)
	v.SetDefault("resilience.cb_failure_threshold", d.Resilience.CBFailureThreshold)
	v.SetDefault("resilience.cb_reset_timeout_seconds", d.Resilience.CBResetTimeoutSec)
	v.SetDefault("resilience.cb_half_open_max_calls", d.Resilience.CBHalfOpenMax)

	// Server (new resilience-related fields)
	v.SetDefault("server.max_response_size", d.Server.MaxResponseSize)
	v.SetDefault("server.stream_timeout", d.Server.StreamTimeout)

	// Tracing
	v.SetDefault("tracing.enabled", d.Tracing.Enabled)
	v.SetDefault("tracing.exporter", d.Tracing.Exporter)
	v.SetDefault("tracing.endpoint", d.Tracing.Endpoint)
	v.SetDefault("tracing.service_name", d.Tracing.ServiceName)
	v.SetDefault("tracing.sample_rate", d.Tracing.SampleRate)
	v.SetDefault("tracing.insecure", d.Tracing.Insecure)

	// Plugins
	v.SetDefault("plugins.enabled", d.Plugins.Enabled)
	v.SetDefault("plugins.dir", d.Plugins.Dir)
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
