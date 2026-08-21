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

	// CacheOperationsTotal 是缓存的唯一可观测出口。
	//
	// 日志回答「这一条请求命中了没有」（汇总行上的 cache 字段），指标回答
	// 「这一档的命中率是多少」——压测时 LOG_LEVEL=warn，日志本来就不落盘，
	// 而「这个吞吐是不是全靠缓存撑的」只能由指标回答。
	//
	// 三个标签的取值集合都有上界：cache ∈ {flight, search}、op ∈ {get, set, del}、
	// result ∈ {hit, miss, ok, error}。error 目前只有写路径会产出——读路径把
	// 所有 err 都当 MISS（redis.go 的一条独立缺陷），修掉后 get 也会用上它。
	CacheOperationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "flight_cache_operations_total",
		Help: "Total number of cache operations by cache, operation and result.",
	}, []string{"cache", "op", "result"})
)

// 缓存指标的标签取值。写成常量而不是字面量：标签集合有上界是这些指标能存在的前提。
const (
	CacheFlight = "flight"
	CacheSearch = "search"

	OpGet = "get"
	OpSet = "set"
	OpDel = "del"

	ResultHit   = "hit"
	ResultMiss  = "miss"
	ResultOK    = "ok"
	ResultError = "error"
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
