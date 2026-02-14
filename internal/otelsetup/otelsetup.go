// Licensed to Andrew Kroh under one or more agreements.
// Andrew Kroh licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

// Package otelsetup provides OpenTelemetry bootstrap helpers.
package otelsetup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/contrib/instrumentation/host"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.39.0"
)

// Setup initializes OpenTelemetry with trace and metric providers.
//
// Traces are only enabled when OTEL_TRACES_EXPORTER is explicitly set
// to a value other than "none". Metrics are only enabled when
// OTEL_METRICS_EXPORTER is explicitly set to a value other than "none".
// The autoexport package handles exporter selection based on standard
// OTel environment variables (e.g., OTEL_EXPORTER_OTLP_PROTOCOL for
// gRPC vs HTTP).
//
// Returns a shutdown function that should be deferred by the caller.
func Setup(ctx context.Context, serviceName, serviceVersion string) (shutdown func(context.Context) error, err error) {
	var shutdownFuncs []func(context.Context) error

	shutdown = func(ctx context.Context) error {
		var errs []error
		for _, fn := range shutdownFuncs {
			if fnErr := fn(ctx); fnErr != nil {
				errs = append(errs, fnErr)
			}
		}
		return errors.Join(errs...)
	}

	// Build resource with service info. Standard OTel env vars
	// (OTEL_SERVICE_NAME, OTEL_RESOURCE_ATTRIBUTES) are picked up
	// automatically by resource.Default().
	res, err := resource.Merge(
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(serviceVersion),
		),
		resource.Default(),
	)
	if err != nil {
		return shutdown, fmt.Errorf("failed to create resource: %w", err)
	}

	// Set up the propagator (W3C TraceContext + Baggage).
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// Traces: Quiet opt-in. Only initialize if the user explicitly set an exporter.
	// This prevents OTel from defaulting to 'otlp' and logging connection errors.
	tracesExporter := os.Getenv("OTEL_TRACES_EXPORTER")
	if tracesExporter != "" && tracesExporter != "none" {
		spanExporter, err := autoexport.NewSpanExporter(ctx)
		if err != nil {
			return shutdown, fmt.Errorf("failed to create span exporter: %w", err)
		}

		tracerProvider := sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(spanExporter),
			sdktrace.WithResource(res),
		)
		shutdownFuncs = append(shutdownFuncs, tracerProvider.Shutdown)
		otel.SetTracerProvider(tracerProvider)
	}

	// Metrics: Quiet opt-in. Only initialize if the user explicitly set an exporter.
	// This prevents OTel from defaulting to 'otlp' and logging connection errors.
	metricsExporter := os.Getenv("OTEL_METRICS_EXPORTER")
	if metricsExporter != "" && metricsExporter != "none" {
		reader, err := autoexport.NewMetricReader(ctx)
		if err != nil {
			return shutdown, fmt.Errorf("failed to create metric reader: %w", err)
		}

		meterProvider := metric.NewMeterProvider(
			metric.WithResource(res),
			metric.WithReader(reader),
		)
		shutdownFuncs = append(shutdownFuncs, meterProvider.Shutdown)
		otel.SetMeterProvider(meterProvider)

		// Start Go runtime metrics collection.
		if err := runtime.Start(runtime.WithMinimumReadMemStatsInterval(15 * time.Second)); err != nil {
			return shutdown, fmt.Errorf("failed to start runtime metrics: %w", err)
		}

		// Start host/process metrics collection.
		if err := host.Start(); err != nil {
			return shutdown, fmt.Errorf("failed to start host metrics: %w", err)
		}
	}

	return shutdown, nil
}

// NewLogger creates a new slog.Logger with JSON output and trace context integration.
func NewLogger(w io.Writer) *slog.Logger {
	jsonHandler := slog.NewJSONHandler(w, nil)
	return slog.New(NewTraceHandler(jsonHandler))
}
