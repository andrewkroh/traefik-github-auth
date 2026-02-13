// Licensed to Andrew Kroh under one or more agreements.
// Andrew Kroh licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

// Package otelsetup provides OpenTelemetry bootstrap helpers.
package otelsetup

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Setup initializes OpenTelemetry with OTLP exporters for traces and metrics.
// Configuration is driven by standard OTEL_* environment variables.
// It returns a shutdown function that should be deferred by the caller.
func Setup(ctx context.Context, serviceName, serviceVersion string) (shutdown func(context.Context) error, err error) {
	var shutdownFuncs []func(context.Context) error

	// Build the shutdown function that calls all registered shutdown functions.
	shutdown = func(ctx context.Context) error {
		var errs []error
		for _, fn := range shutdownFuncs {
			if fnErr := fn(ctx); fnErr != nil {
				errs = append(errs, fnErr)
			}
		}
		return errors.Join(errs...)
	}

	// Create the resource with service name and version.
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(serviceVersion),
		),
	)
	if err != nil {
		return shutdown, err
	}

	// Set up the propagator (W3C TraceContext + Baggage).
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// Set up the trace exporter and provider.
	// Check OTEL_TRACES_EXPORTER env var (supports "console", "otlp", "none").
	tracesExporter := os.Getenv("OTEL_TRACES_EXPORTER")
	if tracesExporter == "" {
		tracesExporter = "otlp" // Default to OTLP
	}

	if tracesExporter != "none" {
		var traceExporter sdktrace.SpanExporter

		switch tracesExporter {
		case "console":
			traceExporter, err = stdouttrace.New()
		case "otlp":
			traceExporter, err = otlptracehttp.New(ctx)
		default:
			return shutdown, errors.New("unsupported OTEL_TRACES_EXPORTER value: " + tracesExporter)
		}

		if err != nil {
			return shutdown, err
		}

		tracerProvider := sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(traceExporter),
			sdktrace.WithResource(res),
		)
		shutdownFuncs = append(shutdownFuncs, tracerProvider.Shutdown)
		otel.SetTracerProvider(tracerProvider)
	}

	// Set up the metric exporter and provider.
	// Check OTEL_METRICS_EXPORTER env var (supports "console", "otlp", "none").
	metricsExporter := os.Getenv("OTEL_METRICS_EXPORTER")
	if metricsExporter == "" {
		metricsExporter = "otlp" // Default to OTLP
	}

	if metricsExporter != "none" {
		var metricExporter metric.Exporter

		switch metricsExporter {
		case "console":
			metricExporter, err = stdoutmetric.New()
		case "otlp":
			metricExporter, err = otlpmetrichttp.New(ctx)
		default:
			return shutdown, errors.New("unsupported OTEL_METRICS_EXPORTER value: " + metricsExporter)
		}

		if err != nil {
			return shutdown, err
		}

		meterProvider := metric.NewMeterProvider(
			metric.WithReader(metric.NewPeriodicReader(metricExporter)),
			metric.WithResource(res),
		)
		shutdownFuncs = append(shutdownFuncs, meterProvider.Shutdown)
		otel.SetMeterProvider(meterProvider)
	}

	return shutdown, nil
}

// NewLogger creates a new slog.Logger with JSON output and trace context integration.
func NewLogger(w io.Writer) *slog.Logger {
	jsonHandler := slog.NewJSONHandler(w, nil)
	return slog.New(NewTraceHandler(jsonHandler))
}
