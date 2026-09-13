package ratelimiter

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"
)

type TokenBucketPolicies map[Key]Policy

type tokenBucket struct {
	tokens     float64
	lastRefill time.Time
}

type TokenBucketRateLimiter struct {
	policies TokenBucketPolicies
	buckets  map[Key]tokenBucket
	now      func() time.Time
	mu       sync.Mutex
}

func NewTokenBucketRateLimiter(policies TokenBucketPolicies, now func() time.Time) (*TokenBucketRateLimiter, error) {
	if now == nil {
		return nil, errors.New("clock is required")
	}
	if err := validateTokenBucketPolicies(policies); err != nil {
		return nil, err
	}

	return &TokenBucketRateLimiter{
		policies: policies,
		buckets:  make(map[Key]tokenBucket),
		now:      now,
	}, nil
}

func (l *TokenBucketRateLimiter) Allow(ctx context.Context, key Key, cost int) (Decision, error) {
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

	// Keep refilling, checking, and spending tokens as one atomic operation.
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	bucket := l.refilledBucket(key, policy, now)

	if float64(cost) > bucket.tokens {
		retryAfter := durationForTokens(policy, float64(cost)-bucket.tokens)
		l.buckets[key] = bucket

		return Decision{
			Allowed:      false,
			Limit:        policy.Limit,
			Remaining:    wholeTokens(bucket.tokens),
			ResetAt:      now.Add(durationForTokens(policy, float64(policy.Limit)-bucket.tokens)),
			RetryAfterMS: max(1, millisecondsRoundedUp(retryAfter)),
		}, nil
	}

	bucket.tokens -= float64(cost)
	l.buckets[key] = bucket

	return Decision{
		Allowed:   true,
		Limit:     policy.Limit,
		Remaining: wholeTokens(bucket.tokens),
		ResetAt:   now.Add(durationForTokens(policy, float64(policy.Limit)-bucket.tokens)),
	}, nil
}

func validateTokenBucketPolicies(policies TokenBucketPolicies) error {
	for key, policy := range policies {
		if key.ClientID == "" || key.Resource == "" {
			return errors.New("policy client_id and resource are required")
		}
		if policy.Limit <= 0 || policy.Window <= 0 {
			return fmt.Errorf("invalid policy for %s/%s", key.ClientID, key.Resource)
		}
	}

	return nil
}

// refilledBucket restores tokens according to the time elapsed since the last decision.
func (l *TokenBucketRateLimiter) refilledBucket(key Key, policy Policy, now time.Time) tokenBucket {
	bucket, exists := l.buckets[key]
	if !exists {
		return tokenBucket{tokens: float64(policy.Limit), lastRefill: now}
	}

	elapsed := now.Sub(bucket.lastRefill)
	if elapsed <= 0 {
		return bucket
	}

	refilled := float64(elapsed) * float64(policy.Limit) / float64(policy.Window)
	bucket.tokens = min(float64(policy.Limit), bucket.tokens+refilled)
	bucket.lastRefill = now
	return bucket
}

// durationForTokens calculates how long the configured refill rate needs to produce tokens.
func durationForTokens(policy Policy, tokens float64) time.Duration {
	return time.Duration(math.Ceil(tokens * float64(policy.Window) / float64(policy.Limit)))
}

// wholeTokens reports capacity that can be spent immediately as an integer cost.
func wholeTokens(tokens float64) int {
	return int(math.Floor(tokens))
}

// millisecondsRoundedUp prevents a caller from retrying before enough tokens exist.
func millisecondsRoundedUp(duration time.Duration) int64 {
	return int64(math.Ceil(float64(duration) / float64(time.Millisecond)))
}
