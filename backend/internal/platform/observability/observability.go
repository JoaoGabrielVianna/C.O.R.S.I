// Package observability wires OpenTelemetry tracing. Disabled by default so
// the binary boots without an OTLP collector; flip OTEL_ENABLED=true plus
// OTEL_EXPORTER_OTLP_ENDPOINT to ship traces.
package observability

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

type Config struct {
	ServiceName  string `env:"OTEL_SERVICE_NAME" envDefault:"corsi"`
	OTLPEndpoint string `env:"OTEL_EXPORTER_OTLP_ENDPOINT"`
	Enabled      bool   `env:"OTEL_ENABLED" envDefault:"false"`
}

type ShutdownFunc func(context.Context) error

func Setup(ctx context.Context, cfg Config) (ShutdownFunc, error) {
	if !cfg.Enabled {
		slog.Info("observability disabled")
		return func(context.Context) error { return nil }, nil
	}

	exp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}
	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(cfg.ServiceName)),
	)
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	slog.Info("observability enabled", "endpoint", cfg.OTLPEndpoint, "service", cfg.ServiceName)
	return tp.Shutdown, nil
}
