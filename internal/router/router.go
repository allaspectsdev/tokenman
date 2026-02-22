package router

import (
	"fmt"
	"sort"
)

// Router resolves model names to provider configurations, supports explicit
// model→provider mappings, automatic discovery from provider model lists,
// and fallback ordering by priority.
type Router struct {
	providers       map[string]*ProviderConfig
	modelMap        map[string]string // model name → provider name
	defaultProvider string
	fallbackEnabled bool
}

// NewRouter creates a new Router.
//
//   - providers maps provider name → config.
//   - modelMap maps model name → provider name for explicit routing.
//   - defaultProvider is the provider used when no mapping is found.
//   - fallback enables returning multiple providers ordered by priority.
func NewRouter(providers map[string]*ProviderConfig, modelMap map[string]string, defaultProvider string, fallback bool) *Router {
	return &Router{
		providers:       providers,
		modelMap:        modelMap,
		defaultProvider: defaultProvider,
		fallbackEnabled: fallback,
	}
}

// Resolve finds the single best provider for a given model. The resolution
// order is:
//  1. Explicit entry in modelMap.
//  2. First enabled provider whose Models list contains the model.
//  3. The default provider.
func (r *Router) Resolve(model string) (*ProviderConfig, error) {
	// 1. Explicit model → provider mapping.
	if providerName, ok := r.modelMap[model]; ok {
		if p, exists := r.providers[providerName]; exists && p.Enabled {
			return p, nil
		}
	}

	// 2. Search enabled providers' model lists (prefer higher priority, i.e.
	//    lower Priority value).
	var best *ProviderConfig
	for _, p := range r.providers {
		if !p.Enabled {
			continue
		}
		if p.SupportsModel(model) {
			if best == nil || p.Priority < best.Priority {
				best = p
			}
		}
	}
	if best != nil {
		return best, nil
	}

	// 3. Fall back to the default provider.
	if r.defaultProvider != "" {
		if p, exists := r.providers[r.defaultProvider]; exists && p.Enabled {
			return p, nil
		}
	}

	return nil, fmt.Errorf("router: no provider found for model %q", model)
}

// ResolveWithFallback returns the primary provider for a model followed by
// fallback providers, ordered by priority (ascending). If fallback is disabled,
// this behaves like Resolve and returns a single-element slice.
func (r *Router) ResolveWithFallback(model string) ([]*ProviderConfig, error) {
	primary, err := r.Resolve(model)
	if err != nil {
		return nil, err
	}

	if !r.fallbackEnabled {
		return []*ProviderConfig{primary}, nil
	}

	// Collect all enabled providers except the primary that support the model.
	var fallbacks []*ProviderConfig
	for _, p := range r.providers {
		if !p.Enabled {
			continue
		}
		if p.Name == primary.Name {
			continue
		}
		if !p.SupportsModel(model) && len(p.Models) > 0 {
			continue // Skip providers that explicitly don't support this model
		}
		fallbacks = append(fallbacks, p)
	}

	// Sort fallbacks by priority (lower value = higher priority).
	sort.Slice(fallbacks, func(i, j int) bool {
		return fallbacks[i].Priority < fallbacks[j].Priority
	})

	result := make([]*ProviderConfig, 0, 1+len(fallbacks))
	result = append(result, primary)
	result = append(result, fallbacks...)
	return result, nil
}

// ListModels returns a de-duplicated, sorted list of all models available
// across all enabled providers.
func (r *Router) ListModels() []string {
	seen := make(map[string]bool)
	for _, p := range r.providers {
		if !p.Enabled {
			continue
		}
		for _, m := range p.Models {
			seen[m] = true
		}
	}

	models := make([]string, 0, len(seen))
	for m := range seen {
		models = append(models, m)
	}
	sort.Strings(models)
	return models
}
