package router

import (
	"testing"
)

func makeProviders() map[string]*ProviderConfig {
	return map[string]*ProviderConfig{
		"anthropic": {
			Name:     "anthropic",
			BaseURL:  "https://api.anthropic.com",
			Models:   []string{"claude-3-opus", "claude-3-sonnet", "claude-3-haiku"},
			Enabled:  true,
			Priority: 1,
		},
		"openai": {
			Name:     "openai",
			BaseURL:  "https://api.openai.com",
			Models:   []string{"gpt-4o", "gpt-4o-mini", "gpt-3.5-turbo"},
			Enabled:  true,
			Priority: 2,
		},
		"backup": {
			Name:     "backup",
			BaseURL:  "https://backup.example.com",
			Models:   []string{"gpt-4o", "claude-3-sonnet"},
			Enabled:  true,
			Priority: 3,
		},
	}
}

func TestResolve_ExplicitModelMap(t *testing.T) {
	providers := makeProviders()
	modelMap := map[string]string{
		"gpt-4o": "backup", // explicitly route gpt-4o to backup
	}
	r := NewRouter(providers, modelMap, "openai", true)

	p, err := r.Resolve("gpt-4o")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if p.Name != "backup" {
		t.Fatalf("expected explicit mapping to 'backup', got %q", p.Name)
	}
}

func TestResolve_FindsInProviderModelList(t *testing.T) {
	providers := makeProviders()
	modelMap := map[string]string{} // no explicit mapping
	r := NewRouter(providers, modelMap, "", false)

	p, err := r.Resolve("claude-3-opus")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if p.Name != "anthropic" {
		t.Fatalf("expected 'anthropic' (has model in list), got %q", p.Name)
	}
}

func TestResolve_FallsBackToDefaultProvider(t *testing.T) {
	providers := makeProviders()
	modelMap := map[string]string{}
	r := NewRouter(providers, modelMap, "openai", false)

	// "some-unknown-model" is not in any provider's model list.
	p, err := r.Resolve("some-unknown-model")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if p.Name != "openai" {
		t.Fatalf("expected fallback to default 'openai', got %q", p.Name)
	}
}

func TestResolve_ErrorWhenNoProviderFound(t *testing.T) {
	providers := makeProviders()
	modelMap := map[string]string{}
	r := NewRouter(providers, modelMap, "", false) // no default

	_, err := r.Resolve("nonexistent-model")
	if err == nil {
		t.Fatal("expected error when no provider found, got nil")
	}
}

func TestResolveWithFallback_ReturnsMultipleOrdered(t *testing.T) {
	providers := makeProviders()
	modelMap := map[string]string{}
	r := NewRouter(providers, modelMap, "anthropic", true)

	results, err := r.ResolveWithFallback("claude-3-opus")
	if err != nil {
		t.Fatalf("ResolveWithFallback error: %v", err)
	}

	if len(results) < 2 {
		t.Fatalf("expected at least 2 providers (primary + fallbacks), got %d", len(results))
	}

	// Primary should be first (anthropic has priority 1 and contains the model).
	if results[0].Name != "anthropic" {
		t.Fatalf("expected primary to be 'anthropic', got %q", results[0].Name)
	}

	// Fallbacks should be ordered by priority.
	for i := 1; i < len(results)-1; i++ {
		if results[i].Priority > results[i+1].Priority {
			t.Fatalf("fallbacks not ordered by priority: %q (pri=%d) before %q (pri=%d)",
				results[i].Name, results[i].Priority, results[i+1].Name, results[i+1].Priority)
		}
	}
}

func TestResolveWithFallback_FallbackDisabled(t *testing.T) {
	providers := makeProviders()
	modelMap := map[string]string{}
	r := NewRouter(providers, modelMap, "anthropic", false) // fallback disabled

	results, err := r.ResolveWithFallback("claude-3-opus")
	if err != nil {
		t.Fatalf("ResolveWithFallback error: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 provider (fallback disabled), got %d", len(results))
	}
	if results[0].Name != "anthropic" {
		t.Fatalf("expected 'anthropic', got %q", results[0].Name)
	}
}

func TestListModels_DeduplicatedAndSorted(t *testing.T) {
	providers := makeProviders()
	r := NewRouter(providers, nil, "", false)

	models := r.ListModels()

	// Check for deduplication: gpt-4o and claude-3-sonnet appear in
	// multiple providers but should only appear once.
	seen := make(map[string]int)
	for _, m := range models {
		seen[m]++
	}
	for model, count := range seen {
		if count > 1 {
			t.Fatalf("model %q appears %d times; should be deduplicated", model, count)
		}
	}

	// Check sorted order.
	for i := 1; i < len(models); i++ {
		if models[i] < models[i-1] {
			t.Fatalf("models not sorted: %q comes after %q", models[i], models[i-1])
		}
	}

	// Verify expected models are present.
	expectedModels := []string{"claude-3-haiku", "claude-3-opus", "claude-3-sonnet", "gpt-3.5-turbo", "gpt-4o", "gpt-4o-mini"}
	if len(models) != len(expectedModels) {
		t.Fatalf("expected %d models, got %d: %v", len(expectedModels), len(models), models)
	}
	for i, exp := range expectedModels {
		if models[i] != exp {
			t.Fatalf("expected model[%d]=%q, got %q", i, exp, models[i])
		}
	}
}

func TestDisabledProvidersAreSkipped(t *testing.T) {
	providers := map[string]*ProviderConfig{
		"disabled-provider": {
			Name:     "disabled-provider",
			BaseURL:  "https://disabled.example.com",
			Models:   []string{"special-model"},
			Enabled:  false,
			Priority: 1,
		},
		"enabled-provider": {
			Name:     "enabled-provider",
			BaseURL:  "https://enabled.example.com",
			Models:   []string{"other-model"},
			Enabled:  true,
			Priority: 2,
		},
	}

	r := NewRouter(providers, nil, "", false)

	// Resolve should not find "special-model" because its provider is disabled.
	_, err := r.Resolve("special-model")
	if err == nil {
		t.Fatal("expected error resolving model from disabled provider, got nil")
	}

	// ListModels should not include models from disabled providers.
	models := r.ListModels()
	for _, m := range models {
		if m == "special-model" {
			t.Fatal("disabled provider's models should not appear in ListModels")
		}
	}
}

func TestSupportsModel(t *testing.T) {
	p := &ProviderConfig{
		Name:   "test",
		Models: []string{"model-a", "model-b", "model-c"},
	}

	if !p.SupportsModel("model-a") {
		t.Fatal("expected SupportsModel to return true for 'model-a'")
	}
	if !p.SupportsModel("model-c") {
		t.Fatal("expected SupportsModel to return true for 'model-c'")
	}
	if p.SupportsModel("model-x") {
		t.Fatal("expected SupportsModel to return false for 'model-x'")
	}
	if p.SupportsModel("") {
		t.Fatal("expected SupportsModel to return false for empty string")
	}
}
