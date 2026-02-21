package tracing

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func TestInit_StdoutExporter(t *testing.T) {
	shutdown, err := Init(context.Background(), "test-service", "1.0.0", "stdout", "", 1.0, false)
	if err != nil {
		t.Fatalf("Init with stdout exporter: %v", err)
	}
	defer shutdown(context.Background())

	// Verify a global tracer provider was registered.
	tp := otel.GetTracerProvider()
	if tp == nil {
		t.Fatal("expected non-nil TracerProvider")
	}

	// Verify the propagator was set.
	prop := otel.GetTextMapPropagator()
	if prop == nil {
		t.Fatal("expected non-nil TextMapPropagator")
	}
}

func TestInit_UnknownExporter(t *testing.T) {
	_, err := Init(context.Background(), "test", "1.0.0", "unknown", "", 1.0, false)
	if err == nil {
		t.Fatal("expected error for unknown exporter")
	}
}

func TestTracer_ReturnsNonNil(t *testing.T) {
	tr := Tracer()
	if tr == nil {
		t.Fatal("expected non-nil Tracer")
	}
}

func TestInit_Shutdown(t *testing.T) {
	shutdown, err := Init(context.Background(), "test-service", "1.0.0", "stdout", "", 0.5, false)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Shutdown should not error.
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestInit_SetsW3CPropagator(t *testing.T) {
	shutdown, err := Init(context.Background(), "test", "1.0.0", "stdout", "", 1.0, false)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer shutdown(context.Background())

	prop := otel.GetTextMapPropagator()
	// The composite propagator should handle traceparent/tracestate fields.
	fields := prop.Fields()
	if len(fields) == 0 {
		t.Fatal("expected propagator to declare fields")
	}

	foundTraceparent := false
	for _, f := range fields {
		if f == "traceparent" {
			foundTraceparent = true
		}
	}
	if !foundTraceparent {
		t.Errorf("expected 'traceparent' in propagator fields, got %v", fields)
	}
}

func TestInit_SamplingRate(t *testing.T) {
	// With sample rate 0, spans should still be created (just not sampled).
	shutdown, err := Init(context.Background(), "test", "1.0.0", "stdout", "", 0.0, false)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer shutdown(context.Background())

	tracer := Tracer()
	_, span := tracer.Start(context.Background(), "test-span")
	defer span.End()

	// The span context should still be valid even if not sampled.
	sc := span.SpanContext()
	if !sc.TraceID().IsValid() {
		t.Error("expected valid trace ID even with 0 sample rate")
	}
}

func TestNewExporter_OTLPGrpcInsecure(t *testing.T) {
	// Test that otlp-grpc exporter creation works (won't connect, but shouldn't error).
	exp, err := newExporter(context.Background(), "otlp-grpc", "localhost:4317", true)
	if err != nil {
		t.Fatalf("newExporter otlp-grpc: %v", err)
	}
	if exp == nil {
		t.Fatal("expected non-nil exporter")
	}
}

func TestNewExporter_OTLPHttpInsecure(t *testing.T) {
	exp, err := newExporter(context.Background(), "otlp-http", "localhost:4318", true)
	if err != nil {
		t.Fatalf("newExporter otlp-http: %v", err)
	}
	if exp == nil {
		t.Fatal("expected non-nil exporter")
	}
}

// Ensure global state is clean for later tests by resetting to noop.
func TestInit_ResetGlobal(t *testing.T) {
	otel.SetTracerProvider(trace.NewNoopTracerProvider())
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())
}
