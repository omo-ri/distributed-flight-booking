package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/omo-ri/distributed-flight-booking/booking-service/internal/grpcclient"
	"github.com/omo-ri/distributed-flight-booking/booking-service/internal/repository"
	pb "github.com/omo-ri/distributed-flight-booking/flight-service/pb/flight"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	ErrNotFound          = errors.New("not found")
	ErrInsufficientSeats = errors.New("insufficient seats")
	ErrAlreadyCancelled  = errors.New("booking already cancelled")
	ErrFlightNotFound    = errors.New("flight not found")
)

type FlightInfo struct {
	ID             string
	FlightNumber   string
	Airline        string
	Origin         string
	Destination    string
	DepartureTime  string
	ArrivalTime    string
	TotalSeats     int32
	AvailableSeats int32
	Price          int64
	Status         string
}

type BookingService interface {
	SearchFlights(ctx context.Context, origin, destination, date string) ([]FlightInfo, error)
	GetFlight(ctx context.Context, id string) (FlightInfo, error)
	CreateBooking(ctx context.Context, req CreateBookingInput) (repository.BookingRow, error)
	GetBooking(ctx context.Context, id string) (repository.BookingRow, error)
	ListBookings(ctx context.Context, userID string) ([]repository.BookingRow, error)
	CancelBooking(ctx context.Context, id string) (repository.BookingRow, error)
}

type CreateBookingInput struct {
	UserID         string
	FlightID       string
	PassengerName  string
	PassengerEmail string
	SeatCount      int32
}

type bookingService struct {
	repo    *repository.BookingRepo
	flights *grpcclient.FlightClient
	log     *slog.Logger
}

func NewBookingService(repo *repository.BookingRepo, flights *grpcclient.FlightClient, log *slog.Logger) BookingService {
	return &bookingService{repo: repo, flights: flights, log: log.With("layer", "service")}
}

func (s *bookingService) SearchFlights(ctx context.Context, origin, destination, date string) ([]FlightInfo, error) {
	s.log.Info("gRPC SearchFlights", "origin", origin, "destination", destination, "date", date)

	resp, err := s.flights.SearchFlights(ctx, origin, destination, date)
	if err != nil {
		s.log.Error("gRPC SearchFlights failed", "error", err)
		return nil, fmt.Errorf("search flights: %w", err)
	}

	flights := make([]FlightInfo, 0, len(resp.Flights))
	for _, f := range resp.Flights {
		flights = append(flights, protoToFlightInfo(f))
	}
	s.log.Info("gRPC SearchFlights OK", "count", len(flights))
	return flights, nil
}

func (s *bookingService) GetFlight(ctx context.Context, id string) (FlightInfo, error) {
	s.log.Info("gRPC GetFlight", "flight_id", id)

	resp, err := s.flights.GetFlight(ctx, id)
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			s.log.Warn("gRPC GetFlight: not found", "flight_id", id)
			return FlightInfo{}, ErrFlightNotFound
		}
		s.log.Error("gRPC GetFlight failed", "flight_id", id, "error", err)
		return FlightInfo{}, fmt.Errorf("get flight: %w", err)
	}

	s.log.Info("gRPC GetFlight OK", "flight_id", id, "flight_number", resp.Flight.FlightNumber)
	return protoToFlightInfo(resp.Flight), nil
}

// CreateBooking: GetFlight → generate UUID → ReserveSeats → write DB
func (s *bookingService) CreateBooking(ctx context.Context, req CreateBookingInput) (repository.BookingRow, error) {
	// 1. Get flight info (price)
	s.log.Info("gRPC GetFlight for booking", "flight_id", req.FlightID)
	flightResp, err := s.flights.GetFlight(ctx, req.FlightID)
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			s.log.Warn("flight not found", "flight_id", req.FlightID)
			return repository.BookingRow{}, ErrFlightNotFound
		}
		s.log.Error("gRPC GetFlight failed", "flight_id", req.FlightID, "error", err)
		return repository.BookingRow{}, fmt.Errorf("get flight: %w", err)
	}
	flight := flightResp.Flight
	s.log.Info("flight retrieved",
		"flight_id", req.FlightID,
		"price", flight.Price,
		"available_seats", flight.AvailableSeats,
	)

	// 2. Generate booking ID upfront for idempotent ReserveSeats
	bookingID := uuid.New().String()

	// 3. Reserve seats — if this fails, no DB record is created
	s.log.Info("gRPC ReserveSeats",
		"booking_id", bookingID,
		"flight_id", req.FlightID,
		"seat_count", req.SeatCount,
	)
	_, err = s.flights.ReserveSeats(ctx, req.FlightID, req.SeatCount, bookingID)
	if err != nil {
		if st, ok := status.FromError(err); ok {
			switch st.Code() {
			case codes.NotFound:
				s.log.Warn("gRPC ReserveSeats: flight not found", "flight_id", req.FlightID)
				return repository.BookingRow{}, ErrFlightNotFound
			case codes.ResourceExhausted:
				s.log.Warn("gRPC ReserveSeats: insufficient seats",
					"flight_id", req.FlightID,
					"requested", req.SeatCount,
					"available", flight.AvailableSeats,
				)
				return repository.BookingRow{}, ErrInsufficientSeats
			}
		}
		s.log.Error("gRPC ReserveSeats failed", "booking_id", bookingID, "error", err)
		return repository.BookingRow{}, fmt.Errorf("reserve seats: %w", err)
	}
	s.log.Info("gRPC ReserveSeats OK", "booking_id", bookingID)

	// 4. Seats reserved — now write booking to DB
	totalPrice := int64(req.SeatCount) * flight.Price
	s.log.Info("writing booking to DB",
		"booking_id", bookingID,
		"total_price", totalPrice,
	)
	bookingRow, err := s.repo.Create(ctx, repository.BookingRow{
		ID:             bookingID,
		UserID:         req.UserID,
		FlightID:       req.FlightID,
		PassengerName:  req.PassengerName,
		PassengerEmail: req.PassengerEmail,
		SeatCount:      req.SeatCount,
		TotalPrice:     totalPrice,
	})
	if err != nil {
		s.log.Error("DB create booking failed", "booking_id", bookingID, "error", err)
		return repository.BookingRow{}, fmt.Errorf("create booking: %w", err)
	}

	s.log.Info("booking created successfully", "booking_id", bookingRow.ID, "status", bookingRow.Status)
	return bookingRow, nil
}

