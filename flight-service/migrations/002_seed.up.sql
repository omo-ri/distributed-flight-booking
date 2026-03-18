INSERT INTO flights (id, flight_number, airline, origin, destination, departure_time, arrival_time, total_seats, available_seats, price)
VALUES
    ('a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11', 'SU1234', 'Aeroflot', 'SVO', 'LED', '2026-04-01 10:00:00', '2026-04-01 11:30:00', 180, 180, 15000),
    ('b0eebc99-9c0b-4ef8-bb6d-6bb9bd380a22', 'SU5678', 'Aeroflot', 'SVO', 'LED', '2026-04-01 18:00:00', '2026-04-01 19:30:00', 120, 120, 12000),
    ('c0eebc99-9c0b-4ef8-bb6d-6bb9bd380a33', 'DP402',  'Pobeda',   'VKO', 'LED', '2026-04-02 08:00:00', '2026-04-02 09:45:00', 189, 189, 8000)
ON CONFLICT DO NOTHING;
