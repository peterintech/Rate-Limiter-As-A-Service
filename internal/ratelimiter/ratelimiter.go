package ratelimiter

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidCost      = errors.New("cost must be greater than zero")
	ErrCostExceedsLimit = errors.New("cost cannot exceed the policy limit")
	ErrNoPolicy         = errors.New("no rate-limit policy found")
)

type Key struct {
	ClientID string
	Resource string
}

type Policy struct {
	Limit  int
	Window time.Duration
}

type Decision struct {
	Allowed      bool
	Limit        int
	Remaining    int
	ResetAt      time.Time
	RetryAfterMS int64
}

type Limiter interface {
	Allow(ctx context.Context, key Key, cost int) (Decision, error)
}
