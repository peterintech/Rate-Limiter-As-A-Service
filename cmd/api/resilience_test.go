package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/peterintech/global-rate-limiter/internal/ratelimiter"
)

func TestDependencyFailureIsBoundedAndRecovers(t *testing.T) {
	var calls atomic.Int32
	var available atomic.Bool

	dependency := limiterFunc(func(ctx context.Context, _ ratelimiter.Key, _ int) (ratelimiter.Decision, error) {
		calls.Add(1)
		if !available.Load() {
			<-ctx.Done()
			return ratelimiter.Decision{}, ctx.Err()
		}
		return ratelimiter.Decision{Allowed: true, Limit: 10, Remaining: 9}, nil
	})
	breaker := newTestCircuitBreaker(t, dependency)
	app := newTestApplicationWithLimiter(config{
		env:               "test",
		circuitBreakerCfg: testCircuitBreakerConfig(),
	}, breaker)
	app.readinessCheck = func(context.Context) error {
		if !available.Load() || !breaker.Ready() {
			return errors.New("dependency unavailable")
		}
		return nil
	}
	mux := app.mount()

	started := time.Now()
	response := executeResilienceCheck(mux)
	failureLatency := time.Since(started)
	checkResponse(t, "dependency failure", http.StatusServiceUnavailable, response.Code)
	const expectedBody = "{\"error\":\"rate-limit service temporarily unavailable\"}\n"
	if response.Body.String() != expectedBody {
		t.Errorf("expected bounded outage body %q; got %q", expectedBody, response.Body.String())
	}
	if response.Header().Get("Retry-After") != "1" {
		t.Errorf("expected Retry-After header of 1 second; got %q", response.Header().Get("Retry-After"))
	}
	checkResponse(t, "dependency calls before open circuit", 1, int(calls.Load()))
	checkResponse(t, "liveness during dependency failure", http.StatusOK, executeGet(mux, "/v1/health").Code)
	checkResponse(t, "readiness during dependency failure", http.StatusServiceUnavailable, executeGet(mux, "/v1/readiness").Code)

	response = executeResilienceCheck(mux)
	checkResponse(t, "open circuit response", http.StatusServiceUnavailable, response.Code)
	checkResponse(t, "dependency calls while circuit is open", 1, int(calls.Load()))

	available.Store(true)
	time.Sleep(2 * testCircuitBreakerConfig().OpenTimeout)
	checkResponse(t, "readiness after dependency recovery", http.StatusOK, executeGet(mux, "/v1/readiness").Code)
	checkResponse(t, "decision after dependency recovery", http.StatusOK, executeResilienceCheck(mux).Code)
	checkResponse(t, "dependency recovery probe", 2, int(calls.Load()))

	if failureLatency > 10*testCircuitBreakerConfig().DecisionTimeout {
		t.Errorf("expected a bounded dependency failure; took %s", failureLatency)
	}
	t.Logf("failure returned in %s; open circuit skipped Redis; recovery probe restored decisions", failureLatency.Round(time.Millisecond))
}

func TestRejectedDecisionDoesNotOpenCircuit(t *testing.T) {
	var calls atomic.Int32
	dependency := limiterFunc(func(context.Context, ratelimiter.Key, int) (ratelimiter.Decision, error) {
		calls.Add(1)
		return ratelimiter.Decision{Allowed: false, Limit: 10, Remaining: 0, RetryAfterMS: 1000}, nil
	})
	breaker := newTestCircuitBreaker(t, dependency)
	app := newTestApplicationWithLimiter(config{
		env:               "test",
		circuitBreakerCfg: testCircuitBreakerConfig(),
	}, breaker)
	mux := app.mount()

	for range 5 {
		checkResponse(t, "exhausted quota", http.StatusTooManyRequests, executeResilienceCheck(mux).Code)
	}
	checkResponse(t, "dependency calls", 5, int(calls.Load()))
	t.Log("five valid quota rejections reached the limiter; none opened the dependency circuit")
}

type limiterFunc func(context.Context, ratelimiter.Key, int) (ratelimiter.Decision, error)

func (fn limiterFunc) Allow(ctx context.Context, key ratelimiter.Key, cost int) (ratelimiter.Decision, error) {
	return fn(ctx, key, cost)
}

func newTestCircuitBreaker(t *testing.T, limiter ratelimiter.Limiter) *ratelimiter.CircuitBreakerLimiter {
	t.Helper()

	breaker, err := ratelimiter.NewCircuitBreakerLimiter(limiter, testCircuitBreakerConfig())
	if err != nil {
		t.Fatal(err)
	}
	return breaker
}

func testCircuitBreakerConfig() ratelimiter.CircuitBreakerConfig {
	return ratelimiter.CircuitBreakerConfig{
		FailureThreshold: 1,
		OpenTimeout:      10 * time.Millisecond,
		DecisionTimeout:  20 * time.Millisecond,
	}
}

func executeResilienceCheck(mux http.Handler) *httptest.ResponseRecorder {
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/check",
		bytes.NewBufferString(`{"client_id":"client-a","resource":"openai","cost":1}`),
	)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	return response
}

func executeGet(mux http.Handler, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	return response
}
