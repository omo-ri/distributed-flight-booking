package service

import (
	"context"
	"log"

	"github.com/omo-ri/coa-hw/hw3/flight-service/internal/cache"
	"github.com/omo-ri/coa-hw/hw3/flight-service/internal/repository"
)

type FlightService interface {
	SearchFlights(ctx context.Context, origin, destination, date string) ([]repository.FlightRow, error)
	GetFlight(ctx context.Context, id string) (repository.FlightRow, error)
	ReserveSeats(ctx context.Context, flightID string, seatCount int32, bookingID string) (repository.SeatReservationRow, error)
	ReleaseReservation(ctx context.Context, bookingID string) (repository.SeatReservationRow, error)
}

type flightService struct {
	repo  *repository.FlightRepo
	cache *cache.RedisCache // nil = caching disabled
}

func NewFlightService(repo *repository.FlightRepo, c *cache.RedisCache) FlightService {
	return &flightService{repo: repo, cache: c}
}

func (s *flightService) SearchFlights(ctx context.Context, origin, destination, date string) ([]repository.FlightRow, error) {
	// Cache read
	if s.cache != nil {
		if data, ok := s.cache.GetSearch(ctx, origin, destination, date); ok {
			var rows []repository.FlightRow
			if err := cache.UnmarshalJSON(data, &rows); err == nil {
				return rows, nil
			}
		}
	}

	// DB fallback
	rows, err := s.repo.SearchFlights(ctx, origin, destination, date)
	if err != nil {
		return nil, err
	}

	// Cache write
	if s.cache != nil {
		if data, err := cache.MarshalJSON(rows); err == nil {
			s.cache.SetSearch(ctx, origin, destination, date, data)
		}
	}

	return rows, nil
}

func (s *flightService) GetFlight(ctx context.Context, id string) (repository.FlightRow, error) {
	// Cache read
	if s.cache != nil {
		if data, ok := s.cache.GetFlight(ctx, id); ok {
			var row repository.FlightRow
			if err := cache.UnmarshalJSON(data, &row); err == nil {
				return row, nil
			}
		}
	}

	// DB fallback
	row, err := s.repo.GetFlightByID(ctx, id)
	if err != nil {
		return row, err
	}

	// Cache write
	if s.cache != nil {
		if data, err := cache.MarshalJSON(row); err == nil {
			s.cache.SetFlight(ctx, id, data)
		}
	}

	return row, nil
}

func (s *flightService) ReserveSeats(ctx context.Context, flightID string, seatCount int32, bookingID string) (repository.SeatReservationRow, error) {
	res, err := s.repo.ReserveSeats(ctx, flightID, seatCount, bookingID)
	if err != nil {
		return res, err
	}

	// Invalidate caches — available_seats changed
	s.invalidateFlightCache(ctx, flightID)

	return res, nil
}

func (s *flightService) ReleaseReservation(ctx context.Context, bookingID string) (repository.SeatReservationRow, error) {
	res, err := s.repo.ReleaseReservation(ctx, bookingID)
	if err != nil {
		return res, err
	}

	// Invalidate caches — available_seats changed
	s.invalidateFlightCache(ctx, res.FlightID)

	return res, nil
}

// invalidateFlightCache deletes the flight key and related search keys.
func (s *flightService) invalidateFlightCache(ctx context.Context, flightID string) {
	if s.cache == nil {
		return
	}

	s.cache.InvalidateFlight(ctx, flightID)

	// Fetch flight to get route info for search cache invalidation
	flight, err := s.repo.GetFlightByID(ctx, flightID)
	if err != nil {
		log.Printf("[CACHE] cannot invalidate search cache: failed to get flight %s: %v", flightID, err)
		return
	}

	date := flight.DepartureTime.Format("2006-01-02")
	s.cache.InvalidateSearchByFlight(ctx, flight.Origin, flight.Destination, date)
}
