package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/omo-ri/distributed-flight-booking/booking-service/api"
	"github.com/omo-ri/distributed-flight-booking/booking-service/internal/circuitbreaker"
	"github.com/omo-ri/distributed-flight-booking/booking-service/internal/logctx"
	"github.com/omo-ri/distributed-flight-booking/booking-service/internal/repository"
	"github.com/omo-ri/distributed-flight-booking/booking-service/internal/service"
)

// isCircuitOpen checks if the error is a circuit breaker open error and returns 503 if so.
func isCircuitOpen(c echo.Context, err error) bool {
	if errors.Is(err, circuitbreaker.ErrCircuitOpen) {
		logctx.Add(c.Request().Context(), logctx.KeyReason, "circuit_open")
		_ = c.JSON(http.StatusServiceUnavailable, api.Error{Message: "service temporarily unavailable"})
		return true
	}
	return false
}

// reject 记录一次业务拒绝。它是 Info 不是 Warn——座位不足、订单不存在都是
// 正常业务结果，没人需要为它做什么（CLAUDE.md § 4 的级别判据）。
func reject(c echo.Context, reason string) {
	logctx.Add(c.Request().Context(), logctx.KeyOutcome, logctx.OutcomeRejected, logctx.KeyReason, reason)
}

// fail 记录一次内部错误。级别不用在这里定：中间件按响应状态码判 Error。
func fail(c echo.Context, err error) {
	logctx.Add(c.Request().Context(), logctx.KeyError, err.Error())
}

// BookingHandler 不持有 logger——正常路径上它一行日志都不打，
// 值得记的字段挂进 logctx，由 requestLogger 写进那一条汇总行。
type BookingHandler struct {
	svc service.BookingService
}

func NewBookingHandler(svc service.BookingService) *BookingHandler {
	return &BookingHandler{svc: svc}
}

// Ensure BookingHandler implements api.ServerInterface.
var _ api.ServerInterface = (*BookingHandler)(nil)

func (h *BookingHandler) SearchFlights(c echo.Context, params api.SearchFlightsParams) error {
	ctx := c.Request().Context()
	var date string
	if params.Date != nil {
		date = params.Date.Time.Format("2006-01-02")
	}
	logctx.Add(ctx,
		logctx.KeyOrigin, params.Origin,
		logctx.KeyDestination, params.Destination,
		logctx.KeyDate, date,
	)

	flights, err := h.svc.SearchFlights(ctx, params.Origin, params.Destination, date)
	if err != nil {
		if isCircuitOpen(c, err) {
			return nil
		}
		fail(c, err)
		return c.JSON(http.StatusInternalServerError, api.Error{Message: err.Error()})
	}

	result := make([]api.Flight, 0, len(flights))
	for _, f := range flights {
		result = append(result, flightInfoToAPI(f))
	}

	return c.JSON(http.StatusOK, result)
}

func (h *BookingHandler) GetFlight(c echo.Context, id openapi_types.UUID) error {
	ctx := c.Request().Context()
	logctx.Add(ctx, logctx.KeyFlightID, id.String())

	flight, err := h.svc.GetFlight(ctx, id.String())
	if errors.Is(err, service.ErrFlightNotFound) {
		reject(c, "flight_not_found")
		return c.JSON(http.StatusNotFound, api.Error{Message: "flight not found"})
	}
	if err != nil {
		if isCircuitOpen(c, err) {
			return nil
		}
		fail(c, err)
		return c.JSON(http.StatusInternalServerError, api.Error{Message: err.Error()})
	}

	return c.JSON(http.StatusOK, flightInfoToAPI(flight))
}

func (h *BookingHandler) CreateBooking(c echo.Context) error {
	ctx := c.Request().Context()
	var req api.CreateBookingJSONRequestBody
	if err := c.Bind(&req); err != nil {
		reject(c, "invalid_body")
		return c.JSON(http.StatusBadRequest, api.Error{Message: "invalid request body"})
	}
	if req.SeatCount < 1 {
		reject(c, "invalid_seat_count")
		return c.JSON(http.StatusBadRequest, api.Error{Message: "seat_count must be at least 1"})
	}

	logctx.Add(ctx,
		logctx.KeyUserID, req.UserId.String(),
		logctx.KeyFlightID, req.FlightId.String(),
		logctx.KeySeatCount, req.SeatCount,
	)

	booking, err := h.svc.CreateBooking(ctx, service.CreateBookingInput{
		UserID:         req.UserId.String(),
		FlightID:       req.FlightId.String(),
		PassengerName:  req.PassengerName,
		PassengerEmail: req.PassengerEmail,
		SeatCount:      int32(req.SeatCount),
	})
	if errors.Is(err, service.ErrFlightNotFound) {
		reject(c, "flight_not_found")
		return c.JSON(http.StatusNotFound, api.Error{Message: "flight not found"})
	}
	if errors.Is(err, service.ErrInsufficientSeats) {
		reject(c, "insufficient_seats")
		return c.JSON(http.StatusConflict, api.Error{Message: "insufficient seats"})
	}
	if err != nil {
		if isCircuitOpen(c, err) {
			return nil
		}
		fail(c, err)
		return c.JSON(http.StatusInternalServerError, api.Error{Message: err.Error()})
	}

	return c.JSON(http.StatusCreated, bookingRowToAPI(booking))
}

func (h *BookingHandler) GetBooking(c echo.Context, id openapi_types.UUID) error {
	ctx := c.Request().Context()
	logctx.Add(ctx, logctx.KeyBookingID, id.String())

	booking, err := h.svc.GetBooking(ctx, id.String())
	if errors.Is(err, service.ErrNotFound) {
		reject(c, "booking_not_found")
		return c.JSON(http.StatusNotFound, api.Error{Message: "booking not found"})
	}
	if err != nil {
		fail(c, err)
		return c.JSON(http.StatusInternalServerError, api.Error{Message: err.Error()})
	}

	return c.JSON(http.StatusOK, bookingRowToAPI(booking))
}

func (h *BookingHandler) ListBookings(c echo.Context, params api.ListBookingsParams) error {
	ctx := c.Request().Context()
	logctx.Add(ctx, logctx.KeyUserID, params.UserId.String())

	bookings, err := h.svc.ListBookings(ctx, params.UserId.String())
	if err != nil {
		fail(c, err)
		return c.JSON(http.StatusInternalServerError, api.Error{Message: err.Error()})
	}

	result := make([]api.Booking, 0, len(bookings))
	for _, b := range bookings {
		result = append(result, bookingRowToAPI(b))
	}

	return c.JSON(http.StatusOK, result)
}

func (h *BookingHandler) CancelBooking(c echo.Context, id openapi_types.UUID) error {
	ctx := c.Request().Context()
	logctx.Add(ctx, logctx.KeyBookingID, id.String())

	booking, err := h.svc.CancelBooking(ctx, id.String())
	if errors.Is(err, service.ErrNotFound) {
		reject(c, "booking_not_found")
		return c.JSON(http.StatusNotFound, api.Error{Message: "booking not found"})
	}
	if errors.Is(err, service.ErrAlreadyCancelled) {
		reject(c, "already_cancelled")
		return c.JSON(http.StatusConflict, api.Error{Message: "booking already cancelled"})
	}
	if err != nil {
		if isCircuitOpen(c, err) {
			return nil
		}
		fail(c, err)
		return c.JSON(http.StatusInternalServerError, api.Error{Message: err.Error()})
	}

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
