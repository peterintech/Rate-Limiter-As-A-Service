package main

import (
	"context"
	"net/http"
	"time"
)

type healthResponse struct {
	Status string `json:"status"`
}

type statusResponse struct {
	StreamLength        int64   `json:"stream_length"`
	PendingEvents       int64   `json:"pending_events"`
	OldestEventAgeMS    int64   `json:"oldest_event_age_ms"`
	ConfiguredLimit     int64   `json:"configured_limit"`
	CapacityUsedPercent float64 `json:"capacity_used_percent"`
}

func (app *application) healthCheckHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{Status: "available"})
}

func (app *application) readinessCheckHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if err := app.processor.Ready(ctx); err != nil {
		app.logger.Errorw("worker dependencies unavailable", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, healthResponse{Status: "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, healthResponse{Status: "available"})
}

func (app *application) statusHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	status, err := app.processor.Status(ctx)
	if err != nil {
		app.logger.Errorw("failed to read worker status", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, healthResponse{Status: "unavailable"})
		return
	}

	response := statusResponse{
		StreamLength:        status.StreamLength,
		PendingEvents:       status.PendingCount,
		OldestEventAgeMS:    oldestEventAge(status.OldestEvent),
		ConfiguredLimit:     app.config.maxBacklog,
		CapacityUsedPercent: float64(status.StreamLength) * 100 / float64(app.config.maxBacklog),
	}
	writeJSON(w, http.StatusOK, response)
}

func oldestEventAge(oldest time.Time) int64 {
	if oldest.IsZero() {
		return 0
	}
	age := time.Since(oldest)
	if age < 0 {
		return 0
	}
	return age.Milliseconds()
}
