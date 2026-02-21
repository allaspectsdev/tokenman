package router

import (
	"time"

	"github.com/allaspects/tokenman/internal/pipeline"
)

// ProviderConfig holds the configuration for an upstream LLM provider.
type ProviderConfig struct {
	Name     string             `json:"name"`
	BaseURL  string             `json:"base_url"`
	APIKey   string             `json:"api_key"`
	Format   pipeline.APIFormat `json:"format"`
	Models   []string           `json:"models"`
	Enabled  bool               `json:"enabled"`
	Priority int                `json:"priority"`
	Timeout  time.Duration      `json:"timeout"`
}

// ProviderStatus represents the current health status of a provider.
type ProviderStatus struct {
	Name       string    `json:"name"`
	Healthy    bool      `json:"healthy"`
	LastCheck  time.Time `json:"last_check"`
	ErrorCount int       `json:"error_count"`
}

// SupportsModel returns true if this provider is configured to serve the
// given model name.
func (p *ProviderConfig) SupportsModel(model string) bool {
	for _, m := range p.Models {
		if m == model {
			return true
		}
	}
	return false
}
