"""Booking-service REST API integration tests.

Tests are organized in ordered classes so that stateful scenarios
(create → query → cancel) can share booking IDs via class attributes.
Run with:  pytest tests/ -v
"""

import uuid

import requests

# --- Seed data constants ---
FLIGHT_SU1234 = "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"  # SVO→LED, 180 seats, 15000
FLIGHT_SU5678 = "b0eebc99-9c0b-4ef8-bb6d-6bb9bd380a22"  # SVO→LED, 120 seats, 12000
FLIGHT_DP402 = "c0eebc99-9c0b-4ef8-bb6d-6bb9bd380a33"   # VKO→LED, 189 seats, 8000
NON_EXISTENT_ID = "00000000-0000-0000-0000-000000000000"
USER_ID = str(uuid.uuid4())


# ─── 1. Flight queries (stateless) ───────────────────────────────────────────


class TestFlightSearch:
    def test_search_flights_with_date(self, base_url):
        resp = requests.get(
            f"{base_url}/flights",
            params={"origin": "SVO", "destination": "LED", "date": "2026-04-01"},
        )
        assert resp.status_code == 200
        data = resp.json()
        assert len(data) == 2

    def test_search_flights_without_date(self, base_url):
        resp = requests.get(
            f"{base_url}/flights",
            params={"origin": "SVO", "destination": "LED"},
        )
        assert resp.status_code == 200
        data = resp.json()
        assert len(data) == 2

    def test_search_flights_another_route(self, base_url):
        resp = requests.get(
            f"{base_url}/flights",
            params={"origin": "VKO", "destination": "LED"},
        )
        assert resp.status_code == 200
        data = resp.json()
        assert len(data) == 1
        assert data[0]["flight_number"] == "DP402"

    def test_get_flight_exists(self, base_url):
        resp = requests.get(f"{base_url}/flights/{FLIGHT_SU1234}")
        assert resp.status_code == 200
        data = resp.json()
        assert data["flight_number"] == "SU1234"
        assert data["available_seats"] == 180
        assert data["price"] == 15000

    def test_get_flight_not_found(self, base_url):
        resp = requests.get(f"{base_url}/flights/{NON_EXISTENT_ID}")
        assert resp.status_code == 404


# ─── 2. Booking create → query → cancel (stateful) ──────────────────────────


class TestBookingLifecycle:
    booking_id: str = ""

    # -- create --

    def test_create_booking(self, base_url):
        resp = requests.post(
            f"{base_url}/bookings",
            json={
                "user_id": USER_ID,
                "flight_id": FLIGHT_SU1234,
                "passenger_name": "Ivan Ivanov",
                "passenger_email": "ivan@example.com",
                "seat_count": 2,
            },
        )
        assert resp.status_code == 201
        data = resp.json()
        assert data["status"] == "CONFIRMED"
        assert data["seat_count"] == 2
        assert data["total_price"] == 2 * 15000
        TestBookingLifecycle.booking_id = data["id"]

    def test_seats_decreased_after_booking(self, base_url):
        resp = requests.get(f"{base_url}/flights/{FLIGHT_SU1234}")
        assert resp.status_code == 200
        assert resp.json()["available_seats"] == 178

    def test_create_booking_insufficient_seats(self, base_url):
        resp = requests.post(
            f"{base_url}/bookings",
            json={
                "user_id": USER_ID,
                "flight_id": FLIGHT_SU1234,
                "passenger_name": "Bulk Buyer",
                "passenger_email": "bulk@example.com",
                "seat_count": 999,
            },
        )
        assert resp.status_code == 409

    def test_create_booking_flight_not_found(self, base_url):
        resp = requests.post(
            f"{base_url}/bookings",
            json={
                "user_id": USER_ID,
                "flight_id": NON_EXISTENT_ID,
                "passenger_name": "Ghost",
                "passenger_email": "ghost@example.com",
                "seat_count": 1,
            },
        )
        assert resp.status_code == 404

    # -- query --

    def test_get_booking(self, base_url):
        assert TestBookingLifecycle.booking_id, "No booking_id from create test"
        resp = requests.get(f"{base_url}/bookings/{TestBookingLifecycle.booking_id}")
        assert resp.status_code == 200
        data = resp.json()
        assert data["passenger_name"] == "Ivan Ivanov"
        assert data["status"] == "CONFIRMED"

    def test_get_booking_not_found(self, base_url):
        resp = requests.get(f"{base_url}/bookings/{NON_EXISTENT_ID}")
        assert resp.status_code == 404

    def test_list_bookings(self, base_url):
        resp = requests.get(f"{base_url}/bookings", params={"user_id": USER_ID})
        assert resp.status_code == 200
        data = resp.json()
        assert len(data) >= 1
        assert any(b["id"] == TestBookingLifecycle.booking_id for b in data)

    # -- cancel --

    def test_cancel_booking(self, base_url):
        assert TestBookingLifecycle.booking_id, "No booking_id from create test"
        resp = requests.post(
            f"{base_url}/bookings/{TestBookingLifecycle.booking_id}/cancel"
        )
        assert resp.status_code == 200
        data = resp.json()
        assert data["status"] == "CANCELLED"

    def test_cancel_booking_duplicate(self, base_url):
        resp = requests.post(
            f"{base_url}/bookings/{TestBookingLifecycle.booking_id}/cancel"
        )
        assert resp.status_code == 409

    def test_seats_restored_after_cancel(self, base_url):
        resp = requests.get(f"{base_url}/flights/{FLIGHT_SU1234}")
        assert resp.status_code == 200
        assert resp.json()["available_seats"] == 180
