package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/peterintech/global-rate-limiter/internal/ratelimiter"
	"go.uber.org/zap"
)

func newTestApplication(t *testing.T, cfg config, now func() time.Time) *application {
	t.Helper()

	limiter, err := ratelimiter.NewTokenBucketRateLimiter(cfg.tokenBucketPolicies, now)
	if err != nil {
		t.Fatal(err)
	}

	return &application{
		config:      cfg,
		logger:      zap.NewNop().Sugar(),
		rateLimiter: limiter,
	}
}

func executeRequest(req *http.Request, mux *chi.Mux) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	return recorder
}

func checkResponse(t *testing.T, scope string, expected, actual int) {
	t.Helper()
	if expected != actual {
		t.Errorf("expected %s %d; got %d", scope, expected, actual)
	}
}
