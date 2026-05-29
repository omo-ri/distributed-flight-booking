package metrics

import (
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const serviceLabel = "booking-service"

var (
	RequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total number of HTTP requests.",
	}, []string{"service", "method", "endpoint", "status"})

	RequestErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_request_errors_total",
		Help: "Total number of HTTP requests that resulted in 4xx or 5xx.",
	}, []string{"service", "method", "endpoint", "error_type"})

	RequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request duration in seconds.",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
	}, []string{"service", "method", "endpoint"})
)

// Middleware records metrics for every HTTP request handled by Echo.
// `endpoint` is the route pattern (e.g. /bookings/:id), not the raw path,
// to keep label cardinality bounded.
func Middleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			start := time.Now()
			err := next(c)
			if err != nil {
				c.Error(err)
			}

			endpoint := c.Path()
			if endpoint == "" {
				endpoint = "unknown"
			}
			method := c.Request().Method
			status := c.Response().Status
			statusStr := strconv.Itoa(status)

			RequestsTotal.WithLabelValues(serviceLabel, method, endpoint, statusStr).Inc()
			RequestDuration.WithLabelValues(serviceLabel, method, endpoint).Observe(time.Since(start).Seconds())

			switch {
			case status >= 500:
				RequestErrorsTotal.WithLabelValues(serviceLabel, method, endpoint, "server_error").Inc()
			case status >= 400:
				RequestErrorsTotal.WithLabelValues(serviceLabel, method, endpoint, "client_error").Inc()
			}

			return nil
		}
	}
}
