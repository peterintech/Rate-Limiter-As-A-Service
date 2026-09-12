package ratelimiter

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type FixedWindowPolicy struct {
	Limit  int
	Window time.Duration
}

type FixedWindowPolicies map[Key]FixedWindowPolicy

type fixedWindow struct {
	start time.Time
	used  int
}

type FixedWindowRateLimiter struct {
	policies FixedWindowPolicies
	windows  map[Key]fixedWindow
	now      func() time.Time
	mutex    sync.Mutex
}

func NewFixedWindowRateLimiter(policies FixedWindowPolicies, now func() time.Time) (*FixedWindowRateLimiter, error) {
	if now == nil {
		return nil, errors.New("clock is required")
	}
	if err := validateFixedWindowPolicies(policies); err != nil {
		return nil, err
	}

	return &FixedWindowRateLimiter{
		policies: policies,
		windows:  make(map[Key]fixedWindow),
		now:      now,
	}, nil
}

func (l *FixedWindowRateLimiter) Allow(ctx context.Context, key Key, cost int) (Decision, error) {
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

	// Keep reading, checking, and updating a window as one atomic operation.
	l.mutex.Lock()
	defer l.mutex.Unlock()

	now := l.now()
	window := l.windowFor(key, policy, now)
	resetAt := window.start.Add(policy.Window)

	if cost > policy.Limit-window.used {
		return Decision{
			Allowed:      false,
			Limit:        policy.Limit,
			Remaining:    policy.Limit - window.used,
			ResetAt:      resetAt,
			RetryAfterMS: max(1, resetAt.Sub(now).Milliseconds()),
		}, nil
	}

	// Record the cost only after confirming the complete request can be allowed.
	window.used += cost
	l.windows[key] = window

	return Decision{
		Allowed:   true,
		Limit:     policy.Limit,
		Remaining: policy.Limit - window.used,
		ResetAt:   resetAt,
	}, nil
}

func validateFixedWindowPolicies(policies FixedWindowPolicies) error {
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

// windowFor returns the key's state for the current aligned time window.
func (l *FixedWindowRateLimiter) windowFor(key Key, policy FixedWindowPolicy, now time.Time) fixedWindow {
	start := now.Truncate(policy.Window)
	window := l.windows[key]
	if !window.start.Equal(start) {
		return fixedWindow{start: start}
	}

	return window
}
