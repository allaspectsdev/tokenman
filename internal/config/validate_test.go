package config

import (
	"strings"
	"testing"
)

func validConfig() *Config {
	cfg := DefaultConfig()
	cfg.Server.DataDir = "/tmp/test"
	return cfg
}

func TestValidate_ValidConfig(t *testing.T) {
	cfg := validConfig()
	if err := validate(cfg); err != nil {
		t.Fatalf("validate valid config: %v", err)
	}
}

func TestValidate_BadProxyPort(t *testing.T) {
	cfg := validConfig()
	cfg.Server.ProxyPort = 70000

	err := validate(cfg)
	if err == nil {
		t.Fatal("expected error for port 70000")
	}
	if !strings.Contains(err.Error(), "proxy_port") {
		t.Errorf("error should mention proxy_port: %v", err)
	}
}

func TestValidate_BadDashboardPort(t *testing.T) {
	cfg := validConfig()
	cfg.Server.DashboardPort = 0

	err := validate(cfg)
	if err == nil {
		t.Fatal("expected error for dashboard port 0")
	}
}

func TestValidate_BadLogLevel(t *testing.T) {
	cfg := validConfig()
	cfg.Server.LogLevel = "verbose"

	err := validate(cfg)
	if err == nil {
		t.Fatal("expected error for invalid log level")
	}
	if !strings.Contains(err.Error(), "log_level") {
		t.Errorf("error should mention log_level: %v", err)
	}
}

func TestValidate_EmptyDataDir(t *testing.T) {
	cfg := validConfig()
	cfg.Server.DataDir = ""

	err := validate(cfg)
	if err == nil {
		t.Fatal("expected error for empty data_dir")
	}
}

func TestValidate_TLS_MissingCert(t *testing.T) {
	cfg := validConfig()
	cfg.Server.TLSEnabled = true
	cfg.Server.CertFile = ""
	cfg.Server.KeyFile = "/path/to/key.pem"

	err := validate(cfg)
	if err == nil {
		t.Fatal("expected error for missing cert_file")
	}
	if !strings.Contains(err.Error(), "cert_file") {
		t.Errorf("error should mention cert_file: %v", err)
	}
}

func TestValidate_TLS_MissingKey(t *testing.T) {
	cfg := validConfig()
	cfg.Server.TLSEnabled = true
	cfg.Server.CertFile = "/path/to/cert.pem"
	cfg.Server.KeyFile = ""

	err := validate(cfg)
	if err == nil {
		t.Fatal("expected error for missing key_file")
	}
}

func TestValidate_NegativeReadTimeout(t *testing.T) {
	cfg := validConfig()
	cfg.Server.ReadTimeout = -1

	err := validate(cfg)
	if err == nil {
		t.Fatal("expected error for negative read_timeout")
	}
}

func TestValidate_NegativeMaxResponseSize(t *testing.T) {
	cfg := validConfig()
	cfg.Server.MaxResponseSize = -1

	err := validate(cfg)
	if err == nil {
		t.Fatal("expected error for negative max_response_size")
	}
}

func TestValidate_NegativeStreamTimeout(t *testing.T) {
	cfg := validConfig()
	cfg.Server.StreamTimeout = -1

	err := validate(cfg)
	if err == nil {
		t.Fatal("expected error for negative stream_timeout")
	}
}

func TestValidate_AuthTokenRequired(t *testing.T) {
	cfg := validConfig()
	cfg.Auth.Enabled = true
	cfg.Auth.Token = ""

	err := validate(cfg)
	if err == nil {
		t.Fatal("expected error for enabled auth with no token")
	}
}

func TestValidate_ProviderBadAPIBase(t *testing.T) {
	cfg := validConfig()
	cfg.Providers["bad"] = ProviderConfig{
		APIBase: "",
		Timeout: 30,
	}

	err := validate(cfg)
	if err == nil {
		t.Fatal("expected error for empty api_base")
	}
}

func TestValidate_ProviderNegativePriority(t *testing.T) {
	cfg := validConfig()
	cfg.Providers["bad"] = ProviderConfig{
		APIBase:  "https://example.com",
		Priority: -1,
		Timeout:  30,
	}

	err := validate(cfg)
	if err == nil {
		t.Fatal("expected error for negative priority")
	}
}

