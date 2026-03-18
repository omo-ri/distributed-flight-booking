package handler

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/omo-ri/coa-hw/hw3/flight-service/internal/repository"
	pb "github.com/omo-ri/coa-hw/hw3/flight-service/pb/flight"
)

// mockFlightService implements service.FlightService for testing.
type mockFlightService struct {
	searchFlightsFunc      func(ctx context.Context, origin, destination, date string) ([]repository.FlightRow, error)
	getFlightFunc          func(ctx context.Context, id string) (repository.FlightRow, error)
	reserveSeatsFunc       func(ctx context.Context, flightID string, seatCount int32, bookingID string) (repository.SeatReservationRow, error)
	releaseReservationFunc func(ctx context.Context, bookingID string) (repository.SeatReservationRow, error)
}

func (m *mockFlightService) SearchFlights(ctx context.Context, origin, destination, date string) ([]repository.FlightRow, error) {
	return m.searchFlightsFunc(ctx, origin, destination, date)
}

func (m *mockFlightService) GetFlight(ctx context.Context, id string) (repository.FlightRow, error) {
	return m.getFlightFunc(ctx, id)
}

func (m *mockFlightService) ReserveSeats(ctx context.Context, flightID string, seatCount int32, bookingID string) (repository.SeatReservationRow, error) {
	return m.reserveSeatsFunc(ctx, flightID, seatCount, bookingID)
}

func (m *mockFlightService) ReleaseReservation(ctx context.Context, bookingID string) (repository.SeatReservationRow, error) {
	return m.releaseReservationFunc(ctx, bookingID)
}

var sampleFlight = repository.FlightRow{
	ID:             "550e8400-e29b-41d4-a716-446655440000",
	FlightNumber:   "SU1234",
	Airline:        "Aeroflot",
	Origin:         "SVO",
	Destination:    "LED",
	DepartureTime:  time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC),
	ArrivalTime:    time.Date(2026, 4, 1, 11, 30, 0, 0, time.UTC),
	TotalSeats:     180,
	AvailableSeats: 180,
	Price:          15000,
	Status:         "SCHEDULED",
}

var sampleReservation = repository.SeatReservationRow{
	ID:        "660e8400-e29b-41d4-a716-446655440001",
	FlightID:  "550e8400-e29b-41d4-a716-446655440000",
	BookingID: "770e8400-e29b-41d4-a716-446655440002",
	SeatCount: 2,
	Status:    "ACTIVE",
	CreatedAt: time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC),
}

// --- SearchFlights ---

