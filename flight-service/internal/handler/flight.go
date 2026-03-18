package handler

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/omo-ri/coa-hw/hw3/flight-service/internal/repository"
	"github.com/omo-ri/coa-hw/hw3/flight-service/internal/service"
	pb "github.com/omo-ri/coa-hw/hw3/flight-service/pb/flight"
)

type FlightHandler struct {
	pb.UnimplementedFlightServiceServer
	svc service.FlightService
}

func NewFlightHandler(svc service.FlightService) *FlightHandler {
	return &FlightHandler{svc: svc}
}

func (h *FlightHandler) SearchFlights(ctx context.Context, req *pb.SearchFlightsRequest) (*pb.SearchFlightsResponse, error) {
	if req.GetOrigin() == "" || req.GetDestination() == "" {
		return nil, status.Error(codes.InvalidArgument, "origin and destination are required")
	}

	rows, err := h.svc.SearchFlights(ctx, req.GetOrigin(), req.GetDestination(), req.GetDate())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "search flights: %v", err)
	}

	flights := make([]*pb.Flight, 0, len(rows))
	for _, r := range rows {
		flights = append(flights, flightRowToProto(r))
	}
	return &pb.SearchFlightsResponse{Flights: flights}, nil
}

func (h *FlightHandler) GetFlight(ctx context.Context, req *pb.GetFlightRequest) (*pb.GetFlightResponse, error) {
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	row, err := h.svc.GetFlight(ctx, req.GetId())
	if errors.Is(err, repository.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "flight not found")
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get flight: %v", err)
	}

	return &pb.GetFlightResponse{Flight: flightRowToProto(row)}, nil
}

func (h *FlightHandler) ReserveSeats(ctx context.Context, req *pb.ReserveSeatsRequest) (*pb.ReserveSeatsResponse, error) {
	if req.GetFlightId() == "" || req.GetBookingId() == "" {
		return nil, status.Error(codes.InvalidArgument, "flight_id and booking_id are required")
	}
	if req.GetSeatCount() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "seat_count must be positive")
	}

	row, err := h.svc.ReserveSeats(ctx, req.GetFlightId(), req.GetSeatCount(), req.GetBookingId())
	if errors.Is(err, repository.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "flight not found")
	}
	if errors.Is(err, repository.ErrInsufficientSeats) {
		return nil, status.Error(codes.ResourceExhausted, "insufficient seats")
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "reserve seats: %v", err)
	}

	return &pb.ReserveSeatsResponse{Reservation: reservationRowToProto(row)}, nil
}

func (h *FlightHandler) ReleaseReservation(ctx context.Context, req *pb.ReleaseReservationRequest) (*pb.ReleaseReservationResponse, error) {
	if req.GetBookingId() == "" {
		return nil, status.Error(codes.InvalidArgument, "booking_id is required")
	}

	row, err := h.svc.ReleaseReservation(ctx, req.GetBookingId())
	if errors.Is(err, repository.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "active reservation not found")
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "release reservation: %v", err)
	}

	return &pb.ReleaseReservationResponse{Reservation: reservationRowToProto(row)}, nil
}

func flightRowToProto(r repository.FlightRow) *pb.Flight {
	return &pb.Flight{
		Id:             r.ID,
		FlightNumber:   r.FlightNumber,
		Airline:        r.Airline,
		Origin:         r.Origin,
		Destination:    r.Destination,
		DepartureTime:  timestamppb.New(r.DepartureTime),
		ArrivalTime:    timestamppb.New(r.ArrivalTime),
		TotalSeats:     r.TotalSeats,
		AvailableSeats: r.AvailableSeats,
		Price:          r.Price,
		Status:         flightStatusToProto(r.Status),
	}
}

func reservationRowToProto(r repository.SeatReservationRow) *pb.SeatReservation {
	return &pb.SeatReservation{
		Id:        r.ID,
		FlightId:  r.FlightID,
		BookingId: r.BookingID,
		SeatCount: r.SeatCount,
		Status:    reservationStatusToProto(r.Status),
		CreatedAt: timestamppb.New(r.CreatedAt),
	}
}

var flightStatusMap = map[string]pb.FlightStatus{
	"SCHEDULED": pb.FlightStatus_FLIGHT_STATUS_SCHEDULED,
	"DEPARTED":  pb.FlightStatus_FLIGHT_STATUS_DEPARTED,
	"CANCELLED": pb.FlightStatus_FLIGHT_STATUS_CANCELLED,
	"COMPLETED": pb.FlightStatus_FLIGHT_STATUS_COMPLETED,
}

func flightStatusToProto(s string) pb.FlightStatus {
	if v, ok := flightStatusMap[s]; ok {
		return v
	}
	return pb.FlightStatus_FLIGHT_STATUS_UNSPECIFIED
}

var reservationStatusMap = map[string]pb.ReservationStatus{
	"ACTIVE":   pb.ReservationStatus_RESERVATION_STATUS_ACTIVE,
	"RELEASED": pb.ReservationStatus_RESERVATION_STATUS_RELEASED,
	"EXPIRED":  pb.ReservationStatus_RESERVATION_STATUS_EXPIRED,
}

func reservationStatusToProto(s string) pb.ReservationStatus {
	if v, ok := reservationStatusMap[s]; ok {
		return v
	}
	return pb.ReservationStatus_RESERVATION_STATUS_UNSPECIFIED
}
