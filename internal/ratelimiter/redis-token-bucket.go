package ratelimiter

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const defaultKeyPrefix = "rate_limit"

var redisTokenBucketScript = redis.NewScript(`
local capacity = tonumber(ARGV[1])
local window_ms = tonumber(ARGV[2])
local cost = tonumber(ARGV[3])
local ttl_ms = tonumber(ARGV[4])

local redis_time = redis.call("TIME")
local now_ms = (tonumber(redis_time[1]) * 1000) + math.floor(tonumber(redis_time[2]) / 1000)
local state = redis.call("HMGET", KEYS[1], "tokens", "last_refill_ms")

local tokens = tonumber(state[1])
local last_refill_ms = tonumber(state[2])

if tokens == nil or last_refill_ms == nil then
    tokens = capacity
    last_refill_ms = now_ms
else
    local elapsed_ms = math.max(0, now_ms - last_refill_ms)
    tokens = math.min(capacity, tokens + (elapsed_ms * capacity / window_ms))
    last_refill_ms = now_ms
end

local allowed = 0
local retry_after_ms = 0

if cost <= tokens then
    allowed = 1
    tokens = tokens - cost
else
    retry_after_ms = math.max(1, math.ceil((cost - tokens) * window_ms / capacity))
end

local remaining = math.floor(tokens)
local reset_at_ms = now_ms + math.ceil((capacity - tokens) * window_ms / capacity)

redis.call("HSET", KEYS[1], "tokens", tostring(tokens), "last_refill_ms", tostring(last_refill_ms))
redis.call("PEXPIRE", KEYS[1], ttl_ms)

return {allowed, remaining, reset_at_ms, retry_after_ms}
`)

type RedisTokenBucketConfig struct {
	KeyPrefix string
}

type RedisTokenBucketRateLimiter struct {
	client    *redis.Client
	policies  TokenBucketPolicies
	keyPrefix string
}

func NewRedisTokenBucketRateLimiter(client *redis.Client, policies TokenBucketPolicies, cfg RedisTokenBucketConfig) (*RedisTokenBucketRateLimiter, error) {
	if client == nil {
		return nil, errors.New("redis client is required")
	}
	if err := validateTokenBucketPolicies(policies); err != nil {
		return nil, err
	}

	keyPrefix := cfg.KeyPrefix
	if keyPrefix == "" {
		keyPrefix = defaultKeyPrefix
	}

	return &RedisTokenBucketRateLimiter{
		client:    client,
		policies:  policies,
		keyPrefix: keyPrefix,
	}, nil
}

func (l *RedisTokenBucketRateLimiter) Allow(ctx context.Context, key Key, cost int) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	if cost <= 0 {
		return Decision{}, ErrInvalidCost
	}

	policy, exists := l.policies[key]
	if !exists {
		return Decision{}, ErrNoPolicy
	}
	if cost > policy.Limit {
		return Decision{}, ErrCostExceedsLimit
	}

	windowMS := millisecondsRoundedUp(policy.Window)
	result, err := redisTokenBucketScript.Run(
		ctx,
		l.client,
		[]string{l.redisKey(key)},
		policy.Limit,
		windowMS,
		cost,
		windowMS*2,
	).Int64Slice()
	if err != nil {
		return Decision{}, fmt.Errorf("apply redis token bucket: %w", err)
	}
	if len(result) != 4 {
		return Decision{}, fmt.Errorf("apply redis token bucket: unexpected result length %d", len(result))
	}

	return Decision{
		Allowed:      result[0] == 1,
		Limit:        policy.Limit,
		Remaining:    int(result[1]),
		ResetAt:      time.UnixMilli(result[2]),
		RetryAfterMS: result[3],
	}, nil
}

// redisKey keeps each client/resource bucket independent and safe for Redis keys.
func (l *RedisTokenBucketRateLimiter) redisKey(key Key) string {
	clientID := base64.RawURLEncoding.EncodeToString([]byte(key.ClientID))
	resource := base64.RawURLEncoding.EncodeToString([]byte(key.Resource))
	return fmt.Sprintf("%s:%s:%s", l.keyPrefix, clientID, resource)
}
