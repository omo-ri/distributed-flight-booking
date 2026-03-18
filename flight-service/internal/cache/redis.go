package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
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
	log.Printf("[CACHE] connected via Sentinel (master=%s, sentinel=%s)", masterName, sentinelAddr)
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
		log.Printf("[CACHE] MISS %s", key)
		return nil, false
	}
	log.Printf("[CACHE] HIT  %s", key)
	return val, true
}

// SetFlight stores a flight in cache with TTL.
func (c *RedisCache) SetFlight(ctx context.Context, id string, data []byte) {
	key := flightKey(id)
	if err := c.client.Set(ctx, key, data, FlightTTL).Err(); err != nil {
		log.Printf("[CACHE] SET ERR %s: %v", key, err)
		return
	}
	log.Printf("[CACHE] SET  %s (ttl=%v)", key, FlightTTL)
}

// InvalidateFlight deletes a flight from cache.
func (c *RedisCache) InvalidateFlight(ctx context.Context, id string) {
	key := flightKey(id)
	c.client.Del(ctx, key)
	log.Printf("[CACHE] DEL  %s", key)
}

// --- Search cache ---

// GetSearch retrieves a cached search result. Returns nil on miss.
func (c *RedisCache) GetSearch(ctx context.Context, origin, destination, date string) ([]byte, bool) {
	key := searchKey(origin, destination, date)
	val, err := c.client.Get(ctx, key).Bytes()
	if err != nil {
		log.Printf("[CACHE] MISS %s", key)
		return nil, false
	}
	log.Printf("[CACHE] HIT  %s", key)
	return val, true
}

// SetSearch stores a search result in cache with TTL.
func (c *RedisCache) SetSearch(ctx context.Context, origin, destination, date string, data []byte) {
	key := searchKey(origin, destination, date)
	if err := c.client.Set(ctx, key, data, SearchTTL).Err(); err != nil {
		log.Printf("[CACHE] SET ERR %s: %v", key, err)
		return
	}
	log.Printf("[CACHE] SET  %s (ttl=%v)", key, SearchTTL)
}

// InvalidateSearchByFlight deletes search cache entries related to a flight's route.
func (c *RedisCache) InvalidateSearchByFlight(ctx context.Context, origin, destination, date string) {
	// Delete the exact key with date
	if date != "" {
		key := searchKey(origin, destination, date)
		c.client.Del(ctx, key)
		log.Printf("[CACHE] DEL  %s", key)
	}
	// Also delete the no-date variant
	key := searchKey(origin, destination, "")
	c.client.Del(ctx, key)
	log.Printf("[CACHE] DEL  %s", key)
}

// MarshalJSON is a helper for callers.
func MarshalJSON(v any) ([]byte, error) {
	return json.Marshal(v)
}

// UnmarshalJSON is a helper for callers.
func UnmarshalJSON(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
