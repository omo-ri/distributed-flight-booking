import subprocess
import pytest

BASE_URL = "http://localhost:8080"


@pytest.fixture(scope="session")
def base_url():
    return BASE_URL


@pytest.fixture(scope="session", autouse=True)
def reset_db():
    """Reset both databases before the entire test session."""
    # Reset booking_db
    subprocess.run(
        [
            "docker", "compose", "exec", "-T", "booking-db",
            "psql", "-U", "booking", "-d", "booking_db",
            "-c", "TRUNCATE bookings CASCADE;",
        ],
        cwd="/home/omo-ri/GolandProjects/coa-hw/hw3",
        check=True,
        capture_output=True,
    )
    # Reset flight_db: clear reservations and restore seat counts
    subprocess.run(
        [
            "docker", "compose", "exec", "-T", "flight-db",
            "psql", "-U", "flight", "-d", "flight_db",
            "-c", "TRUNCATE seat_reservations CASCADE; UPDATE flights SET available_seats = total_seats;",
        ],
        cwd="/home/omo-ri/GolandProjects/coa-hw/hw3",
        check=True,
        capture_output=True,
    )
    # Flush Redis cache
    subprocess.run(
        [
            "docker", "compose", "exec", "-T", "redis",
            "redis-cli", "FLUSHALL",
        ],
        cwd="/home/omo-ri/GolandProjects/coa-hw/hw3",
        check=True,
        capture_output=True,
    )
