CREATE TABLE IF NOT EXISTS flights
(
    id              UUID PRIMARY KEY      DEFAULT gen_random_uuid(),
    flight_number   VARCHAR(10)  NOT NULL,
    airline         VARCHAR(100) NOT NULL,
    origin          VARCHAR(3)   NOT NULL,
    destination     VARCHAR(3)   NOT NULL,
    departure_time  TIMESTAMP    NOT NULL,
    arrival_time    TIMESTAMP    NOT NULL,
    total_seats     INT          NOT NULL CHECK (total_seats > 0),
    available_seats INT          NOT NULL CHECK (available_seats >= 0),
    price           BIGINT       NOT NULL CHECK (price > 0),
    status          VARCHAR(20)  NOT NULL DEFAULT 'SCHEDULED',
    CONSTRAINT chk_available_le_total CHECK (available_seats <= total_seats),
    CONSTRAINT chk_status CHECK (status IN ('SCHEDULED', 'DEPARTED', 'CANCELLED', 'COMPLETED'))
);

CREATE UNIQUE INDEX uq_flight_number_date ON flights (flight_number, CAST(departure_time AS date));
CREATE INDEX idx_flights_route_date ON flights (origin, destination, CAST(departure_time AS date));

CREATE TABLE IF NOT EXISTS seat_reservations
(
    id         UUID PRIMARY KEY     DEFAULT gen_random_uuid(),
    flight_id  UUID        NOT NULL REFERENCES flights (id),
    booking_id UUID        NOT NULL UNIQUE,
    seat_count INT         NOT NULL CHECK (seat_count > 0),
    status     VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',
    created_at TIMESTAMP   NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_reservation_status CHECK (status IN ('ACTIVE', 'RELEASED', 'EXPIRED'))
);

CREATE INDEX idx_reservations_booking ON seat_reservations (booking_id);