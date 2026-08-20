package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/omo-ri/distributed-flight-booking/booking-service/api"
	"github.com/omo-ri/distributed-flight-booking/booking-service/internal/circuitbreaker"
	"github.com/omo-ri/distributed-flight-booking/booking-service/internal/repository"
	"github.com/omo-ri/distributed-flight-booking/booking-service/internal/service"
)

// isCircuitOpen checks if the error is a circuit breaker open error and returns 503 if so.
func isCircuitOpen(c echo.Context, err error) bool {
	if errors.Is(err, circuitbreaker.ErrCircuitOpen) {
		_ = c.JSON(http.StatusServiceUnavailable, api.Error{Message: "service temporarily unavailable"})
		return true
	}
	return false
}

type BookingHandler struct {
	svc service.BookingService
	log *slog.Logger
}

func NewBookingHandler(svc service.BookingService, log *slog.Logger) *BookingHandler {
	return &BookingHandler{svc: svc, log: log.With("layer", "handler")}
}

// Ensure BookingHandler implements api.ServerInterface.
var _ api.ServerInterface = (*BookingHandler)(nil)

func (h *BookingHandler) SearchFlights(c echo.Context, params api.SearchFlightsParams) error {
	var date string
	if params.Date != nil {
		date = params.Date.Time.Format("2006-01-02")
	}

	h.log.Info("searching flights",
		"origin", params.Origin,
		"destination", params.Destination,
		"date", date,
	)

	flights, err := h.svc.SearchFlights(c.Request().Context(), params.Origin, params.Destination, date)
	if err != nil {
		if isCircuitOpen(c, err) {
			return nil
		}
		h.log.Error("search flights failed", "error", err)
		return c.JSON(http.StatusInternalServerError, api.Error{Message: err.Error()})
	}

	result := make([]api.Flight, 0, len(flights))
	for _, f := range flights {
		result = append(result, flightInfoToAPI(f))
	}

	h.log.Info("search flights completed", "count", len(result))
	return c.JSON(http.StatusOK, result)
}

func (h *BookingHandler) GetFlight(c echo.Context, id openapi_types.UUID) error {
	h.log.Info("getting flight", "flight_id", id)

	flight, err := h.svc.GetFlight(c.Request().Context(), id.String())
	if errors.Is(err, service.ErrFlightNotFound) {
		h.log.Warn("flight not found", "flight_id", id)
		return c.JSON(http.StatusNotFound, api.Error{Message: "flight not found"})
	}
	if err != nil {
		if isCircuitOpen(c, err) {
			return nil
		}
		h.log.Error("get flight failed", "flight_id", id, "error", err)
		return c.JSON(http.StatusInternalServerError, api.Error{Message: err.Error()})
	}

	return c.JSON(http.StatusOK, flightInfoToAPI(flight))
}

func (h *BookingHandler) CreateBooking(c echo.Context) error {
	var req api.CreateBookingJSONRequestBody
	if err := c.Bind(&req); err != nil {
		h.log.Warn("invalid request body", "error", err)
		return c.JSON(http.StatusBadRequest, api.Error{Message: "invalid request body"})
	}
	if req.SeatCount < 1 {
		h.log.Warn("invalid seat_count", "seat_count", req.SeatCount)
		return c.JSON(http.StatusBadRequest, api.Error{Message: "seat_count must be at least 1"})
	}

	h.log.Info("creating booking",
		"user_id", req.UserId,
		"flight_id", req.FlightId,
		"passenger", req.PassengerName,
		"seats", req.SeatCount,
	)

	booking, err := h.svc.CreateBooking(c.Request().Context(), service.CreateBookingInput{
		UserID:         req.UserId.String(),
		FlightID:       req.FlightId.String(),
		PassengerName:  req.PassengerName,
		PassengerEmail: req.PassengerEmail,
		SeatCount:      int32(req.SeatCount),
	})
	if errors.Is(err, service.ErrFlightNotFound) {
		h.log.Warn("booking failed: flight not found", "flight_id", req.FlightId)
		return c.JSON(http.StatusNotFound, api.Error{Message: "flight not found"})
	}
	if errors.Is(err, service.ErrInsufficientSeats) {
		h.log.Warn("booking failed: insufficient seats", "flight_id", req.FlightId, "seats", req.SeatCount)
		return c.JSON(http.StatusConflict, api.Error{Message: "insufficient seats"})
	}
	if err != nil {
		if isCircuitOpen(c, err) {
			return nil
		}
		h.log.Error("create booking failed", "error", err)
		return c.JSON(http.StatusInternalServerError, api.Error{Message: err.Error()})
	}

	h.log.Info("booking created",
		"booking_id", booking.ID,
		"total_price", booking.TotalPrice,
		"status", booking.Status,
	)
	return c.JSON(http.StatusCreated, bookingRowToAPI(booking))
}

