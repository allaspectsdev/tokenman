package config

// DefaultProxyPort is the default port for the proxy server.
const DefaultProxyPort = 7677

// DefaultDashboardPort is the default port for the dashboard server.
const DefaultDashboardPort = 7678

// DefaultLogLevel is the default log level.
const DefaultLogLevel = "info"

// DefaultDataDir is the default data directory (before tilde expansion).
const DefaultDataDir = "~/.tokenman"

// DefaultConfigFilename is the name of the config file.
const DefaultConfigFilename = "tokenman.toml"

// DefaultDedupTTL is the default dedup cache TTL.
const DefaultDedupTTL = 300

// DefaultHeartbeatDedupWindow is the default heartbeat dedup window in seconds.
const DefaultHeartbeatDedupWindow = 30

// DefaultHistoryWindowSize is the default history window size.
const DefaultHistoryWindowSize = 10

// DefaultRetentionDays is the default metrics retention in days.
const DefaultRetentionDays = 30

// DefaultCacheTTL is the default metrics cache TTL in seconds.
const DefaultCacheTTL = 300

// DefaultProviderTimeout is the default provider timeout in seconds.
const DefaultProviderTimeout = 30

// DefaultBudgetAlertThresholds are the default alert thresholds (percentages).
var DefaultBudgetAlertThresholds = []float64{50, 75, 90}

// ValidLogLevels lists the allowed log level values.
var ValidLogLevels = []string{"trace", "debug", "info", "warn", "error", "fatal"}

// ValidPIIActions lists the allowed PII action values.
var ValidPIIActions = []string{"redact", "hash", "log", "block"}

// ValidInjectionActions lists the allowed injection detection action values.
var ValidInjectionActions = []string{"log", "block", "sanitize", "warn"}

// DefaultConfig returns a Config populated with all default values.
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			ProxyPort:     DefaultProxyPort,
			DashboardPort: DefaultDashboardPort,
			LogLevel:      DefaultLogLevel,
			DataDir:       DefaultDataDir,
		},
		Auth: AuthConfig{
			Enabled: false,
			Token:   "",
		},
		Providers: map[string]ProviderConfig{
			"anthropic": {
				Name:     "Anthropic",
				APIBase:  "https://api.anthropic.com",
				KeyRef:   "keyring://tokenman/anthropic",
				Models:   []string{"claude-sonnet-4-20250514", "claude-haiku-4-20250414"},
				Enabled:  true,
				Priority: 1,
				Timeout:  DefaultProviderTimeout,
			},
			"openai": {
				Name:     "OpenAI",
				APIBase:  "https://api.openai.com",
				KeyRef:   "keyring://tokenman/openai",
				Models:   []string{"gpt-4o", "gpt-4o-mini"},
				Enabled:  true,
				Priority: 2,
				Timeout:  DefaultProviderTimeout,
			},
		},
		Routing: RoutingConfig{
			DefaultProvider: "anthropic",
			ModelMap:        map[string]string{},
			HeartbeatModel:  "",
			FallbackEnabled: true,
		},
		Compression: CompressionConfig{
			Dedup: DedupConfig{
				Enabled:    true,
				TTLSeconds: DefaultDedupTTL,
			},
			Rules: RulesConfig{
				CollapseWhitespace: true,
				MinifyJSON:         true,
				MinifyXML:          true,
				DedupInstructions:  true,
				StripMarkdown:      false,
			},
			Heartbeat: HeartbeatConfig{
				Enabled:            true,
				DedupWindowSeconds: DefaultHeartbeatDedupWindow,
				HeartbeatModel:     "",
			},
			History: HistoryConfig{
				Enabled:    true,
				WindowSize: DefaultHistoryWindowSize,
			},
			Summarization: SummarizationConfig{
				Enabled:          false,
				MaxMessages:      50,
				SummaryModel:     "claude-haiku-4-20250414",
				SummaryMaxTokens: 1024,
			},
		},
		Security: SecurityConfig{
			PII: PIIConfig{
				Enabled:   true,
				Action:    "redact",
				AllowList: []string{},
			},
			Injection: InjectionConfig{
				Enabled: true,
				Action:  "log",
			},
			Budget: BudgetConfig{
				Enabled:         false,
				HourlyLimit:     0,
				DailyLimit:      0,
				MonthlyLimit:    0,
				AlertThresholds: DefaultBudgetAlertThresholds,
			},
			RateLimit: RateLimitConfig{
				Enabled:        false,
				DefaultRate:    10.0,
				DefaultBurst:   20,
				ProviderLimits: map[string]ProviderRateLimit{},
			},
		},
		Dashboard: DashboardConfig{
			Enabled:  true,
			AutoOpen: false,
		},
		Metrics: MetricsConfig{
			RetentionDays:   DefaultRetentionDays,
			CacheTTLSeconds: DefaultCacheTTL,
		},
		Plugins: PluginConfig{
			Enabled: false,
			Dir:     "~/.tokenman/plugins",
			Configs: map[string]map[string]interface{}{},
		},
	}
}
