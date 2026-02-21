package tracing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func setupTestTracer(t *testing.T) *tracetest.InMemoryExporter {
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

func TestHTTPMiddleware_CreatesSpan(t *testing.T) {
	exporter := setupTestTracer(t)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	handler := HTTPMiddleware(inner)
	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span")
	}

	span := spans[0]
	if span.Name != "GET /health" {
		t.Errorf("expected span name 'GET /health', got %q", span.Name)
	}
}

func TestHTTPMiddleware_CapturesStatusCode(t *testing.T) {
	exporter := setupTestTracer(t)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	handler := HTTPMiddleware(inner)
	req := httptest.NewRequest("GET", "/missing", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span")
	}

	// Verify http.response.status_code attribute was set.
	found := false
	for _, attr := range spans[0].Attributes {
		if string(attr.Key) == "http.response.status_code" {
			found = true
			if attr.Value.AsInt64() != 404 {
				t.Errorf("expected status_code 404, got %d", attr.Value.AsInt64())
			}
		}
	}
	if !found {
		t.Error("expected http.response.status_code attribute on span")
	}
}

func TestHTTPMiddleware_ServerErrorSetsSpanError(t *testing.T) {
	exporter := setupTestTracer(t)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	handler := HTTPMiddleware(inner)
	req := httptest.NewRequest("GET", "/error", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span")
	}

	// For 5xx, span status should be set to Error (code 2).
	if spans[0].Status.Code != 2 {
		t.Errorf("expected span status Error (2), got %d", spans[0].Status.Code)
	}
}

func TestHTTPMiddleware_ExtractsTraceContext(t *testing.T) {
	exporter := setupTestTracer(t)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The trace context should be available in the request context.
		span := trace.SpanFromContext(r.Context())
		if !span.SpanContext().IsValid() {
			t.Error("expected valid span context in request")
		}
		w.WriteHeader(http.StatusOK)
	})

	handler := HTTPMiddleware(inner)
	req := httptest.NewRequest("POST", "/v1/messages", nil)
	// Inject a W3C traceparent header.
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span")
	}

	// The span should be a child of the injected trace.
	traceID := spans[0].SpanContext.TraceID().String()
	if traceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("expected trace ID from injected header, got %s", traceID)
	}
}

func TestStatusWriter_DefaultsTo200(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rec}
	sw.Write([]byte("hello"))

	if sw.status != http.StatusOK {
		t.Errorf("expected default status 200, got %d", sw.status)
	}
}

func TestStatusWriter_Flush(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rec}

	// Should not panic even if flusher interface is available.
	sw.Flush()
}