func (h *BookingHandler) GetBooking(c echo.Context, id openapi_types.UUID) error {
	h.log.Info("getting booking", "booking_id", id)

	booking, err := h.svc.GetBooking(c.Request().Context(), id.String())
	if errors.Is(err, service.ErrNotFound) {
		h.log.Warn("booking not found", "booking_id", id)
		return c.JSON(http.StatusNotFound, api.Error{Message: "booking not found"})
	}
	if err != nil {
		h.log.Error("get booking failed", "booking_id", id, "error", err)
		return c.JSON(http.StatusInternalServerError, api.Error{Message: err.Error()})
	}

	return c.JSON(http.StatusOK, bookingRowToAPI(booking))
}

func (h *BookingHandler) ListBookings(c echo.Context, params api.ListBookingsParams) error {
	h.log.Info("listing bookings", "user_id", params.UserId)

	bookings, err := h.svc.ListBookings(c.Request().Context(), params.UserId.String())
	if err != nil {
		h.log.Error("list bookings failed", "user_id", params.UserId, "error", err)
		return c.JSON(http.StatusInternalServerError, api.Error{Message: err.Error()})
	}

	result := make([]api.Booking, 0, len(bookings))
	for _, b := range bookings {
		result = append(result, bookingRowToAPI(b))
	}

	h.log.Info("list bookings completed", "user_id", params.UserId, "count", len(result))
	return c.JSON(http.StatusOK, result)
}

func (h *BookingHandler) CancelBooking(c echo.Context, id openapi_types.UUID) error {
	h.log.Info("cancelling booking", "booking_id", id)

	booking, err := h.svc.CancelBooking(c.Request().Context(), id.String())
	if errors.Is(err, service.ErrNotFound) {
		h.log.Warn("cancel failed: booking not found", "booking_id", id)
		return c.JSON(http.StatusNotFound, api.Error{Message: "booking not found"})
	}
	if errors.Is(err, service.ErrAlreadyCancelled) {
		h.log.Warn("cancel failed: already cancelled", "booking_id", id)
		return c.JSON(http.StatusConflict, api.Error{Message: "booking already cancelled"})
	}
	if err != nil {
		if isCircuitOpen(c, err) {
			return nil
		}
		h.log.Error("cancel booking failed", "booking_id", id, "error", err)
		return c.JSON(http.StatusInternalServerError, api.Error{Message: err.Error()})
	}

	h.log.Info("booking cancelled", "booking_id", id)
	return c.JSON(http.StatusOK, bookingRowToAPI(booking))
}

// --- converters ---

func bookingRowToAPI(b repository.BookingRow) api.Booking {
	return api.Booking{
		Id:             uuid.MustParse(b.ID),
		UserId:         uuid.MustParse(b.UserID),
		FlightId:       uuid.MustParse(b.FlightID),
		PassengerName:  b.PassengerName,
		PassengerEmail: b.PassengerEmail,
		SeatCount:      int(b.SeatCount),
		TotalPrice:     int(b.TotalPrice),
		Status:         api.BookingStatus(b.Status),
		CreatedAt:      b.CreatedAt,
	}
}

func flightInfoToAPI(f service.FlightInfo) api.Flight {
	depTime, _ := time.Parse(time.RFC3339, f.DepartureTime)
	arrTime, _ := time.Parse(time.RFC3339, f.ArrivalTime)
	return api.Flight{
		Id:             uuid.MustParse(f.ID),
		FlightNumber:   f.FlightNumber,
		Airline:        f.Airline,
		Origin:         f.Origin,
		Destination:    f.Destination,
		DepartureTime:  depTime,
		ArrivalTime:    arrTime,
		TotalSeats:     int(f.TotalSeats),
		AvailableSeats: int(f.AvailableSeats),
		Price:          int(f.Price),
		Status:         api.FlightStatus(f.Status),
	}
}
