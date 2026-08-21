package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/omo-ri/distributed-flight-booking/booking-service/internal/grpcclient"
	"github.com/omo-ri/distributed-flight-booking/booking-service/internal/logctx"
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

// bookingService 不持有 logger——正常路径上它一行日志都不打（CLAUDE.md § 4）。
// 需要出现在汇总行里的东西挂进 logctx，错误靠 %w 往上传，wrap 链本身就是调用路径。
type bookingService struct {
	repo    *repository.BookingRepo
	flights *grpcclient.FlightClient
}

func NewBookingService(repo *repository.BookingRepo, flights *grpcclient.FlightClient) BookingService {
	return &bookingService{repo: repo, flights: flights}
}

func (s *bookingService) SearchFlights(ctx context.Context, origin, destination, date string) ([]FlightInfo, error) {
	resp, err := s.flights.SearchFlights(ctx, origin, destination, date)
	if err != nil {
		return nil, fmt.Errorf("search flights: %w", err)
	}

	flights := make([]FlightInfo, 0, len(resp.Flights))
	for _, f := range resp.Flights {
		flights = append(flights, protoToFlightInfo(f))
	}
	return flights, nil
}

func (s *bookingService) GetFlight(ctx context.Context, id string) (FlightInfo, error) {
	resp, err := s.flights.GetFlight(ctx, id)
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			return FlightInfo{}, ErrFlightNotFound
		}
		return FlightInfo{}, fmt.Errorf("get flight: %w", err)
	}

	return protoToFlightInfo(resp.Flight), nil
}

// CreateBooking: GetFlight → generate UUID → ReserveSeats → write DB
func (s *bookingService) CreateBooking(ctx context.Context, req CreateBookingInput) (repository.BookingRow, error) {
	// 1. Get flight info (price)
	flightResp, err := s.flights.GetFlight(ctx, req.FlightID)
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			return repository.BookingRow{}, ErrFlightNotFound
		}
		return repository.BookingRow{}, fmt.Errorf("get flight: %w", err)
	}
	flight := flightResp.Flight

	// 2. Generate booking ID upfront for idempotent ReserveSeats
	bookingID := uuid.New().String()
	logctx.Add(ctx, logctx.KeyBookingID, bookingID)

	// 3. Reserve seats — if this fails, no DB record is created
	_, err = s.flights.ReserveSeats(ctx, req.FlightID, req.SeatCount, bookingID)
	if err != nil {
		if st, ok := status.FromError(err); ok {
			switch st.Code() {
			case codes.NotFound:
				return repository.BookingRow{}, ErrFlightNotFound
			case codes.ResourceExhausted:
				return repository.BookingRow{}, ErrInsufficientSeats
			}
		}
		return repository.BookingRow{}, fmt.Errorf("reserve seats: %w", err)
	}

	// 4. Seats reserved — now write booking to DB
	totalPrice := int64(req.SeatCount) * flight.Price
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
		return repository.BookingRow{}, fmt.Errorf("create booking: %w", err)
	}

	return bookingRow, nil
}

func (s *bookingService) GetBooking(ctx context.Context, id string) (repository.BookingRow, error) {
	b, err := s.repo.GetByID(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return b, ErrNotFound
	}
	return b, err
}

func (s *bookingService) ListBookings(ctx context.Context, userID string) ([]repository.BookingRow, error) {
	bookings, err := s.repo.ListByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	return bookings, nil
}

func (s *bookingService) CancelBooking(ctx context.Context, id string) (repository.BookingRow, error) {
	b, err := s.repo.GetByID(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return b, ErrNotFound
	}
	if err != nil {
		return b, err
	}
	if b.Status == "CANCELLED" {
		return b, ErrAlreadyCancelled
	}

	// Release seats。best-effort：释放失败不挡取消，但它是「异常但已自动处理」，
	// 要让这一条请求的汇总行升到 Warn，否则座位泄漏会静默发生。
	if _, err := s.flights.ReleaseReservation(ctx, b.ID); err != nil {
		logctx.Escalate(ctx, slog.LevelWarn)
		logctx.Add(ctx, logctx.KeyDegraded, "release_failed")
	}

	// Update status
	if err := s.repo.UpdateStatus(ctx, id, "CANCELLED"); err != nil {
		return b, fmt.Errorf("update status: %w", err)
	}

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
