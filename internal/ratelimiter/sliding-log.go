package ratelimiter

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type SlidingLogPolicy struct {
	Limit  int
	Window time.Duration
}

type SlidingLogPolicies map[Key]SlidingLogPolicy

type requestEntry struct {
	at   time.Time
	cost int
}

type slidingLog struct {
	requests []requestEntry
	used     int
}

type SlidingLogRateLimiter struct {
	policies SlidingLogPolicies
	logs     map[Key]slidingLog
	now      func() time.Time
	mutex    sync.Mutex
}

func NewSlidingLogRateLimiter(policies SlidingLogPolicies, now func() time.Time) (*SlidingLogRateLimiter, error) {
	if now == nil {
		return nil, errors.New("clock is required")
	}
	if err := validateSlidingLogPolicies(policies); err != nil {
		return nil, err
	}

	return &SlidingLogRateLimiter{
		policies: policies,
		logs:     make(map[Key]slidingLog),
		now:      now,
	}, nil
}

func (l *SlidingLogRateLimiter) Allow(ctx context.Context, key Key, cost int) (Decision, error) {
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

	// Keep removing, checking, and recording requests as one atomic operation.
	l.mutex.Lock()
	defer l.mutex.Unlock()

	now := l.now()
	log := l.activeLog(key, policy, now)
	l.logs[key] = log
	remaining := policy.Limit - log.used

	if cost > remaining {
		resetAt := nextAvailability(log, policy, cost, now)
		return Decision{
			Allowed:      false,
			Limit:        policy.Limit,
			Remaining:    remaining,
			ResetAt:      resetAt,
			RetryAfterMS: max(1, resetAt.Sub(now).Milliseconds()),
		}, nil
	}

	log.requests = append(log.requests, requestEntry{at: now, cost: cost})
	log.used += cost
	l.logs[key] = log

	return Decision{
		Allowed:   true,
		Limit:     policy.Limit,
		Remaining: policy.Limit - log.used,
		ResetAt:   log.requests[0].at.Add(policy.Window),
	}, nil
}

func validateSlidingLogPolicies(policies SlidingLogPolicies) error {
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

// activeLog removes requests that no longer count toward the rolling limit.
func (l *SlidingLogRateLimiter) activeLog(key Key, policy SlidingLogPolicy, now time.Time) slidingLog {
	log := l.logs[key]
	cutoff := now.Add(-policy.Window)
	firstActive := 0

	for firstActive < len(log.requests) && !log.requests[firstActive].at.After(cutoff) {
		log.used -= log.requests[firstActive].cost
		firstActive++
	}

	log.requests = log.requests[firstActive:]
	return log
}

// nextAvailability finds when enough recorded cost expires for the request.
func nextAvailability(log slidingLog, policy SlidingLogPolicy, cost int, now time.Time) time.Time {
	required := cost - (policy.Limit - log.used)
	released := 0

	for _, request := range log.requests {
		released += request.cost
		if released >= required {
			return request.at.Add(policy.Window)
		}
	}

	return now.Add(policy.Window)
}
