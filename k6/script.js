// k6 load test for booking-service.
//
// Scenario: a closed-loop mix of reads (search + getById) and a small share
// of full write round-trips (create booking -> cancel booking). The
// create+cancel pair keeps available_seats stable across the whole run, so
// the test is repeatable.
//
// Thresholds enforce the SLOs declared by hw7 task 7:
//   - p(95) latency < 500ms
//   - request error rate < 1%
// When violated, k6 exits with code 99 and the CI step fails.

import http from 'k6/http';
import { check, sleep } from 'k6';
import { uuidv4 } from 'https://jslib.k6.io/k6-utils/1.4.0/index.js';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';

// Seeded flights (see flight-service/migrations/002_seed.up.sql).
const FLIGHTS = [
  'a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11', // SU1234
  'b0eebc99-9c0b-4ef8-bb6d-6bb9bd380a22', // SU5678
  'c0eebc99-9c0b-4ef8-bb6d-6bb9bd380a33', // DP402
];

export const options = {
  stages: [
    { duration: '10s', target: 10 }, // ramp up to 10 VUs
    { duration: '30s', target: 10 }, // hold 10 VUs for 30s (assignment minimum)
    { duration: '5s',  target: 0  }, // ramp down
  ],
  thresholds: {
    http_req_duration: ['p(95)<500'],
    http_req_failed:   ['rate<0.01'],
    checks:            ['rate>0.99'],
  },
  summaryTrendStats: ['avg', 'min', 'med', 'p(95)', 'p(99)', 'max'],
};

function pick(arr) {
  return arr[Math.floor(Math.random() * arr.length)];
}

export default function () {
  const r = Math.random();
  const flightId = pick(FLIGHTS);

  if (r < 0.5) {
    // 50% — search by route
    const res = http.get(`${BASE_URL}/flights?origin=SVO&destination=LED`, {
      tags: { endpoint: 'search_flights' },
    });
    check(res, { 'search 200': (r) => r.status === 200 });
  } else if (r < 0.9) {
    // 40% — get flight by id
    const res = http.get(`${BASE_URL}/flights/${flightId}`, {
      tags: { endpoint: 'get_flight' },
    });
    check(res, { 'get_flight 200': (r) => r.status === 200 });
  } else {
    // 10% — create booking, then cancel (keeps inventory neutral)
    const payload = JSON.stringify({
      user_id: uuidv4(),
      flight_id: flightId,
      passenger_name: 'k6 Load',
      passenger_email: 'k6@example.com',
      seat_count: 1,
    });
    const createRes = http.post(`${BASE_URL}/bookings`, payload, {
      headers: { 'Content-Type': 'application/json' },
      tags: { endpoint: 'create_booking' },
    });
    if (check(createRes, { 'create 201': (r) => r.status === 201 })) {
      const id = createRes.json('id');
      const cancelRes = http.post(`${BASE_URL}/bookings/${id}/cancel`, null, {
        tags: { endpoint: 'cancel_booking' },
      });
      check(cancelRes, { 'cancel 200': (r) => r.status === 200 });
    }
  }

  sleep(0.1);
}
