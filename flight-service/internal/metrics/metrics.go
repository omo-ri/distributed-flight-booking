package metrics

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

const (
	serviceLabel = "flight-service"
	methodLabel  = "grpc"
)

var (
	RequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total number of RPC requests (gRPC is exposed under the http_* family for cross-service uniformity).",
	}, []string{"service", "method", "endpoint", "status"})

	RequestErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_request_errors_total",
		Help: "Total number of RPC requests that returned a non-OK gRPC status.",
	}, []string{"service", "method", "endpoint", "error_type"})

	RequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "RPC handler duration in seconds.",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
	}, []string{"service", "method", "endpoint"})
)

// UnaryServerInterceptor records metrics for every unary gRPC call.
// `endpoint` is the full gRPC method (e.g. /flight.FlightService/GetFlight).
func UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)

		code := status.Code(err)
		endpoint := info.FullMethod

		RequestsTotal.WithLabelValues(serviceLabel, methodLabel, endpoint, code.String()).Inc()
		RequestDuration.WithLabelValues(serviceLabel, methodLabel, endpoint).Observe(time.Since(start).Seconds())

		if err != nil {
			RequestErrorsTotal.WithLabelValues(serviceLabel, methodLabel, endpoint, code.String()).Inc()
		}

		return resp, err
	}
}
