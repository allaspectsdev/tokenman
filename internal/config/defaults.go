package config

// DefaultBindAddress is the default bind address (localhost only for security).
const DefaultBindAddress = "127.0.0.1"

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

// DefaultReadTimeout is the default HTTP server read timeout in seconds.
const DefaultReadTimeout = 10

// DefaultWriteTimeout is the default HTTP server write timeout in seconds.
// Set high (5 minutes) to accommodate LLM streaming responses.
const DefaultWriteTimeout = 300

// DefaultIdleTimeout is the default HTTP server idle timeout in seconds.
const DefaultIdleTimeout = 120

// DefaultMaxBodySize is the default maximum request body size in bytes (10 MB).
const DefaultMaxBodySize = 10 << 20

// DefaultMaxResponseSize is the default maximum upstream response size in bytes (100 MB).
const DefaultMaxResponseSize int64 = 100 << 20

// DefaultStreamTimeout is the default streaming connection timeout in seconds (10 min).
const DefaultStreamTimeout = 600

// DefaultMaxStreamSessions is the default maximum number of concurrent stream sessions.
const DefaultMaxStreamSessions = 100

// DefaultSessionTTL is the default stream session time-to-live in seconds (1 hour).
const DefaultSessionTTL = 3600

// DefaultRetryMaxAttempts is the default maximum number of retry attempts per provider.
const DefaultRetryMaxAttempts = 3

// DefaultRetryBaseDelayMs is the default base delay for exponential backoff in milliseconds.
const DefaultRetryBaseDelayMs = 500

// DefaultRetryMaxDelayMs is the default maximum delay for exponential backoff in milliseconds.
const DefaultRetryMaxDelayMs = 30000

// DefaultCBFailureThreshold is the default number of consecutive failures before opening the circuit.
const DefaultCBFailureThreshold = 5

// DefaultCBResetTimeout is the default circuit breaker reset timeout in seconds.
const DefaultCBResetTimeout = 60

// DefaultCBHalfOpenMax is the default number of successful calls in half-open state to close the circuit.
const DefaultCBHalfOpenMax = 1

// DefaultTracingExporter is the default tracing exporter type.
const DefaultTracingExporter = "otlp-grpc"

// DefaultTracingEndpoint is the default OTLP collector endpoint.
const DefaultTracingEndpoint = "localhost:4317"

// DefaultTracingServiceName is the default service name for traces.
const DefaultTracingServiceName = "tokenman"

// DefaultTracingSampleRate is the default sampling rate (1.0 = 100%).
const DefaultTracingSampleRate = 1.0

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
			BindAddress:     DefaultBindAddress,
			ProxyPort:       DefaultProxyPort,
			DashboardPort:   DefaultDashboardPort,
			LogLevel:        DefaultLogLevel,
			DataDir:         DefaultDataDir,
			TLSEnabled:      false,
			CertFile:        "",
			KeyFile:         "",
			ReadTimeout:     DefaultReadTimeout,
			WriteTimeout:    DefaultWriteTimeout,
			IdleTimeout:     DefaultIdleTimeout,
			MaxBodySize:       DefaultMaxBodySize,
			MaxResponseSize:   DefaultMaxResponseSize,
			StreamTimeout:     DefaultStreamTimeout,
			MaxStreamSessions: DefaultMaxStreamSessions,
			SessionTTL:        DefaultSessionTTL,
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
		Resilience: ResilienceConfig{
			RetryMaxAttempts:   DefaultRetryMaxAttempts,
			RetryBaseDelayMs:   DefaultRetryBaseDelayMs,
			RetryMaxDelayMs:    DefaultRetryMaxDelayMs,
			CBEnabled:          true,
			CBFailureThreshold: DefaultCBFailureThreshold,
			CBResetTimeoutSec:  DefaultCBResetTimeout,
			CBHalfOpenMax:      DefaultCBHalfOpenMax,
		},
		Tracing: TracingConfig{
			Enabled:     false,
			Exporter:    DefaultTracingExporter,
			Endpoint:    DefaultTracingEndpoint,
			ServiceName: DefaultTracingServiceName,
			SampleRate:  DefaultTracingSampleRate,
			Insecure:    false,
		},
		Dashboard: DashboardConfig{
			Enabled:        true,
			AutoOpen:       false,
			AllowedOrigins: []string{"http://localhost:7677", "http://localhost:7678"},
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