func TestSearchFlights_Success(t *testing.T) {
	h := NewFlightHandler(&mockFlightService{
		searchFlightsFunc: func(_ context.Context, origin, dest, date string) ([]repository.FlightRow, error) {
			if origin != "SVO" || dest != "LED" || date != "2026-04-01" {
				t.Fatalf("unexpected args: %s %s %s", origin, dest, date)
			}
			return []repository.FlightRow{sampleFlight}, nil
		},
	})

	resp, err := h.SearchFlights(context.Background(), &pb.SearchFlightsRequest{
		Origin: "SVO", Destination: "LED", Date: "2026-04-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Flights) != 1 {
		t.Fatalf("expected 1 flight, got %d", len(resp.Flights))
	}
	if resp.Flights[0].FlightNumber != "SU1234" {
		t.Fatalf("expected SU1234, got %s", resp.Flights[0].FlightNumber)
	}
}

func TestSearchFlights_MissingOrigin(t *testing.T) {
	h := NewFlightHandler(&mockFlightService{})
	_, err := h.SearchFlights(context.Background(), &pb.SearchFlightsRequest{Destination: "LED"})
	assertGRPCCode(t, err, codes.InvalidArgument)
}

// --- GetFlight ---

func TestGetFlight_Success(t *testing.T) {
	h := NewFlightHandler(&mockFlightService{
		getFlightFunc: func(_ context.Context, id string) (repository.FlightRow, error) {
			return sampleFlight, nil
		},
	})

	resp, err := h.GetFlight(context.Background(), &pb.GetFlightRequest{Id: sampleFlight.ID})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Flight.Id != sampleFlight.ID {
		t.Fatalf("expected %s, got %s", sampleFlight.ID, resp.Flight.Id)
	}
}

func TestGetFlight_NotFound(t *testing.T) {
	h := NewFlightHandler(&mockFlightService{
		getFlightFunc: func(_ context.Context, id string) (repository.FlightRow, error) {
			return repository.FlightRow{}, repository.ErrNotFound
		},
	})

	_, err := h.GetFlight(context.Background(), &pb.GetFlightRequest{Id: "nonexistent"})
	assertGRPCCode(t, err, codes.NotFound)
}

func TestGetFlight_EmptyID(t *testing.T) {
	h := NewFlightHandler(&mockFlightService{})
	_, err := h.GetFlight(context.Background(), &pb.GetFlightRequest{})
	assertGRPCCode(t, err, codes.InvalidArgument)
}

// --- ReserveSeats ---

func TestReserveSeats_Success(t *testing.T) {
	h := NewFlightHandler(&mockFlightService{
		reserveSeatsFunc: func(_ context.Context, flightID string, seatCount int32, bookingID string) (repository.SeatReservationRow, error) {
			return sampleReservation, nil
		},
	})

	resp, err := h.ReserveSeats(context.Background(), &pb.ReserveSeatsRequest{
		FlightId: sampleFlight.ID, SeatCount: 2, BookingId: sampleReservation.BookingID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Reservation.SeatCount != 2 {
		t.Fatalf("expected 2, got %d", resp.Reservation.SeatCount)
	}
}

func TestReserveSeats_InsufficientSeats(t *testing.T) {
	h := NewFlightHandler(&mockFlightService{
		reserveSeatsFunc: func(_ context.Context, _ string, _ int32, _ string) (repository.SeatReservationRow, error) {
			return repository.SeatReservationRow{}, repository.ErrInsufficientSeats
		},
	})

	_, err := h.ReserveSeats(context.Background(), &pb.ReserveSeatsRequest{
		FlightId: sampleFlight.ID, SeatCount: 999, BookingId: sampleReservation.BookingID,
	})
	assertGRPCCode(t, err, codes.ResourceExhausted)
}

func TestReserveSeats_InvalidArgs(t *testing.T) {
	h := NewFlightHandler(&mockFlightService{})

	_, err := h.ReserveSeats(context.Background(), &pb.ReserveSeatsRequest{SeatCount: 1, BookingId: "abc"})
	assertGRPCCode(t, err, codes.InvalidArgument)

	_, err = h.ReserveSeats(context.Background(), &pb.ReserveSeatsRequest{FlightId: "abc", BookingId: "abc", SeatCount: 0})
	assertGRPCCode(t, err, codes.InvalidArgument)
}

// --- ReleaseReservation ---

func TestReleaseReservation_Success(t *testing.T) {
	released := sampleReservation
	released.Status = "RELEASED"
	h := NewFlightHandler(&mockFlightService{
		releaseReservationFunc: func(_ context.Context, bookingID string) (repository.SeatReservationRow, error) {
			return released, nil
		},
	})

	resp, err := h.ReleaseReservation(context.Background(), &pb.ReleaseReservationRequest{
		BookingId: sampleReservation.BookingID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Reservation.Status != pb.ReservationStatus_RESERVATION_STATUS_RELEASED {
		t.Fatalf("expected RELEASED, got %v", resp.Reservation.Status)
	}
}

func TestReleaseReservation_NotFound(t *testing.T) {
	h := NewFlightHandler(&mockFlightService{
		releaseReservationFunc: func(_ context.Context, _ string) (repository.SeatReservationRow, error) {
			return repository.SeatReservationRow{}, repository.ErrNotFound
		},
	})

	_, err := h.ReleaseReservation(context.Background(), &pb.ReleaseReservationRequest{BookingId: "nonexistent"})
	assertGRPCCode(t, err, codes.NotFound)
}

// --- helpers ---

func assertGRPCCode(t *testing.T, err error, expected codes.Code) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got: %v", err)
	}
	if st.Code() != expected {
		t.Fatalf("expected code %v, got %v: %s", expected, st.Code(), st.Message())
	}
}
