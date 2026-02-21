package plugin

import (
	"context"

	"github.com/allaspects/tokenman/internal/pipeline"
)

// Plugin defines the interface that all plugins must implement.
type Plugin interface {
	// Name returns the unique name of this plugin.
	Name() string

	// Version returns the plugin version string.
	Version() string

	// Init is called once when the plugin is loaded.
	Init(config map[string]interface{}) error

	// Close is called when the plugin is being unloaded.
	Close() error
}

// MiddlewarePlugin is a Plugin that also acts as pipeline middleware.
type MiddlewarePlugin interface {
	Plugin
	pipeline.Middleware
}

// TransformPlugin can transform requests and responses without the full
// middleware interface.
type TransformPlugin interface {
	Plugin
	TransformRequest(ctx context.Context, req *pipeline.Request) (*pipeline.Request, error)
	TransformResponse(ctx context.Context, req *pipeline.Request, resp *pipeline.Response) (*pipeline.Response, error)
}

// HookPlugin receives notifications about request lifecycle events.
type HookPlugin interface {
	Plugin
	OnRequestStart(ctx context.Context, req *pipeline.Request)
	OnRequestComplete(ctx context.Context, req *pipeline.Request, resp *pipeline.Response)
	OnError(ctx context.Context, req *pipeline.Request, err error)
}
