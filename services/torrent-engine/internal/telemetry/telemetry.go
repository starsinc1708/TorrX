package telemetry

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Init configures the global OpenTelemetry trace provider.
// If OTEL_EXPORTER_OTLP_ENDPOINT is not set, tracing is disabled and a noop shutdown is returned.
// Sample rate is controlled by OTEL_TRACE_SAMPLE_RATE (0.0–1.0, default 0.1 = 10%).
func Init(ctx context.Context, serviceName string) (shutdown func(context.Context) error, err error) {
	endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	if endpoint == "" {
		return func(context.Context) error { return nil }, nil
	}

	initCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	exporter, err := otlptracehttp.New(initCtx,
		otlptracehttp.WithEndpoint(strings.TrimPrefix(strings.TrimPrefix(endpoint, "http://"), "https://")),
		otlptracehttp.WithInsecure(),
		otlptracehttp.WithTimeout(3*time.Second),
		otlptracehttp.WithRetry(otlptracehttp.RetryConfig{Enabled: false}),
	)
	if err != nil {
		// Non-fatal: service starts without tracing.
		return func(context.Context) error { return nil }, nil
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(serviceName)),
	)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(parseSampleRate()))),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tp.Shutdown, nil
}

// parseSampleRate reads OTEL_TRACE_SAMPLE_RATE and returns a float64 in [0,1].
// Defaults to 0.1 (10%) if unset or invalid.
func parseSampleRate() float64 {
	raw := strings.TrimSpace(os.Getenv("OTEL_TRACE_SAMPLE_RATE"))
	if raw == "" {
		return 0.1
	}
	rate, err := strconv.ParseFloat(raw, 64)
	if err != nil || rate < 0 || rate > 1 {
		return 0.1
	}
	return rate
}
