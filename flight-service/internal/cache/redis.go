package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/omo-ri/distributed-flight-booking/flight-service/internal/logctx"
	"github.com/omo-ri/distributed-flight-booking/flight-service/internal/metrics"
)

const (
	FlightTTL = 5 * time.Minute
	SearchTTL = 5 * time.Minute
)

type RedisCache struct {
	client redis.UniversalClient
}

// NewRedisSentinelCache creates a Redis client that connects via Sentinel for high availability.
func NewRedisSentinelCache(sentinelAddr, masterName string) (*RedisCache, error) {
	client := redis.NewFailoverClient(&redis.FailoverOptions{
		MasterName:    masterName,
		SentinelAddrs: []string{sentinelAddr},
	})
	if err := client.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("redis sentinel ping: %w", err)
	}
	return &RedisCache{client: client}, nil
}

// NewRedisCache creates a direct Redis client (non-sentinel, for local dev).
func NewRedisCache(addr string) (*RedisCache, error) {
	client := redis.NewClient(&redis.Options{Addr: addr})
	if err := client.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return &RedisCache{client: client}, nil
}

func (c *RedisCache) Close() error {
	return c.client.Close()
}

// --- Flight cache ---

func flightKey(id string) string {
	return "flight:" + id
}

func searchKey(origin, destination, date string) string {
	return fmt.Sprintf("search:%s:%s:%s", origin, destination, date)
}

// GetFlight retrieves a cached flight. Returns nil on miss.
func (c *RedisCache) GetFlight(ctx context.Context, id string) ([]byte, bool) {
	key := flightKey(id)
	val, err := c.client.Get(ctx, key).Bytes()
	if err != nil {
		record(ctx, metrics.CacheFlight, metrics.OpGet, metrics.ResultMiss)
		return nil, false
	}
	record(ctx, metrics.CacheFlight, metrics.OpGet, metrics.ResultHit)
	return val, true
}

// SetFlight stores a flight in cache with TTL.
func (c *RedisCache) SetFlight(ctx context.Context, id string, data []byte) {
	key := flightKey(id)
	if err := c.client.Set(ctx, key, data, FlightTTL).Err(); err != nil {
		// 写缓存失败不影响这次响应（已经从 DB 拿到数据了），但它是异常，
		// 让这一条请求的汇总行升到 Warn，否则缓存悄悄失效没人知道。
		logctx.Escalate(ctx, slog.LevelWarn)
		logctx.Add(ctx, logctx.KeyDegraded, "cache_set_failed")
		metrics.CacheOperationsTotal.WithLabelValues(metrics.CacheFlight, metrics.OpSet, metrics.ResultError).Inc()
		return
	}
	metrics.CacheOperationsTotal.WithLabelValues(metrics.CacheFlight, metrics.OpSet, metrics.ResultOK).Inc()
}

// InvalidateFlight deletes a flight from cache.
func (c *RedisCache) InvalidateFlight(ctx context.Context, id string) {
	del(ctx, c, metrics.CacheFlight, flightKey(id))
}

// --- Search cache ---

// GetSearch retrieves a cached search result. Returns nil on miss.
func (c *RedisCache) GetSearch(ctx context.Context, origin, destination, date string) ([]byte, bool) {
	key := searchKey(origin, destination, date)
	val, err := c.client.Get(ctx, key).Bytes()
	if err != nil {
		record(ctx, metrics.CacheSearch, metrics.OpGet, metrics.ResultMiss)
		return nil, false
	}
	record(ctx, metrics.CacheSearch, metrics.OpGet, metrics.ResultHit)
	return val, true
}

// SetSearch stores a search result in cache with TTL.
func (c *RedisCache) SetSearch(ctx context.Context, origin, destination, date string, data []byte) {
	key := searchKey(origin, destination, date)
	if err := c.client.Set(ctx, key, data, SearchTTL).Err(); err != nil {
		logctx.Escalate(ctx, slog.LevelWarn)
		logctx.Add(ctx, logctx.KeyDegraded, "cache_set_failed")
		metrics.CacheOperationsTotal.WithLabelValues(metrics.CacheSearch, metrics.OpSet, metrics.ResultError).Inc()
		return
	}
	metrics.CacheOperationsTotal.WithLabelValues(metrics.CacheSearch, metrics.OpSet, metrics.ResultOK).Inc()
}

// InvalidateSearchByFlight deletes search cache entries related to a flight's route.
func (c *RedisCache) InvalidateSearchByFlight(ctx context.Context, origin, destination, date string) {
	// Delete the exact key with date
	if date != "" {
		del(ctx, c, metrics.CacheSearch, searchKey(origin, destination, date))
	}
	// Also delete the no-date variant
	del(ctx, c, metrics.CacheSearch, searchKey(origin, destination, ""))
}

// record 把一次缓存读的结果同时写进指标和这一条请求的汇总行。
// 两者回答的问题不同：指标答「这一档命中率多少」，日志答「这一条命中了没有」。
func record(ctx context.Context, cacheName, op, result string) {
	metrics.CacheOperationsTotal.WithLabelValues(cacheName, op, result).Inc()
	if op == metrics.OpGet {
		logctx.Add(ctx, logctx.KeyCache, cacheField(result))
	}
}

// cacheField 把指标的 result 标签翻成日志字段的取值。两者字面量相同是巧合不是约定——
// 显式翻一道，改指标标签时不会顺手改掉日志字段。
func cacheField(result string) string {
	if result == metrics.ResultHit {
		return logctx.CacheHit
	}
	return logctx.CacheMiss
}

// del 删一个键并记一次指标。删失败不抬级别——失效失败的后果（读到旧余座数）
// 由调用方 service.invalidateFlightCache 判断，那里已经有 Warn。
func del(ctx context.Context, c *RedisCache, cacheName, key string) {
	result := metrics.ResultOK
	if err := c.client.Del(ctx, key).Err(); err != nil {
		result = metrics.ResultError
	}
	metrics.CacheOperationsTotal.WithLabelValues(cacheName, metrics.OpDel, result).Inc()
}

// MarshalJSON is a helper for callers.
func MarshalJSON(v any) ([]byte, error) {
	return json.Marshal(v)
}

// UnmarshalJSON is a helper for callers.
func UnmarshalJSON(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
