package ratelimiter

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var ErrUnavailable = errors.New("rate-limit dependency unavailable")

type CircuitBreakerConfig struct {
	FailureThreshold int
	OpenTimeout      time.Duration
	DecisionTimeout  time.Duration
}

type circuitState uint8

const (
	circuitClosed circuitState = iota
	circuitOpen
	circuitHalfOpen
)

type CircuitBreakerLimiter struct {
	limiter Limiter
	config  CircuitBreakerConfig
	now     func() time.Time

	mu           sync.Mutex
	state        circuitState
	failures     int
	openedAt     time.Time
	probeRunning bool
}

func NewCircuitBreakerLimiter(limiter Limiter, config CircuitBreakerConfig) (*CircuitBreakerLimiter, error) {
	if limiter == nil {
		return nil, errors.New("limiter is required")
	}
	if config.FailureThreshold <= 0 {
		return nil, errors.New("circuit-breaker failure threshold must be greater than zero")
	}
	if config.OpenTimeout <= 0 {
		return nil, errors.New("circuit-breaker open timeout must be greater than zero")
	}
	if config.DecisionTimeout <= 0 {
		return nil, errors.New("rate-limit decision timeout must be greater than zero")
	}

	return &CircuitBreakerLimiter{
		limiter: limiter,
		config:  config,
		now:     time.Now,
	}, nil
}

func (l *CircuitBreakerLimiter) Allow(ctx context.Context, key Key, cost int) (Decision, error) {
	if !l.startRequest() {
		return Decision{}, ErrUnavailable
	}

	decisionCtx, cancel := context.WithTimeout(ctx, l.config.DecisionTimeout)
	defer cancel()

	decision, err := l.limiter.Allow(decisionCtx, key, cost)
	if err == nil {
		l.recordSuccess()
		return decision, nil
	}
	if isRequestError(err) || ctx.Err() != nil {
		l.cancelProbe()
		return Decision{}, err
	}

	l.recordFailure()
	return Decision{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
}

// Ready reports whether the circuit can currently attempt a Redis operation.
func (l *CircuitBreakerLimiter) Ready() bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.state != circuitOpen || l.now().Sub(l.openedAt) >= l.config.OpenTimeout
}

func (l *CircuitBreakerLimiter) startRequest() bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.state == circuitClosed {
		return true
	}
	if l.state == circuitOpen {
		if l.now().Sub(l.openedAt) < l.config.OpenTimeout {
			return false
		}
		l.state = circuitHalfOpen
	}
	if l.probeRunning {
		return false
	}

	l.probeRunning = true
	return true
}

func (l *CircuitBreakerLimiter) recordSuccess() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.state = circuitClosed
	l.failures = 0
	l.probeRunning = false
}

func (l *CircuitBreakerLimiter) recordFailure() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.probeRunning = false
	if l.state == circuitHalfOpen {
		l.open()
		return
	}

	l.failures++
	if l.failures >= l.config.FailureThreshold {
		l.open()
	}
}

func (l *CircuitBreakerLimiter) cancelProbe() {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.state == circuitHalfOpen {
		l.state = circuitOpen
	}
	l.probeRunning = false
}

func (l *CircuitBreakerLimiter) open() {
	l.state = circuitOpen
	l.openedAt = l.now()
}

func isRequestError(err error) bool {
	return errors.Is(err, ErrInvalidCost) ||
		errors.Is(err, ErrCostExceedsLimit) ||
		errors.Is(err, ErrNoPolicy)
}