func TestValidate_RoutingUnknownProvider(t *testing.T) {
	cfg := validConfig()
	cfg.Routing.DefaultProvider = "nonexistent"

	err := validate(cfg)
	if err == nil {
		t.Fatal("expected error for unknown default_provider")
	}
}

func TestValidate_ModelMapUnknownProvider(t *testing.T) {
	cfg := validConfig()
	cfg.Routing.ModelMap = map[string]string{"some-model": "ghost"}

	err := validate(cfg)
	if err == nil {
		t.Fatal("expected error for unknown provider in model_map")
	}
}

func TestValidate_BadPIIAction(t *testing.T) {
	cfg := validConfig()
	cfg.Security.PII.Action = "explode"

	err := validate(cfg)
	if err == nil {
		t.Fatal("expected error for invalid PII action")
	}
}

func TestValidate_BadInjectionAction(t *testing.T) {
	cfg := validConfig()
	cfg.Security.Injection.Action = "destroy"

	err := validate(cfg)
	if err == nil {
		t.Fatal("expected error for invalid injection action")
	}
}

func TestValidate_NegativeBudgetLimit(t *testing.T) {
	cfg := validConfig()
	cfg.Security.Budget.HourlyLimit = -1

	err := validate(cfg)
	if err == nil {
		t.Fatal("expected error for negative hourly_limit")
	}
}

func TestValidate_AlertThresholdOutOfRange(t *testing.T) {
	cfg := validConfig()
	cfg.Security.Budget.AlertThresholds = []float64{50, 150}

	err := validate(cfg)
	if err == nil {
		t.Fatal("expected error for alert threshold > 100")
	}
}

func TestValidate_Resilience_NegativeRetryAttempts(t *testing.T) {
	cfg := validConfig()
	cfg.Resilience.RetryMaxAttempts = -1

	err := validate(cfg)
	if err == nil {
		t.Fatal("expected error for negative retry_max_attempts")
	}
}

func TestValidate_Resilience_ZeroFailureThreshold(t *testing.T) {
	cfg := validConfig()
	cfg.Resilience.CBFailureThreshold = 0

	err := validate(cfg)
	if err == nil {
		t.Fatal("expected error for cb_failure_threshold = 0")
	}
}

func TestValidate_Resilience_ZeroResetTimeout(t *testing.T) {
	cfg := validConfig()
	cfg.Resilience.CBResetTimeoutSec = 0

	err := validate(cfg)
	if err == nil {
		t.Fatal("expected error for cb_reset_timeout_seconds = 0")
	}
}

func TestValidate_Resilience_ZeroHalfOpenMax(t *testing.T) {
	cfg := validConfig()
	cfg.Resilience.CBHalfOpenMax = 0

	err := validate(cfg)
	if err == nil {
		t.Fatal("expected error for cb_half_open_max_calls = 0")
	}
}

func TestValidate_MetricsRetentionZero(t *testing.T) {
	cfg := validConfig()
	cfg.Metrics.RetentionDays = 0

	err := validate(cfg)
	if err == nil {
		t.Fatal("expected error for retention_days = 0")
	}
}

func TestValidate_NegativeCacheTTL(t *testing.T) {
	cfg := validConfig()
	cfg.Metrics.CacheTTLSeconds = -1

	err := validate(cfg)
	if err == nil {
		t.Fatal("expected error for negative cache_ttl_seconds")
	}
}

func TestValidate_MultipleErrors(t *testing.T) {
	cfg := validConfig()
	cfg.Server.ProxyPort = 0
	cfg.Server.DashboardPort = 0
	cfg.Server.LogLevel = "bad"

	err := validate(cfg)
	if err == nil {
		t.Fatal("expected multiple validation errors")
	}

	// Should contain multiple error indicators.
	errStr := err.Error()
	if !strings.Contains(errStr, "proxy_port") || !strings.Contains(errStr, "log_level") {
		t.Errorf("error should mention multiple fields: %v", err)
	}
}

func TestIsValidEnum(t *testing.T) {
	if !isValidEnum("INFO", ValidLogLevels) {
		t.Error("INFO should be valid (case-insensitive)")
	}
	if isValidEnum("verbose", ValidLogLevels) {
		t.Error("verbose should not be valid")
	}
}
