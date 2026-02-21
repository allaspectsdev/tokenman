package proxy

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/allaspectsdev/tokenman/internal/tracing"
)

// Server is the HTTP server for the TokenMan proxy. It binds the chi router
// to the configured address and provides graceful shutdown support.
type Server struct {
	router  chi.Router
	handler *ProxyHandler
	addr    string
	httpSrv *http.Server
}

// NewServer creates a new Server with the given ProxyHandler, listen address,
// and HTTP timeout durations. Zero-value timeouts leave the corresponding
// http.Server field at its default (no timeout). If tracingEnabled is true,
// the OpenTelemetry HTTP middleware is added to extract/inject trace context.
func NewServer(handler *ProxyHandler, addr string, readTimeout, writeTimeout, idleTimeout time.Duration, tracingEnabled bool) *Server {
	r := chi.NewRouter()

	// Standard chi middleware.
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)

	// OpenTelemetry trace context extraction/injection.
	if tracingEnabled {
		r.Use(tracing.HTTPMiddleware)
	}

	// Mount proxy routes.
	r.Post("/v1/messages", handler.HandleRequest)
	r.Post("/v1/chat/completions", handler.HandleRequest)
	r.Get("/v1/models", handler.HandleModels)
	r.Get("/health", handler.HandleHealth)
	r.Get("/health/ready", handler.HandleReady)

	// Stream session routes (SSE-based bidirectional streaming).
	r.Post("/v1/stream/create", handler.HandleStreamCreate)
	r.Post("/v1/stream/{id}/send", handler.HandleStreamSend)
	r.Get("/v1/stream/{id}/events", handler.HandleStreamEvents)
	r.Delete("/v1/stream/{id}", handler.HandleStreamDelete)

	srv := &Server{
		router:  r,
		handler: handler,
		addr:    addr,
	}

	srv.httpSrv = &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		IdleTimeout:  idleTimeout,
	}

	return srv
}

// Router returns the underlying chi.Router, useful for testing or additional
// route mounting by the caller.
func (s *Server) Router() chi.Router {
	return s.router
}

// Start begins listening for HTTP connections on the configured address.
// It blocks until the server is shut down or encounters a fatal error.
func (s *Server) Start() error {
	return s.httpSrv.ListenAndServe()
}

// StartTLS begins listening for HTTPS connections using the given certificate
// and key files. It blocks until the server is shut down or encounters a fatal error.
func (s *Server) StartTLS(certFile, keyFile string) error {
	if err := s.httpSrv.ListenAndServeTLS(certFile, keyFile); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("proxy server (TLS): %w", err)
	}
	return nil
}

// Shutdown gracefully stops the server, waiting for in-flight requests to
// complete within the given context deadline.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpSrv.Shutdown(ctx)
}
