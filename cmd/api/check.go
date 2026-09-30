package main

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/peterintech/global-rate-limiter/internal/ratelimiter"
)

type checkRateLimitPayload struct {
	ClientID string `json:"client_id" validate:"required"`
	Resource string `json:"resource" validate:"required"`
	Cost     int    `json:"cost" validate:"required,gt=0"`
}

type checkRateLimitResponse struct {
	Allowed      bool   `json:"allowed"`
	Limit        int    `json:"limit"`
	Remaining    int    `json:"remaining"`
	ResetAt      string `json:"reset_at"`
	RetryAfterMS int64  `json:"retry_after_ms"`
}

func (app *application) checkRateLimitHandler(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	resource := unknownMetricResource
	outcome := metricOutcomeInternalError
	app.metrics.inFlight.Inc()
	defer func() {
		app.metrics.inFlight.Dec()
		app.metrics.observe(resource, outcome, started)
	}()

	var payload checkRateLimitPayload
	if err := readJSON(w, r, &payload); err != nil {
		outcome = metricOutcomeInvalidRequest
		app.badRequestError(w, r, err)
		return
	}

	if err := validate.Struct(payload); err != nil {
		resource = payload.Resource
		outcome = metricOutcomeInvalidRequest
		app.badRequestError(w, r, err)
		return
	}
	resource = payload.Resource

	decision, err := app.rateLimiter.Allow(r.Context(), ratelimiter.Key{
		ClientID: payload.ClientID,
		Resource: payload.Resource,
	}, payload.Cost)
	if err != nil {
		switch {
		case errors.Is(err, ratelimiter.ErrUnavailable):
			outcome = metricOutcomeDependencyUnavailable
			app.serviceUnavailableError(w, r, err)
		case errors.Is(err, ratelimiter.ErrEventBacklogFull):
			outcome = metricOutcomeBacklogFull
			app.serviceUnavailableError(w, r, err)
		case errors.Is(err, ratelimiter.ErrNoPolicy):
			outcome = metricOutcomeUnknownPolicy
			app.notFoundError(w, r, err)
		case errors.Is(err, ratelimiter.ErrInvalidCost), errors.Is(err, ratelimiter.ErrCostExceedsLimit):
			outcome = metricOutcomeInvalidRequest
			app.badRequestError(w, r, err)
		default:
			outcome = metricOutcomeInternalError
			app.internalServerError(w, r, err)
		}
		return
	}

	response := checkRateLimitResponse{
		Allowed:      decision.Allowed,
		Limit:        decision.Limit,
		Remaining:    decision.Remaining,
		ResetAt:      decision.ResetAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		RetryAfterMS: decision.RetryAfterMS,
	}
	status := http.StatusOK
	if !decision.Allowed {
		outcome = metricOutcomeQuotaRejected
		retryAfter := strconv.FormatInt(decision.RetryAfterMS, 10)

		w.Header().Set("Retry-After", retryAfter)

		status = http.StatusTooManyRequests
	} else {
		outcome = metricOutcomeAllowed
	}

	if err := writeJSON(w, status, response); err != nil {
		outcome = metricOutcomeInternalError
		app.internalServerError(w, r, err)
	}
}
