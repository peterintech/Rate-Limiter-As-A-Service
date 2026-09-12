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
