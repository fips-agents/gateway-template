// Package tracing provides optional OpenTelemetry instrumentation for the
// gateway. When OTEL_EXPORTER_OTLP_ENDPOINT is set, the gateway participates
// in distributed traces by creating a child span per request and injecting
// the updated trace context into forwarded headers. When unset, tracing is
// a no-op with zero overhead.
package tracing

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// Init initialises the OTel TracerProvider when OTEL_EXPORTER_OTLP_ENDPOINT
// is set. Returns a shutdown function that must be called on process exit to
// flush pending spans. When the endpoint is unset, Init is a no-op and the
// returned shutdown does nothing.
func Init(ctx context.Context, serviceName, serviceVersion string) (func(context.Context) error, error) {
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		return func(context.Context) error { return nil }, nil
	}

	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("tracing: failed to create OTLP exporter: %w", err)
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(serviceVersion),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("tracing: failed to create resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	slog.Info("tracing enabled",
		"endpoint", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		"service", serviceName,
	)

	return tp.Shutdown, nil
}

// Middleware creates a child span for each request and injects the updated
// trace context back into r.Header. Downstream handlers that call
// copyPropagationHeaders will automatically forward the gateway's span ID
// rather than the client's original — the gateway becomes a visible hop in
// the distributed trace.
//
// When no TracerProvider is configured (Init was not called or endpoint was
// empty), the global tracer is a no-op and this middleware adds no overhead.
func Middleware(next http.Handler) http.Handler {
	tracer := otel.Tracer("gateway-template")
	propagator := otel.GetTextMapPropagator()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx, span := tracer.Start(ctx, r.Method+" "+r.URL.Path,
			trace.WithSpanKind(trace.SpanKindServer),
		)

		span.SetAttributes(
			attribute.String("http.request.method", r.Method),
			attribute.String("url.path", r.URL.Path),
		)
		if reqID := r.Header.Get("X-Request-ID"); reqID != "" {
			span.SetAttributes(attribute.String("http.request.header.x_request_id", reqID))
		}
		if subject := r.Header.Get("X-Auth-Subject"); subject != "" {
			span.SetAttributes(attribute.String("enduser.id", subject))
		}
		if tenant := r.Header.Get("X-Tenant-ID"); tenant != "" {
			span.SetAttributes(attribute.String("tenant.id", tenant))
		}

		propagator.Inject(ctx, propagation.HeaderCarrier(r.Header))

		sw := &statusCapture{ResponseWriter: w}
		defer func() {
			span.SetAttributes(
				attribute.Int("http.response.status_code", sw.status),
			)
			if sw.status >= 500 {
				span.SetStatus(codes.Error, http.StatusText(sw.status))
			}
			span.End()
		}()

		next.ServeHTTP(sw, r.WithContext(ctx))
	})
}

type statusCapture struct {
	http.ResponseWriter
	status int
}

func (s *statusCapture) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusCapture) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusCapture) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
