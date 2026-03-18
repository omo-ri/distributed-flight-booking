package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrNotFound = errors.New("not found")

type FlightRow struct {
	ID             string
	FlightNumber   string
	Airline        string
	Origin         string
	Destination    string
	DepartureTime  time.Time
	ArrivalTime    time.Time
	TotalSeats     int32
	AvailableSeats int32
	Price          int64
	Status         string
}

type SeatReservationRow struct {
	ID        string
	FlightID  string
	BookingID string
	SeatCount int32
	Status    string
	CreatedAt time.Time
}

type FlightRepo struct {
	db *DB
}

func NewFlightRepo(db *DB) *FlightRepo {
	return &FlightRepo{db: db}
}

func (r *FlightRepo) SearchFlights(ctx context.Context, origin, destination, date string) ([]FlightRow, error) {
	query := `SELECT id, flight_number, airline, origin, destination,
	                  departure_time, arrival_time, total_seats, available_seats, price, status
	           FROM flights
	           WHERE origin = $1 AND destination = $2 AND status = 'SCHEDULED'`
	args := []any{origin, destination}

	if date != "" {
		query += ` AND departure_time::date = $3`
		args = append(args, date)
	}

	query += ` ORDER BY departure_time`

	return r.scanFlights(ctx, r.db.Pool, query, args...)
}

func (r *FlightRepo) GetFlightByID(ctx context.Context, id string) (FlightRow, error) {
	return r.getFlightByID(ctx, r.db.Pool, id)
}

func (r *FlightRepo) getFlightByID(ctx context.Context, q DBTX, id string) (FlightRow, error) {
	var f FlightRow
	err := q.QueryRow(ctx,
		`SELECT id, flight_number, airline, origin, destination,
		        departure_time, arrival_time, total_seats, available_seats, price, status
		 FROM flights WHERE id = $1`, id,
	).Scan(&f.ID, &f.FlightNumber, &f.Airline, &f.Origin, &f.Destination,
		&f.DepartureTime, &f.ArrivalTime, &f.TotalSeats, &f.AvailableSeats, &f.Price, &f.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return f, ErrNotFound
	}
	return f, err
}

// ReserveSeats atomically decreases available_seats and creates a SeatReservation in one transaction.
// Uses SELECT FOR UPDATE to prevent race conditions.
func (r *FlightRepo) ReserveSeats(ctx context.Context, flightID string, seatCount int32, bookingID string) (SeatReservationRow, error) {
	var res SeatReservationRow

	err := r.db.WithTx(ctx, func(tx pgx.Tx) error {
		// Idempotency: check if reservation already exists for this booking
		err := tx.QueryRow(ctx,
			`SELECT id, flight_id, booking_id, seat_count, status, created_at
			 FROM seat_reservations WHERE booking_id = $1 AND status = 'ACTIVE'`, bookingID,
		).Scan(&res.ID, &res.FlightID, &res.BookingID, &res.SeatCount, &res.Status, &res.CreatedAt)
		if err == nil {
			return nil // already reserved
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		// Lock the flight row
		var available int32
		err = tx.QueryRow(ctx,
			`SELECT available_seats FROM flights WHERE id = $1 FOR UPDATE`, flightID,
		).Scan(&available)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}

		if available < seatCount {
			return ErrInsufficientSeats
		}

		// Decrease available seats
		_, err = tx.Exec(ctx,
			`UPDATE flights SET available_seats = available_seats - $1 WHERE id = $2`,
			seatCount, flightID)
		if err != nil {
			return err
		}

		// Create reservation
		err = tx.QueryRow(ctx,
			`INSERT INTO seat_reservations (flight_id, booking_id, seat_count, status)
			 VALUES ($1, $2, $3, 'ACTIVE')
			 RETURNING id, flight_id, booking_id, seat_count, status, created_at`,
			flightID, bookingID, seatCount,
		).Scan(&res.ID, &res.FlightID, &res.BookingID, &res.SeatCount, &res.Status, &res.CreatedAt)
		return err
	})

	return res, err
}

var ErrInsufficientSeats = errors.New("insufficient seats")

// ReleaseReservation finds the active reservation for bookingID, returns seats, and marks it RELEASED.
func (r *FlightRepo) ReleaseReservation(ctx context.Context, bookingID string) (SeatReservationRow, error) {
	var res SeatReservationRow

	err := r.db.WithTx(ctx, func(tx pgx.Tx) error {
		// Find active reservation
		err := tx.QueryRow(ctx,
			`SELECT id, flight_id, booking_id, seat_count, status, created_at
			 FROM seat_reservations WHERE booking_id = $1 AND status = 'ACTIVE'`, bookingID,
		).Scan(&res.ID, &res.FlightID, &res.BookingID, &res.SeatCount, &res.Status, &res.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}

		// Return seats
		_, err = tx.Exec(ctx,
			`UPDATE flights SET available_seats = available_seats + $1 WHERE id = $2`,
			res.SeatCount, res.FlightID)
		if err != nil {
			return err
		}

		// Mark released
		_, err = tx.Exec(ctx,
			`UPDATE seat_reservations SET status = 'RELEASED' WHERE id = $1`, res.ID)
		if err != nil {
			return err
		}

		res.Status = "RELEASED"
		return nil
	})

	return res, err
}

func (r *FlightRepo) scanFlights(ctx context.Context, q DBTX, query string, args ...any) ([]FlightRow, error) {
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var flights []FlightRow
	for rows.Next() {
		var f FlightRow
		if err := rows.Scan(&f.ID, &f.FlightNumber, &f.Airline, &f.Origin, &f.Destination,
			&f.DepartureTime, &f.ArrivalTime, &f.TotalSeats, &f.AvailableSeats, &f.Price, &f.Status); err != nil {
			return nil, err
		}
		flights = append(flights, f)
	}
	return flights, rows.Err()
}