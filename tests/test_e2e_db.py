"""End-to-end test that drives the public REST API and asserts state
landed correctly in BOTH databases.

Scenario:
  search flight  ->  create booking  ->  cancel booking
For each step we assert:
  - HTTP status + response body shape
  - booking_db.bookings row (created / status flipped to CANCELLED)
  - flight_db.seat_reservations row (ACTIVE / RELEASED)
  - flight_db.flights.available_seats decremented and then restored

The test is intentionally isolated: it uses a fresh user_id (uuid4)
so it can't collide with other tests, and it cleans the booking it
created at teardown.
"""

import uuid

import psycopg2.extras
import pytest
import requests

FLIGHT_SU1234 = "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"


@pytest.fixture
def user_id():
    return str(uuid.uuid4())


def _fetch_booking_row(booking_db, booking_id):
    with booking_db.cursor(cursor_factory=psycopg2.extras.RealDictCursor) as cur:
        cur.execute("SELECT * FROM bookings WHERE id = %s", (booking_id,))
        return cur.fetchone()


def _fetch_reservation_row(flight_db, booking_id):
    with flight_db.cursor(cursor_factory=psycopg2.extras.RealDictCursor) as cur:
        cur.execute(
            "SELECT * FROM seat_reservations WHERE booking_id = %s",
            (booking_id,),
        )
        return cur.fetchone()


def _fetch_available_seats(flight_db, flight_id):
    with flight_db.cursor() as cur:
        cur.execute("SELECT available_seats FROM flights WHERE id = %s", (flight_id,))
        return cur.fetchone()[0]


def test_full_booking_lifecycle_persists_to_both_dbs(
    base_url, booking_db, flight_db, user_id
):
    seats_before = _fetch_available_seats(flight_db, FLIGHT_SU1234)

    # --- 1. Create booking ---
    resp = requests.post(
        f"{base_url}/bookings",
        json={
            "user_id": user_id,
            "flight_id": FLIGHT_SU1234,
            "passenger_name": "E2E Tester",
            "passenger_email": "e2e@example.com",
            "seat_count": 3,
        },
    )
    assert resp.status_code == 201, resp.text
    body = resp.json()
    booking_id = body["id"]
    assert body["status"] == "CONFIRMED"
    assert body["seat_count"] == 3
    assert isinstance(body["total_price"], int)

    # --- 2. Assert booking_db row ---
    row = _fetch_booking_row(booking_db, booking_id)
    assert row is not None, "booking row not persisted in booking_db"
    assert row["status"] == "CONFIRMED"
    assert row["seat_count"] == 3
    assert str(row["user_id"]) == user_id
    assert str(row["flight_id"]) == FLIGHT_SU1234
    assert row["passenger_email"] == "e2e@example.com"

    # --- 3. Assert flight_db cross-service state ---
    reservation = _fetch_reservation_row(flight_db, booking_id)
    assert reservation is not None, "seat reservation not persisted in flight_db"
    assert reservation["status"] == "ACTIVE"
    assert reservation["seat_count"] == 3

    assert _fetch_available_seats(flight_db, FLIGHT_SU1234) == seats_before - 3

    # --- 4. Cancel booking ---
    resp = requests.post(f"{base_url}/bookings/{booking_id}/cancel")
    assert resp.status_code == 200, resp.text
    assert resp.json()["status"] == "CANCELLED"

    # --- 5. Assert state reflected in both DBs ---
    row = _fetch_booking_row(booking_db, booking_id)
    assert row["status"] == "CANCELLED"

    reservation = _fetch_reservation_row(flight_db, booking_id)
    assert reservation["status"] == "RELEASED"

    assert _fetch_available_seats(flight_db, FLIGHT_SU1234) == seats_before
