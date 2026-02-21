package tracing

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func setupTestTracerWithPropagator(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	t.Cleanup(func() {
		tp.Shutdown(context.Background())
		otel.SetTracerProvider(trace.NewNoopTracerProvider())
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())
	})
	return exporter
}

func TestStartPipelineSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	otel.SetTracerProvider(tp)
	defer func() {
		tp.Shutdown(context.Background())
		otel.SetTracerProvider(trace.NewNoopTracerProvider())
	}()

	ctx, span := StartPipelineSpan(context.Background(), "request")
	defer span.End()

	if !trace.SpanFromContext(ctx).SpanContext().IsValid() {
		t.Error("expected valid span in context")
	}

	span.End()
	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span")
	}
	if spans[0].Name != "pipeline.request" {
		t.Errorf("expected span name 'pipeline.request', got %q", spans[0].Name)
	}
}

func TestStartMiddlewareSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	otel.SetTracerProvider(tp)
	defer func() {
		tp.Shutdown(context.Background())
		otel.SetTracerProvider(trace.NewNoopTracerProvider())
	}()

	_, span := StartMiddlewareSpan(context.Background(), "cache", "request")
	span.End()

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span")
	}
	if spans[0].Name != "middleware.cache.request" {
		t.Errorf("expected span name 'middleware.cache.request', got %q", spans[0].Name)
	}

	// Check attributes.
	found := map[string]bool{}
	for _, attr := range spans[0].Attributes {
		found[string(attr.Key)] = true
	}
	if !found["middleware.name"] {
		t.Error("expected middleware.name attribute")
	}
	if !found["middleware.phase"] {
		t.Error("expected middleware.phase attribute")
	}
}

func TestStartUpstreamSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	otel.SetTracerProvider(tp)
	defer func() {
		tp.Shutdown(context.Background())
		otel.SetTracerProvider(trace.NewNoopTracerProvider())
	}()

	_, span := StartUpstreamSpan(context.Background(), "https://api.example.com/v1/messages", "anthropic")
	span.End()

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span")
	}
	if spans[0].Name != "upstream.forward" {
		t.Errorf("expected span name 'upstream.forward', got %q", spans[0].Name)
	}
	if spans[0].SpanKind != trace.SpanKindClient {
		t.Errorf("expected SpanKindClient, got %v", spans[0].SpanKind)
	}
}

func TestInjectHeaders(t *testing.T) {
	setupTestTracerWithPropagator(t)

	ctx, span := Tracer().Start(context.Background(), "test")
	defer span.End()

	req := httptest.NewRequest("POST", "/v1/messages", nil)
	InjectHeaders(ctx, req)

	// The traceparent header should be set.
	tp2 := req.Header.Get("traceparent")
	if tp2 == "" {
		t.Error("expected traceparent header to be injected")
	}
}

func TestSetRequestAttributes(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	otel.SetTracerProvider(tp)
	defer func() {
		tp.Shutdown(context.Background())
		otel.SetTracerProvider(trace.NewNoopTracerProvider())
	}()

	ctx, span := Tracer().Start(context.Background(), "test")
	SetRequestAttributes(ctx, "req-123", "claude-sonnet-4-20250514", "anthropic", false)
	span.End()

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span")
	}

	attrs := map[string]interface{}{}
	for _, attr := range spans[0].Attributes {
		attrs[string(attr.Key)] = attr.Value.AsInterface()
	}

	if attrs["request.id"] != "req-123" {
		t.Errorf("expected request.id 'req-123', got %v", attrs["request.id"])
	}
	if attrs["request.model"] != "claude-sonnet-4-20250514" {
		t.Errorf("expected request.model, got %v", attrs["request.model"])
	}
}

func TestSetResponseAttributes(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	otel.SetTracerProvider(tp)
	defer func() {
		tp.Shutdown(context.Background())
		otel.SetTracerProvider(trace.NewNoopTracerProvider())
	}()

	ctx, span := Tracer().Start(context.Background(), "test")
	SetResponseAttributes(ctx, 200, 100, 50, false, "anthropic")
	span.End()

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span")
	}

	attrs := map[string]interface{}{}
	for _, attr := range spans[0].Attributes {
		attrs[string(attr.Key)] = attr.Value.AsInterface()
	}

	if attrs["response.status_code"] != int64(200) {
		t.Errorf("expected response.status_code 200, got %v", attrs["response.status_code"])
	}
	if attrs["response.tokens_out"] != int64(50) {
		t.Errorf("expected response.tokens_out 50, got %v", attrs["response.tokens_out"])
	}
}

func TestRecordError_NilDoesNotPanic(t *testing.T) {
	// Should not panic with a nil error.
	RecordError(context.Background(), nil)
}

func TestRecordError_RecordsOnSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	otel.SetTracerProvider(tp)
	defer func() {
		tp.Shutdown(context.Background())
		otel.SetTracerProvider(trace.NewNoopTracerProvider())
	}()

	ctx, span := Tracer().Start(context.Background(), "test")
	RecordError(ctx, errors.New("test error"))
	span.End()

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span")
	}

	if len(spans[0].Events) == 0 {
		t.Error("expected error event on span")
	}
}

func TestInjectHeaders_WithHTTPRequest(t *testing.T) {
	setupTestTracerWithPropagator(t)

	ctx, span := Tracer().Start(context.Background(), "parent")
	defer span.End()

	req, _ := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", nil)
	InjectHeaders(ctx, req)

	traceparent := req.Header.Get("traceparent")
	if traceparent == "" {
		t.Fatal("expected traceparent header")
	}

	// Format: version-traceid-spanid-flags
	// Should contain the trace ID from the parent span.
	parentTraceID := span.SpanContext().TraceID().String()
	if len(traceparent) < 55 {
		t.Fatalf("traceparent too short: %s", traceparent)
	}
	extractedTraceID := traceparent[3:35]
	if extractedTraceID != parentTraceID {
		t.Errorf("expected trace ID %s in traceparent, got %s", parentTraceID, extractedTraceID)
	}
}
