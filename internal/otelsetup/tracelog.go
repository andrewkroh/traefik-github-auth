// Licensed to Andrew Kroh under one or more agreements.
// Andrew Kroh licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package otelsetup

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// TraceHandler wraps a slog.Handler and adds trace context attributes.
type TraceHandler struct {
	inner slog.Handler
}

// NewTraceHandler creates a new TraceHandler wrapping the given inner handler.
func NewTraceHandler(inner slog.Handler) *TraceHandler {
	return &TraceHandler{inner: inner}
}

// Enabled delegates to the inner handler.
func (h *TraceHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle extracts span context from ctx and adds trace.id and span.id
// as attributes to the log record when an active span is present.
func (h *TraceHandler) Handle(ctx context.Context, record slog.Record) error {
	sc := trace.SpanContextFromContext(ctx)
	if sc.IsValid() {
		record.AddAttrs(
			slog.String("trace.id", sc.TraceID().String()),
			slog.String("span.id", sc.SpanID().String()),
		)
	}
	return h.inner.Handle(ctx, record)
}

// WithAttrs returns a new TraceHandler wrapping the inner handler's WithAttrs result.
func (h *TraceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return NewTraceHandler(h.inner.WithAttrs(attrs))
}

// WithGroup returns a new TraceHandler wrapping the inner handler's WithGroup result.
func (h *TraceHandler) WithGroup(name string) slog.Handler {
	return NewTraceHandler(h.inner.WithGroup(name))
}
