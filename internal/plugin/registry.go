package plugin

import (
	"fmt"
	"sync"

	"github.com/rs/zerolog/log"
)

// Registry manages loaded plugins.
type Registry struct {
	plugins    map[string]Plugin
	middleware []MiddlewarePlugin
	transforms []TransformPlugin
	hooks      []HookPlugin
	mu         sync.RWMutex
}

// NewRegistry creates an empty plugin registry.
func NewRegistry() *Registry {
	return &Registry{
		plugins: make(map[string]Plugin),
	}
}

// Register adds a plugin to the registry. The plugin's Init method is
// called with the provided config.
func (r *Registry) Register(p Plugin, config map[string]interface{}) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := p.Name()
	if _, exists := r.plugins[name]; exists {
		return fmt.Errorf("plugin %q already registered", name)
	}

	if err := p.Init(config); err != nil {
		return fmt.Errorf("initializing plugin %q: %w", name, err)
	}

	r.plugins[name] = p

	// Categorize by capability.
	if mp, ok := p.(MiddlewarePlugin); ok {
		r.middleware = append(r.middleware, mp)
	}
	if tp, ok := p.(TransformPlugin); ok {
		r.transforms = append(r.transforms, tp)
	}
	if hp, ok := p.(HookPlugin); ok {
		r.hooks = append(r.hooks, hp)
	}

	log.Info().Str("plugin", name).Str("version", p.Version()).Msg("plugin registered")
	return nil
}

// Unregister removes a plugin from the registry and calls its Close method.
func (r *Registry) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	p, exists := r.plugins[name]
	if !exists {
		return fmt.Errorf("plugin %q not found", name)
	}

	if err := p.Close(); err != nil {
		log.Warn().Err(err).Str("plugin", name).Msg("error closing plugin")
	}

	delete(r.plugins, name)

	// Rebuild slices without the removed plugin.
	r.middleware = filterMiddleware(r.middleware, name)
	r.transforms = filterTransforms(r.transforms, name)
	r.hooks = filterHooks(r.hooks, name)

	log.Info().Str("plugin", name).Msg("plugin unregistered")
	return nil
}

// PluginInfo is a summary of a registered plugin.
type PluginInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// List returns the names and versions of all registered plugins.
func (r *Registry) List() []PluginInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	infos := make([]PluginInfo, 0, len(r.plugins))
	for _, p := range r.plugins {
		infos = append(infos, PluginInfo{
			Name:    p.Name(),
			Version: p.Version(),
		})
	}
	return infos
}

// Middleware returns all registered middleware plugins.
func (r *Registry) Middleware() []MiddlewarePlugin {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]MiddlewarePlugin{}, r.middleware...)
}

// Transforms returns all registered transform plugins.
func (r *Registry) Transforms() []TransformPlugin {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]TransformPlugin{}, r.transforms...)
}

// Hooks returns all registered hook plugins.
func (r *Registry) Hooks() []HookPlugin {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]HookPlugin{}, r.hooks...)
}

// CloseAll closes all registered plugins.
func (r *Registry) CloseAll() {
	r.mu.Lock()
	defer r.mu.Unlock()

	for name, p := range r.plugins {
		if err := p.Close(); err != nil {
			log.Warn().Err(err).Str("plugin", name).Msg("error closing plugin")
		}
	}
	r.plugins = make(map[string]Plugin)
	r.middleware = nil
	r.transforms = nil
	r.hooks = nil
}

func filterMiddleware(slice []MiddlewarePlugin, name string) []MiddlewarePlugin {
	result := make([]MiddlewarePlugin, 0, len(slice))
	for _, p := range slice {
		if p.Name() != name {
			result = append(result, p)
		}
	}
	return result
}

func filterTransforms(slice []TransformPlugin, name string) []TransformPlugin {
	result := make([]TransformPlugin, 0, len(slice))
	for _, p := range slice {
		if p.Name() != name {
			result = append(result, p)
		}
	}
	return result
}

func filterHooks(slice []HookPlugin, name string) []HookPlugin {
	result := make([]HookPlugin, 0, len(slice))
	for _, p := range slice {
		if p.Name() != name {
			result = append(result, p)
		}
	}
	return result
}
