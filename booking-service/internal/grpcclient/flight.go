package grpcclient

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/omo-ri/distributed-flight-booking/booking-service/internal/circuitbreaker"
	"github.com/omo-ri/distributed-flight-booking/booking-service/internal/logctx"
	pb "github.com/omo-ri/distributed-flight-booking/flight-service/pb/flight"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	maxRetries  = 3
	baseBackoff = 100 * time.Millisecond

	// traceIDMetadataKey 是 trace_id 跨服务的载体。接收端是
	// flight-service/internal/logging.TraceIDMetadataKey——两个服务是独立
	// module（除 pb 契约层外不共享代码），所以这个键名两边各写一份。
	// 改一边必须改另一边，否则两侧汇总行就串不起来了。
	traceIDMetadataKey = "x-trace-id"
)

// retryableCodes are the gRPC codes that should be retried.
var retryableCodes = map[codes.Code]bool{
	codes.Unavailable:      true,
	codes.DeadlineExceeded: true,
}

type FlightClient struct {
	conn   *grpc.ClientConn
	client pb.FlightServiceClient
	apiKey string
	cb     *circuitbreaker.CircuitBreaker
}

func NewFlightClient(addr, apiKey string, cb *circuitbreaker.CircuitBreaker) (*FlightClient, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("grpc dial %s: %w", addr, err)
	}
	return &FlightClient{
		conn:   conn,
		client: pb.NewFlightServiceClient(conn),
		apiKey: apiKey,
		cb:     cb,
	}, nil
}

// withMetadata attaches the API key and the trace id to the outgoing gRPC metadata.
//
// trace_id 走 metadata 是「一次跨服务调用在两边的日志里能串起来」的全部机制：
// 两侧各出一条汇总行，靠同一个 trace_id join。没有它，A 口径就退化成
// 两条互不相干的行。
func (c *FlightClient) withMetadata(ctx context.Context) context.Context {
	ctx = metadata.AppendToOutgoingContext(ctx, "x-api-key", c.apiKey)
	if traceID := logctx.TraceID(ctx); traceID != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, traceIDMetadataKey, traceID)
	}
	return ctx
}

func (c *FlightClient) Close() error {
	return c.conn.Close()
}

// withCircuitBreaker wraps fn with circuit breaker check, then retry with exponential backoff.
//
// 这里一行日志都不打：熔断状态、重试次数、下游错误码都挂进 logctx，
// 由 requestLogger 写进那一条汇总行——「这条 503 是熔断拒的、还是重试耗光的」
// 因此在同一行上一眼可分（CLAUDE.md § 4）。
func withCircuitBreaker[T any](ctx context.Context, cb *circuitbreaker.CircuitBreaker, method string, fn func() (T, error)) (T, error) {
	// Circuit breaker check
	if err := cb.Allow(); err != nil {
		var zero T
		logctx.Add(ctx, logctx.KeyCB, cbState(circuitbreaker.Open))
		return zero, err
	}
	// cb 字段只在非 closed 时挂：闭合是常态，每条都打 cb=closed 是纯噪音，
	// 反过来字段一出现就意味着这条请求走了非常态分支。
	if st := cb.State(); st != circuitbreaker.Closed {
		logctx.Add(ctx, logctx.KeyCB, cbState(st))
	}

	result, err := retry(ctx, method, fn)
	if err != nil {
		cb.RecordFailure()
		if st, ok := status.FromError(err); ok {
			logctx.Add(ctx, logctx.KeyDownstreamCode, st.Code().String())
		}
	} else {
		cb.RecordSuccess()
	}
	return result, err
}

func cbState(s circuitbreaker.State) string {
	return strings.ToLower(s.String())
}

// retry executes fn with exponential backoff. Only retries on UNAVAILABLE and DEADLINE_EXCEEDED.
func retry[T any](ctx context.Context, method string, fn func() (T, error)) (T, error) {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		result, err := fn()
		if err == nil {
			if attempt > 0 {
				// 重试后成功 = 异常但已自动处理，抬到 Warn（CLAUDE.md § 4）。
				logctx.Escalate(ctx, slog.LevelWarn)
				logctx.Add(ctx, logctx.KeyRetries, attempt, logctx.KeyDegraded, "retry_recovered")
			}
			return result, nil
		}

		st, ok := status.FromError(err)
		if !ok || !retryableCodes[st.Code()] {
			// Non-retryable error — return immediately
			return result, err
		}

		lastErr = err
		if attempt < maxRetries {
			backoff := baseBackoff << uint(attempt) // 100ms, 200ms, 400ms
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				var zero T
				return zero, ctx.Err()
			}
		}
	}

	logctx.Add(ctx, logctx.KeyRetries, maxRetries)
	var zero T
	return zero, fmt.Errorf("%s: all %d retries exhausted: %w", method, maxRetries, lastErr)
}

func (c *FlightClient) SearchFlights(ctx context.Context, origin, destination, date string) (*pb.SearchFlightsResponse, error) {
	return withCircuitBreaker(ctx, c.cb, "SearchFlights", func() (*pb.SearchFlightsResponse, error) {
		return c.client.SearchFlights(c.withMetadata(ctx), &pb.SearchFlightsRequest{
			Origin:      origin,
			Destination: destination,
			Date:        date,
		})
	})
}

func (c *FlightClient) GetFlight(ctx context.Context, id string) (*pb.GetFlightResponse, error) {
	return withCircuitBreaker(ctx, c.cb, "GetFlight", func() (*pb.GetFlightResponse, error) {
		return c.client.GetFlight(c.withMetadata(ctx), &pb.GetFlightRequest{Id: id})
	})
}

func (c *FlightClient) ReserveSeats(ctx context.Context, flightID string, seatCount int32, bookingID string) (*pb.ReserveSeatsResponse, error) {
	return withCircuitBreaker(ctx, c.cb, "ReserveSeats", func() (*pb.ReserveSeatsResponse, error) {
		return c.client.ReserveSeats(c.withMetadata(ctx), &pb.ReserveSeatsRequest{
			FlightId:  flightID,
			SeatCount: seatCount,
			BookingId: bookingID,
		})
	})
}

func (c *FlightClient) ReleaseReservation(ctx context.Context, bookingID string) (*pb.ReleaseReservationResponse, error) {
	return withCircuitBreaker(ctx, c.cb, "ReleaseReservation", func() (*pb.ReleaseReservationResponse, error) {
		return c.client.ReleaseReservation(c.withMetadata(ctx), &pb.ReleaseReservationRequest{BookingId: bookingID})
	})
}
