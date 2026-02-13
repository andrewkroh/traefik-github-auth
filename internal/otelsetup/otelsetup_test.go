// Licensed to Andrew Kroh under one or more agreements.
// Andrew Kroh licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package otelsetup

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestSetup_NoEndpoint(t *testing.T) {
	// Ensure OTEL_EXPORTER_OTLP_ENDPOINT is not set.
	os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")

	ctx := context.Background()
	shutdown, err := Setup(ctx, "test-service", "0.0.1")
	if err != nil {
		t.Fatalf("Setup returned unexpected error: %v", err)
	}
	if shutdown == nil {
		t.Fatal("Setup returned nil shutdown function")
	}

	// Shutdown may return errors when no OTLP collector is running
	// (the SDK defaults to localhost:4318). The important thing is
	// that Setup itself did not error and the shutdown function is callable.
	_ = shutdown(ctx)
}

// captureHandler is a slog.Handler that captures the last record's attributes.
type captureHandler struct {
	attrs   []slog.Attr
	enabled bool
	group   string
	extra   []slog.Attr
}

func newCaptureHandler() *captureHandler {
	return &captureHandler{enabled: true}
}

func (h *captureHandler) Enabled(_ context.Context, _ slog.Level) bool {
	return h.enabled
}

func (h *captureHandler) Handle(_ context.Context, record slog.Record) error {
	h.attrs = nil
	record.Attrs(func(a slog.Attr) bool {
		h.attrs = append(h.attrs, a)
		return true
	})
	return nil
}

func (h *captureHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &captureHandler{
		enabled: h.enabled,
		extra:   append(h.extra, attrs...),
	}
}

func (h *captureHandler) WithGroup(name string) slog.Handler {
	return &captureHandler{
		enabled: h.enabled,
		group:   name,
	}
}

func TestTraceHandler_NoSpanContext(t *testing.T) {
	inner := newCaptureHandler()
	handler := NewTraceHandler(inner)

	ctx := context.Background()
	rec := slog.Record{}
	rec.Message = "test message"

	if err := handler.Handle(ctx, rec); err != nil {
		t.Fatalf("Handle returned unexpected error: %v", err)
	}

	// Verify that trace.id and span.id were NOT added.
	for _, attr := range inner.attrs {
		if attr.Key == "trace.id" || attr.Key == "span.id" {
			t.Errorf("unexpected attribute %q found in record without span context", attr.Key)
		}
	}
}

func TestTraceHandler_WithSpanContext(t *testing.T) {
	// Create an in-memory tracer provider.
	tp := sdktrace.NewTracerProvider()
	defer tp.Shutdown(context.Background())

	tracer := tp.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "test-span")
	defer span.End()

	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		t.Fatal("expected valid span context")
	}

	inner := newCaptureHandler()
	handler := NewTraceHandler(inner)

	rec := slog.Record{}
	rec.Message = "test with span"

	if err := handler.Handle(ctx, rec); err != nil {
		t.Fatalf("Handle returned unexpected error: %v", err)
	}

	var foundTraceID, foundSpanID bool
	for _, attr := range inner.attrs {
		switch attr.Key {
		case "trace.id":
			foundTraceID = true
			if attr.Value.String() != sc.TraceID().String() {
				t.Errorf("trace.id = %q, want %q", attr.Value.String(), sc.TraceID().String())
			}
		case "span.id":
			foundSpanID = true
			if attr.Value.String() != sc.SpanID().String() {
				t.Errorf("span.id = %q, want %q", attr.Value.String(), sc.SpanID().String())
			}
		}
	}

	if !foundTraceID {
		t.Error("trace.id attribute not found")
	}
	if !foundSpanID {
		t.Error("span.id attribute not found")
	}
}

func TestTraceHandler_Enabled(t *testing.T) {
	inner := newCaptureHandler()
	inner.enabled = false
	handler := NewTraceHandler(inner)

	ctx := context.Background()
	if handler.Enabled(ctx, slog.LevelInfo) {
		t.Error("Enabled should return false when inner handler returns false")
	}

	inner.enabled = true
	if !handler.Enabled(ctx, slog.LevelInfo) {
		t.Error("Enabled should return true when inner handler returns true")
	}
}

func TestTraceHandler_WithAttrs(t *testing.T) {
	inner := newCaptureHandler()
	handler := NewTraceHandler(inner)

	attrs := []slog.Attr{slog.String("key", "value")}
	result := handler.WithAttrs(attrs)

	th, ok := result.(*TraceHandler)
	if !ok {
		t.Fatal("WithAttrs should return a *TraceHandler")
	}

	// The inner handler should be a captureHandler with the extra attrs.
	ch, ok := th.inner.(*captureHandler)
	if !ok {
		t.Fatal("inner handler should be a *captureHandler")
	}
	if len(ch.extra) != 1 || ch.extra[0].Key != "key" {
		t.Errorf("inner handler should have extra attrs, got %v", ch.extra)
	}
}

func TestTraceHandler_WithGroup(t *testing.T) {
	inner := newCaptureHandler()
	handler := NewTraceHandler(inner)

	result := handler.WithGroup("mygroup")

	th, ok := result.(*TraceHandler)
	if !ok {
		t.Fatal("WithGroup should return a *TraceHandler")
	}

	// The inner handler should be a captureHandler with the group set.
	ch, ok := th.inner.(*captureHandler)
	if !ok {
		t.Fatal("inner handler should be a *captureHandler")
	}
	if ch.group != "mygroup" {
		t.Errorf("inner handler group = %q, want %q", ch.group, "mygroup")
	}
}

func TestNewLogger(t *testing.T) {
	// Create a tracer provider and start a span.
	tp := sdktrace.NewTracerProvider()
	defer tp.Shutdown(context.Background())

	tracer := tp.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "test-span")
	defer span.End()

	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		t.Fatal("expected valid span context")
	}

	// Create a logger writing to a buffer.
	var buf bytes.Buffer
	logger := NewLogger(&buf)

	// Log with the span context.
	logger.InfoContext(ctx, "hello world")

	// Parse the JSON output.
	var entry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("failed to parse JSON log output: %v\nraw output: %s", err, buf.String())
	}

	// Verify it contains trace.id and span.id.
	traceID, ok := entry["trace.id"]
	if !ok {
		t.Fatal("JSON output missing trace.id")
	}
	if traceID != sc.TraceID().String() {
		t.Errorf("trace.id = %q, want %q", traceID, sc.TraceID().String())
	}

	spanID, ok := entry["span.id"]
	if !ok {
		t.Fatal("JSON output missing span.id")
	}
	if spanID != sc.SpanID().String() {
		t.Errorf("span.id = %q, want %q", spanID, sc.SpanID().String())
	}

	// Verify it's valid JSON (it is, since we parsed it).
	if _, ok := entry["msg"]; !ok {
		t.Error("JSON output missing msg field")
	}
}
