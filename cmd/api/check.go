package main

import (
	"errors"
	"net/http"
	"strconv"

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
	var payload checkRateLimitPayload
	if err := readJSON(w, r, &payload); err != nil {
		app.badRequestError(w, r, err)
		return
	}

	if err := validate.Struct(payload); err != nil {
		app.badRequestError(w, r, err)
		return
	}

	decision, err := app.rateLimiter.Allow(r.Context(), ratelimiter.Key{
		ClientID: payload.ClientID,
		Resource: payload.Resource,
	}, payload.Cost)
	if err != nil {
		switch {
		case errors.Is(err, ratelimiter.ErrNoPolicy):
			app.notFoundError(w, r, err)
		case errors.Is(err, ratelimiter.ErrInvalidCost), errors.Is(err, ratelimiter.ErrCostExceedsLimit):
			app.badRequestError(w, r, err)
		default:
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
		retryAfter := strconv.FormatInt(decision.RetryAfterMS, 10)

		w.Header().Set("Retry-After", retryAfter)

		status = http.StatusTooManyRequests
	}

	if err := writeJSON(w, status, response); err != nil {
		app.internalServerError(w, r, err)
	}
}
