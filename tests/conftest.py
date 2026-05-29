import pathlib
import subprocess

import psycopg2
import pytest

BASE_URL = "http://localhost:8080"
BOOKING_DB_DSN = "host=localhost port=5434 user=booking password=booking_pass dbname=booking_db"
FLIGHT_DB_DSN = "host=localhost port=5433 user=flight password=flight_pass dbname=flight_db"

# Repo root for `docker compose` calls — works in CI and locally.
COMPOSE_CWD = pathlib.Path(__file__).resolve().parent.parent


@pytest.fixture(scope="session")
def base_url():
    return BASE_URL


def _compose_exec(service: str, *cmd: str) -> None:
    subprocess.run(
        ["docker", "compose", "exec", "-T", service, *cmd],
        cwd=str(COMPOSE_CWD),
        check=True,
        capture_output=True,
    )


@pytest.fixture(scope="session", autouse=True)
def reset_db():
    """Reset both databases before the entire test session."""
    _compose_exec(
        "booking-db",
        "psql", "-U", "booking", "-d", "booking_db",
        "-c", "TRUNCATE bookings CASCADE;",
    )
    _compose_exec(
        "flight-db",
        "psql", "-U", "flight", "-d", "flight_db",
        "-c", "TRUNCATE seat_reservations CASCADE; UPDATE flights SET available_seats = total_seats;",
    )
    # Flush Redis (Sentinel-managed master)
    _compose_exec("redis-master", "redis-cli", "FLUSHALL")


@pytest.fixture(scope="session")
def booking_db():
    conn = psycopg2.connect(BOOKING_DB_DSN)
    conn.autocommit = True
    yield conn
    conn.close()


@pytest.fixture(scope="session")
def flight_db():
    conn = psycopg2.connect(FLIGHT_DB_DSN)
    conn.autocommit = True
    yield conn
    conn.close()
