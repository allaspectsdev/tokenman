package pipeline

import "context"

// Middleware interface that all pipeline stages implement.
type Middleware interface {
	// Name returns the unique name of this middleware.
	Name() string

	// Enabled reports whether this middleware is active.
	Enabled() bool

	// ProcessRequest processes an incoming request. Middleware may modify the
	// request, short-circuit the pipeline by setting req.Flags["cache_hit"]=true
	// and storing a *CachedResponse in the context, or return an error to abort.
	ProcessRequest(ctx context.Context, req *Request) (*Request, error)

	// ProcessResponse processes an outgoing response. Middleware may modify the
	// response or return an error.
	ProcessResponse(ctx context.Context, req *Request, resp *Response) (*Response, error)
}
