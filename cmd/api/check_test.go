package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/peterintech/global-rate-limiter/internal/ratelimiter"
)

func TestRateLimiter(t *testing.T) {
	const limit = 3
	const reqs = 6

	cfg := config{
		addr: ":0",
		env:  "test",
		slidingLogPolicies: ratelimiter.SlidingLogPolicies{
			{ClientID: "client-a", Resource: "openai"}: {Limit: limit, Window: time.Minute},
		},
	}
	app := newTestApplication(t, cfg, time.Now)
	mux := app.mount()

	for req := 1; req <= reqs; req++ {
		request := httptest.NewRequest(
			http.MethodPost,
			"/v1/check",
			bytes.NewBufferString(`{"client_id":"client-a","resource":"openai","cost":1}`),
		)
		response := executeRequest(request, mux)

		expectedStatus := http.StatusOK
		if req > limit {
			expectedStatus = http.StatusTooManyRequests
		}
		checkResponse(t, "response code", expectedStatus, response.Code)
	}

	t.Logf("policy limit=%d; allowed=%d; rejected=%d", limit, limit, reqs-limit)
}

func TestRateLimiterHandlesConcurrentRequests(t *testing.T) {
	const limit = 10
	const requests = 50

	cfg := config{
		addr: ":0",
		env:  "test",
		slidingLogPolicies: ratelimiter.SlidingLogPolicies{
			{ClientID: "client-a", Resource: "openai"}: {Limit: limit, Window: time.Minute},
		},
	}
	app := newTestApplication(t, cfg, time.Now)
	mux := app.mount()

	start := make(chan struct{})
	results := make(chan int, requests)

	for range requests {
		go func() {
			<-start

			request := httptest.NewRequest(
				http.MethodPost,
				"/v1/check",
				bytes.NewBufferString(`{"client_id":"client-a","resource":"openai","cost":1}`),
			)
			response := executeRequest(request, mux)
			results <- response.Code
		}()
	}

	close(start)

	allowed := 0
	rejected := 0
	for range requests {
		switch status := <-results; status {
		case http.StatusOK:
			allowed++
		case http.StatusTooManyRequests:
			rejected++
		default:
			t.Errorf("unexpected response code %d", status)
		}
	}

	checkResponse(t, "allowed requests", limit, allowed)
	checkResponse(t, "rejected requests", requests-limit, rejected)
	t.Logf("concurrent requests=%d; allowed=%d; rejected=%d", requests, allowed, rejected)
}

func TestSlidingLogPreventsBoundaryBurst(t *testing.T) {
	const limit = 3

	cfg := config{
		addr: ":0",
		env:  "test",
		slidingLogPolicies: ratelimiter.SlidingLogPolicies{
			{ClientID: "client-a", Resource: "openai"}: {Limit: limit, Window: time.Minute},
		},
	}
	now := time.Date(2026, time.January, 1, 12, 0, 59, 900_000_000, time.UTC)
	app := newTestApplication(t, cfg, func() time.Time { return now })
	mux := app.mount()
	t.Logf("sending %d requests at %s, immediately before the fixed-window boundary", limit, now.Format(time.RFC3339Nano))

	for range limit {
		request := httptest.NewRequest(
			http.MethodPost,
			"/v1/check",
			bytes.NewBufferString(`{"client_id":"client-a","resource":"openai","cost":1}`),
		)
		response := executeRequest(request, mux)
		checkResponse(t, "response code before boundary", http.StatusOK, response.Code)
	}

	burstInterval := 200 * time.Millisecond
	now = now.Add(burstInterval)

	t.Logf("sending %d more requests at %s, immediately after the fixed-window boundary", limit, now.Format(time.RFC3339Nano))

	for range limit {
		request := httptest.NewRequest(
			http.MethodPost,
			"/v1/check",
			bytes.NewBufferString(`{"client_id":"client-a","resource":"openai","cost":1}`),
		)
		response := executeRequest(request, mux)
		checkResponse(t, "response code after boundary", http.StatusTooManyRequests, response.Code)
	}

	t.Logf("sliding log kept approvals at %d within %s for a limit of %d per minute", limit, burstInterval, limit)
}
