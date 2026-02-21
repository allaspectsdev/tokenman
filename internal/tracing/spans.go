package tracing

import (
	"context"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// StartPipelineSpan creates a child span for the full pipeline processing phase.
func StartPipelineSpan(ctx context.Context, phase string) (context.Context, trace.Span) {
	return Tracer().Start(ctx, "pipeline."+phase,
		trace.WithAttributes(attribute.String("pipeline.phase", phase)),
	)
}

// StartMiddlewareSpan creates a child span for a single middleware execution.
func StartMiddlewareSpan(ctx context.Context, name, phase string) (context.Context, trace.Span) {
	return Tracer().Start(ctx, "middleware."+name+"."+phase,
		trace.WithAttributes(
			attribute.String("middleware.name", name),
			attribute.String("middleware.phase", phase),
		),
	)
}

// StartUpstreamSpan creates a child span for an upstream HTTP call.
// It returns the context, span, and a function to inject trace headers into the request.
func StartUpstreamSpan(ctx context.Context, url, provider string) (context.Context, trace.Span) {
	return Tracer().Start(ctx, "upstream.forward",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("upstream.url", url),
			attribute.String("upstream.provider", provider),
		),
	)
}

// InjectHeaders injects the current trace context (traceparent, tracestate)
// into the given HTTP request headers so the upstream service can continue
// the trace.
func InjectHeaders(ctx context.Context, req *http.Request) {
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))
}

// SetRequestAttributes adds request-level attributes to the current span.
func SetRequestAttributes(ctx context.Context, requestID, model, format string, stream bool) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("request.id", requestID),
		attribute.String("request.model", model),
		attribute.String("request.format", format),
		attribute.Bool("request.stream", stream),
	)
}

// SetResponseAttributes adds response-level attributes to the current span.
func SetResponseAttributes(ctx context.Context, statusCode int, tokensIn, tokensOut int, cacheHit bool, provider string) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.Int("response.status_code", statusCode),
		attribute.Int("response.tokens_in", tokensIn),
		attribute.Int("response.tokens_out", tokensOut),
		attribute.Bool("response.cache_hit", cacheHit),
		attribute.String("response.provider", provider),
	)
}

// RecordError records an error on the current span.
func RecordError(ctx context.Context, err error) {
	if err != nil {
		trace.SpanFromContext(ctx).RecordError(err)
	}
}
