package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrNotFound = errors.New("not found")

type BookingRow struct {
	ID             string
	UserID         string
	FlightID       string
	PassengerName  string
	PassengerEmail string
	SeatCount      int32
	TotalPrice     int64
	Status         string
	CreatedAt      time.Time
}

type BookingRepo struct {
	db *DB
}

func NewBookingRepo(db *DB) *BookingRepo {
	return &BookingRepo{db: db}
}

func (r *BookingRepo) Create(ctx context.Context, b BookingRow) (BookingRow, error) {
	err := r.db.Pool.QueryRow(ctx,
		`INSERT INTO bookings (id, user_id, flight_id, passenger_name, passenger_email, seat_count, total_price, status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, 'CONFIRMED')
		 RETURNING id, user_id, flight_id, passenger_name, passenger_email, seat_count, total_price, status, created_at`,
		b.ID, b.UserID, b.FlightID, b.PassengerName, b.PassengerEmail, b.SeatCount, b.TotalPrice,
	).Scan(&b.ID, &b.UserID, &b.FlightID, &b.PassengerName, &b.PassengerEmail,
		&b.SeatCount, &b.TotalPrice, &b.Status, &b.CreatedAt)
	return b, err
}

func (r *BookingRepo) GetByID(ctx context.Context, id string) (BookingRow, error) {
	var b BookingRow
	err := r.db.Pool.QueryRow(ctx,
		`SELECT id, user_id, flight_id, passenger_name, passenger_email, seat_count, total_price, status, created_at
		 FROM bookings WHERE id = $1`, id,
	).Scan(&b.ID, &b.UserID, &b.FlightID, &b.PassengerName, &b.PassengerEmail,
		&b.SeatCount, &b.TotalPrice, &b.Status, &b.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, ErrNotFound
	}
	return b, err
}

func (r *BookingRepo) ListByUserID(ctx context.Context, userID string) ([]BookingRow, error) {
	rows, err := r.db.Pool.Query(ctx,
		`SELECT id, user_id, flight_id, passenger_name, passenger_email, seat_count, total_price, status, created_at
		 FROM bookings WHERE user_id = $1 ORDER BY created_at DESC`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var bookings []BookingRow
	for rows.Next() {
		var b BookingRow
		if err := rows.Scan(&b.ID, &b.UserID, &b.FlightID, &b.PassengerName, &b.PassengerEmail,
			&b.SeatCount, &b.TotalPrice, &b.Status, &b.CreatedAt); err != nil {
			return nil, err
		}
		bookings = append(bookings, b)
	}
	return bookings, rows.Err()
}

func (r *BookingRepo) UpdateStatus(ctx context.Context, id string, status string) error {
	tag, err := r.db.Pool.Exec(ctx,
		`UPDATE bookings SET status = $1 WHERE id = $2`, status, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
