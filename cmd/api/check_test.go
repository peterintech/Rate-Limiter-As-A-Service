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
		tokenBucketPolicies: ratelimiter.TokenBucketPolicies{
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
		tokenBucketPolicies: ratelimiter.TokenBucketPolicies{
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

func TestTokenBucketPreventsBoundaryReset(t *testing.T) {
	const limit = 3

	cfg := config{
		addr: ":0",
		env:  "test",
		tokenBucketPolicies: ratelimiter.TokenBucketPolicies{
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

	t.Logf("token bucket kept approvals at %d within %s for a capacity of %d", limit, burstInterval, limit)
}

func TestTokenBucketRefillsOverTime(t *testing.T) {
	const limit = 2

	cfg := config{
		addr: ":0",
		env:  "test",
		tokenBucketPolicies: ratelimiter.TokenBucketPolicies{
			{ClientID: "client-a", Resource: "openai"}: {Limit: limit, Window: time.Minute},
		},
	}
	now := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	app := newTestApplication(t, cfg, func() time.Time { return now })
	mux := app.mount()

	for range limit {
		request := httptest.NewRequest(
			http.MethodPost,
			"/v1/check",
			bytes.NewBufferString(`{"client_id":"client-a","resource":"openai","cost":1}`),
		)
		response := executeRequest(request, mux)
		checkResponse(t, "response code while spending capacity", http.StatusOK, response.Code)
	}

	now = now.Add(30 * time.Second)

	refilledRequest := httptest.NewRequest(
		http.MethodPost,
		"/v1/check",
		bytes.NewBufferString(`{"client_id":"client-a","resource":"openai","cost":1}`),
	)
	refilledResponse := executeRequest(refilledRequest, mux)
	checkResponse(t, "response code after one token refills", http.StatusOK, refilledResponse.Code)

	exhaustedRequest := httptest.NewRequest(
		http.MethodPost,
		"/v1/check",
		bytes.NewBufferString(`{"client_id":"client-a","resource":"openai","cost":1}`),
	)
	exhaustedResponse := executeRequest(exhaustedRequest, mux)
	checkResponse(t, "response code after refilled token is spent", http.StatusTooManyRequests, exhaustedResponse.Code)

	t.Log("one token refilled after half of the two-token window elapsed")
}

func TestIndependentInstancesMultiplyQuota(t *testing.T) {
	const limit = 3

	cfg := config{
		addr: ":0",
		env:  "test",
		tokenBucketPolicies: ratelimiter.TokenBucketPolicies{
			{ClientID: "client-a", Resource: "openai"}: {Limit: limit, Window: time.Minute},
		},
	}
	now := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	instances := []struct {
		name string
		app  *application
	}{
		{name: "instance A", app: newTestApplication(t, cfg, func() time.Time { return now })},
		{name: "instance B", app: newTestApplication(t, cfg, func() time.Time { return now })},
	}

	totalAllowed := 0
	for _, instance := range instances {
		mux := instance.app.mount()
		instanceAllowed := 0

		for requestNumber := 1; requestNumber <= limit+1; requestNumber++ {
			request := httptest.NewRequest(
				http.MethodPost,
				"/v1/check",
				bytes.NewBufferString(`{"client_id":"client-a","resource":"openai","cost":1}`),
			)
			response := executeRequest(request, mux)

			expectedStatus := http.StatusOK
			if requestNumber > limit {
				expectedStatus = http.StatusTooManyRequests
			}
			checkResponse(t, instance.name+" response code", expectedStatus, response.Code)

			if response.Code == http.StatusOK {
				instanceAllowed++
			}
		}

		checkResponse(t, instance.name+" allowed requests", limit, instanceAllowed)
		totalAllowed += instanceAllowed
		t.Logf("%s approved %d requests against a configured quota of %d", instance.name, instanceAllowed, limit)
	}

	checkResponse(t, "combined approvals", limit*len(instances), totalAllowed)
	t.Logf("intended cluster quota=%d; instances=%d; combined approvals=%d", limit, len(instances), totalAllowed)
}
