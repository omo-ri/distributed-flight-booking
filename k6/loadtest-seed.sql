-- 压测装置：k6 三场景专用航班。
--
-- 为什么不进 flight-service/migrations/：
--   迁移是生产 schema 的一部分，把压测数据塞进去会污染每一次部署和每一次
--   L2 容器启动。这些行是测试装置，不是系统的一部分（见 docs/tasks/T-06）。
-- 为什么不走 API：
--   系统没有航班管理路径（design/system-design.md § 1.1），造不出来。
--
-- ID 是确定性的，k6 脚本自己算得出，不需要在 SQL 与 JS 之间传数据：
--   读航班  00000000-0000-4000-8000-0000000000NN   (NN = 01..32 hex, 共 50 个)
--   写航班  00000000-0000-4000-8000-00000000ffff
--
-- 幂等：可以反复灌。写航班的库存会被重置回满值。
--
-- 用法：make loadtest-seed

BEGIN;

-- 清掉上一轮压测留下的预留记录，让每轮的起点相同。
-- 注意：booking-db 里的 bookings 行清不掉（跨库，没有分布式事务）——它们会
-- 逐轮累积。要彻底重置用 `docker compose down -v`。
DELETE FROM seat_reservations
WHERE flight_id::text LIKE '00000000-0000-4000-8000-%';

-- ---------------------------------------------------------------------------
-- 读路径：50 个航班，同一条航线同一天，让 GET /flights/{id} 的热点打散。
-- 座位数无所谓，读路径不碰库存。
-- ---------------------------------------------------------------------------
INSERT INTO flights (id, flight_number, airline, origin, destination,
                     departure_time, arrival_time,
                     total_seats, available_seats, price, status)
SELECT
    ('00000000-0000-4000-8000-' || lpad(to_hex(i), 12, '0'))::uuid,
    'LT' || lpad(i::text, 4, '0'),
    'LoadTest Air',
    'AAA', 'BBB',
    TIMESTAMP '2030-01-01 00:00:00' + (i * INTERVAL '1 minute'),
    TIMESTAMP '2030-01-01 02:00:00' + (i * INTERVAL '1 minute'),
    300, 300, 10000, 'SCHEDULED'
FROM generate_series(1, 50) AS i
ON CONFLICT (id) DO NOTHING;

-- ---------------------------------------------------------------------------
-- 单航班写：一个航班，库存大到压不干。
--
-- 为什么要大：单航班 300 座，开环压几秒就卖光，之后测的是「409 的吞吐量」。
-- 行锁竞争的是那一行，不是那个数字——把库存耗尽这个变量消掉，测出来的数
-- 才只反映锁本身（docs/tasks/T-06）。
--
-- 5,000,000 座：写阶梯即使跑满 1500 req/s × 10 分钟也只消耗 90 万。
-- ---------------------------------------------------------------------------
INSERT INTO flights (id, flight_number, airline, origin, destination,
                     departure_time, arrival_time,
                     total_seats, available_seats, price, status)
VALUES (
    '00000000-0000-4000-8000-00000000ffff'::uuid,
    'LTHOT', 'LoadTest Air', 'AAA', 'CCC',
    TIMESTAMP '2030-06-01 12:00:00', TIMESTAMP '2030-06-01 14:00:00',
    5000000, 5000000, 10000, 'SCHEDULED'
)
ON CONFLICT (id) DO UPDATE
    SET total_seats     = EXCLUDED.total_seats,
        available_seats = EXCLUDED.total_seats;  -- 重置库存，让每轮压测起点相同

COMMIT;

\echo '--- loadtest flights ---'
SELECT flight_number, id, total_seats, available_seats
FROM flights
WHERE id::text LIKE '00000000-0000-4000-8000-%'
ORDER BY flight_number
LIMIT 5;

SELECT count(*) AS loadtest_flight_count
FROM flights
WHERE id::text LIKE '00000000-0000-4000-8000-%';
