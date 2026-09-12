package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
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
		fixedWindowPolicies: ratelimiter.FixedWindowPolicies{
			{ClientID: "client-a", Resource: "openai"}: {Limit: limit, Window: time.Minute},
		},
	}
	app := newTestApplication(t, cfg)
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
		checkResponseCode(t, expectedStatus, response.Code)
	}
}

func TestRateLimiterHandlesConcurrentRequests(t *testing.T) {
	const limit = 10
	const requests = 50

	cfg := config{
		addr: ":0",
		env:  "test",
		fixedWindowPolicies: ratelimiter.FixedWindowPolicies{
			{ClientID: "client-a", Resource: "openai"}: {Limit: limit, Window: time.Minute},
		},
	}
	app := newTestApplication(t, cfg)
	mux := app.mount()

	var allowed atomic.Int32
	var rejected atomic.Int32
	var requestsGroup sync.WaitGroup
	start := make(chan struct{})

	for range requests {
		requestsGroup.Add(1)
		go func() {
			defer requestsGroup.Done()
			<-start

			request := httptest.NewRequest(
				http.MethodPost,
				"/v1/check",
				bytes.NewBufferString(`{"client_id":"client-a","resource":"openai","cost":1}`),
			)
			response := executeRequest(request, mux)

			switch response.Code {
			case http.StatusOK:
				allowed.Add(1)
			case http.StatusTooManyRequests:
				rejected.Add(1)
			default:
				t.Errorf("unexpected response code %d", response.Code)
			}
		}()
	}

	close(start)
	requestsGroup.Wait()

	if got := int(allowed.Load()); got != limit {
		t.Errorf("expected %d allowed requests; got %d", limit, got)
	}
	if got := int(rejected.Load()); got != requests-limit {
		t.Errorf("expected %d rejected requests; got %d", requests-limit, got)
	}
}
