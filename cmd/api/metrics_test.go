package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/peterintech/global-rate-limiter/internal/ratelimiter"
)

func TestRateLimiterMetricsReportDecisionOutcomes(t *testing.T) {
	cfg := config{
		env: "test",
		tokenBucketPolicies: ratelimiter.TokenBucketPolicies{
			{ClientID: "client-a", Resource: "openai"}: {Limit: 1, Window: time.Minute},
		},
	}
	app := newTestApplication(t, cfg, time.Now)
	mux := app.mount()

	checks := []struct {
		body   string
		status int
	}{
		{body: `{"client_id":"client-a","resource":"openai","cost":1}`, status: http.StatusOK},
		{body: `{"client_id":"client-a","resource":"openai","cost":1}`, status: http.StatusTooManyRequests},
		{body: `{"client_id":"client-a","resource":"openai","cost":0}`, status: http.StatusBadRequest},
		{body: `{"client_id":"client-a","resource":"unconfigured-resource","cost":1}`, status: http.StatusNotFound},
	}
	for _, check := range checks {
		request := httptest.NewRequest(http.MethodPost, "/v1/check", bytes.NewBufferString(check.body))
		response := executeRequest(request, mux)
		checkResponse(t, "instrumented decision", check.status, response.Code)
	}

	response := executeRequest(httptest.NewRequest(http.MethodGet, "/metrics", nil), mux)
	checkResponse(t, "metrics endpoint", http.StatusOK, response.Code)
	body := response.Body.String()

	expected := []string{
		`rate_limiter_decisions_total{outcome="allowed",resource="openai"} 1`,
		`rate_limiter_decisions_total{outcome="quota_rejected",resource="openai"} 1`,
		`rate_limiter_decisions_total{outcome="invalid_request",resource="openai"} 1`,
		`rate_limiter_decisions_total{outcome="unknown_policy",resource="unknown"} 1`,
		`rate_limiter_decision_duration_seconds_count{outcome="allowed",resource="openai"} 1`,
		`rate_limiter_in_flight_requests 0`,
	}
	for _, metric := range expected {
		if !strings.Contains(body, metric) {
			t.Errorf("expected metrics output to contain %q", metric)
		}
	}
	if strings.Contains(body, "client-a") || strings.Contains(body, "unconfigured-resource") {
		t.Error("metrics output exposed an unbounded client or resource label")
	}

	t.Log("metrics reported four outcomes, one duration per decision, zero in-flight work, and no client identifier")
}
