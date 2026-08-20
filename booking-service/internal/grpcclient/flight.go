package grpcclient

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/omo-ri/distributed-flight-booking/booking-service/internal/circuitbreaker"
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

// withAuth attaches the API key to the outgoing gRPC metadata.
func (c *FlightClient) withAuth(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "x-api-key", c.apiKey)
}

func (c *FlightClient) Close() error {
	return c.conn.Close()
}

// withCircuitBreaker wraps fn with circuit breaker check, then retry with exponential backoff.
func withCircuitBreaker[T any](ctx context.Context, cb *circuitbreaker.CircuitBreaker, method string, fn func() (T, error)) (T, error) {
	// Circuit breaker check
	if err := cb.Allow(); err != nil {
		var zero T
		log.Printf("[CIRCUIT-BREAKER] %s blocked — circuit is OPEN", method)
		return zero, err
	}

	result, err := retry(ctx, method, fn)
	if err != nil {
		cb.RecordFailure()
	} else {
		cb.RecordSuccess()
	}
	return result, err
}

// retry executes fn with exponential backoff. Only retries on UNAVAILABLE and DEADLINE_EXCEEDED.
func retry[T any](ctx context.Context, method string, fn func() (T, error)) (T, error) {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		result, err := fn()
		if err == nil {
			if attempt > 0 {
				log.Printf("[RETRY] %s succeeded on attempt %d", method, attempt+1)
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
			log.Printf("[RETRY] %s attempt %d failed (%s), retrying in %v", method, attempt+1, st.Code(), backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				var zero T
				return zero, ctx.Err()
			}
		}
	}

	var zero T
	return zero, fmt.Errorf("%s: all %d retries exhausted: %w", method, maxRetries, lastErr)
}

func (c *FlightClient) SearchFlights(ctx context.Context, origin, destination, date string) (*pb.SearchFlightsResponse, error) {
	return withCircuitBreaker(ctx, c.cb, "SearchFlights", func() (*pb.SearchFlightsResponse, error) {
		return c.client.SearchFlights(c.withAuth(ctx), &pb.SearchFlightsRequest{
			Origin:      origin,
			Destination: destination,
			Date:        date,
		})
	})
}

func (c *FlightClient) GetFlight(ctx context.Context, id string) (*pb.GetFlightResponse, error) {
	return withCircuitBreaker(ctx, c.cb, "GetFlight", func() (*pb.GetFlightResponse, error) {
		return c.client.GetFlight(c.withAuth(ctx), &pb.GetFlightRequest{Id: id})
	})
}

func (c *FlightClient) ReserveSeats(ctx context.Context, flightID string, seatCount int32, bookingID string) (*pb.ReserveSeatsResponse, error) {
	return withCircuitBreaker(ctx, c.cb, "ReserveSeats", func() (*pb.ReserveSeatsResponse, error) {
		return c.client.ReserveSeats(c.withAuth(ctx), &pb.ReserveSeatsRequest{
			FlightId:  flightID,
			SeatCount: seatCount,
			BookingId: bookingID,
		})
	})
}

func (c *FlightClient) ReleaseReservation(ctx context.Context, bookingID string) (*pb.ReleaseReservationResponse, error) {
	return withCircuitBreaker(ctx, c.cb, "ReleaseReservation", func() (*pb.ReleaseReservationResponse, error) {
		return c.client.ReleaseReservation(c.withAuth(ctx), &pb.ReleaseReservationRequest{BookingId: bookingID})
	})
}