func (s *bookingService) GetBooking(ctx context.Context, id string) (repository.BookingRow, error) {
	s.log.Info("DB GetBooking", "booking_id", id)

	b, err := s.repo.GetByID(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		s.log.Warn("booking not found in DB", "booking_id", id)
		return b, ErrNotFound
	}
	if err != nil {
		s.log.Error("DB GetBooking failed", "booking_id", id, "error", err)
	}
	return b, err
}

func (s *bookingService) ListBookings(ctx context.Context, userID string) ([]repository.BookingRow, error) {
	s.log.Info("DB ListBookings", "user_id", userID)

	bookings, err := s.repo.ListByUserID(ctx, userID)
	if err != nil {
		s.log.Error("DB ListBookings failed", "user_id", userID, "error", err)
		return nil, err
	}
	s.log.Info("DB ListBookings OK", "user_id", userID, "count", len(bookings))
	return bookings, nil
}

func (s *bookingService) CancelBooking(ctx context.Context, id string) (repository.BookingRow, error) {
	s.log.Info("DB GetBooking for cancel", "booking_id", id)

	b, err := s.repo.GetByID(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		s.log.Warn("cancel: booking not found", "booking_id", id)
		return b, ErrNotFound
	}
	if err != nil {
		s.log.Error("cancel: DB GetBooking failed", "booking_id", id, "error", err)
		return b, err
	}
	if b.Status == "CANCELLED" {
		s.log.Warn("cancel: already cancelled", "booking_id", id)
		return b, ErrAlreadyCancelled
	}

	// Release seats
	s.log.Info("gRPC ReleaseReservation", "booking_id", b.ID)
	_, err = s.flights.ReleaseReservation(ctx, b.ID)
	if err != nil {
		s.log.Error("gRPC ReleaseReservation failed (best-effort)", "booking_id", b.ID, "error", err)
	} else {
		s.log.Info("gRPC ReleaseReservation OK", "booking_id", b.ID)
	}

	// Update status
	s.log.Info("DB UpdateStatus → CANCELLED", "booking_id", id)
	if err := s.repo.UpdateStatus(ctx, id, "CANCELLED"); err != nil {
		s.log.Error("DB UpdateStatus failed", "booking_id", id, "error", err)
		return b, fmt.Errorf("update status: %w", err)
	}

	s.log.Info("booking cancelled successfully", "booking_id", id)
	b.Status = "CANCELLED"
	return b, nil
}

func protoToFlightInfo(f *pb.Flight) FlightInfo {
	var depTime, arrTime string
	if f.DepartureTime != nil {
		depTime = f.DepartureTime.AsTime().Format("2006-01-02T15:04:05Z07:00")
	}
	if f.ArrivalTime != nil {
		arrTime = f.ArrivalTime.AsTime().Format("2006-01-02T15:04:05Z07:00")
	}
	statusMap := map[pb.FlightStatus]string{
		pb.FlightStatus_FLIGHT_STATUS_SCHEDULED: "SCHEDULED",
		pb.FlightStatus_FLIGHT_STATUS_DEPARTED:  "DEPARTED",
		pb.FlightStatus_FLIGHT_STATUS_CANCELLED: "CANCELLED",
		pb.FlightStatus_FLIGHT_STATUS_COMPLETED: "COMPLETED",
	}
	return FlightInfo{
		ID:             f.Id,
		FlightNumber:   f.FlightNumber,
		Airline:        f.Airline,
		Origin:         f.Origin,
		Destination:    f.Destination,
		DepartureTime:  depTime,
		ArrivalTime:    arrTime,
		TotalSeats:     f.TotalSeats,
		AvailableSeats: f.AvailableSeats,
		Price:          f.Price,
		Status:         statusMap[f.Status],
	}
}
