package main

import (
	"net/http"
	"strconv"
	"time"
)

func (app *application) internalServerError(w http.ResponseWriter, r *http.Request, err error) {
	app.logger.Errorw("internal server error", "method", r.Method, "path", r.URL.Path, "error", err)
	writeErrorJSON(w, http.StatusInternalServerError, "rate-limit check failed")
}

func (app *application) serviceUnavailableError(w http.ResponseWriter, r *http.Request, err error) {
	app.logger.Errorw("rate-limit dependency unavailable", "method", r.Method, "path", r.URL.Path, "error", err)
	w.Header().Set("Retry-After", retryAfterSeconds(app.config.circuitBreakerCfg.OpenTimeout))
	writeErrorJSON(w, http.StatusServiceUnavailable, "rate-limit service temporarily unavailable")
}

func retryAfterSeconds(duration time.Duration) string {
	seconds := (duration + time.Second - 1) / time.Second
	return strconv.FormatInt(int64(seconds), 10)
}

func (app *application) badRequestError(w http.ResponseWriter, r *http.Request, err error) {
	app.logger.Warnw("bad request", "method", r.Method, "path", r.URL.Path, "error", err)
	writeErrorJSON(w, http.StatusBadRequest, err.Error())
}

func (app *application) notFoundError(w http.ResponseWriter, r *http.Request, err error) {
	app.logger.Warnw("not found", "method", r.Method, "path", r.URL.Path, "error", err)
	writeErrorJSON(w, http.StatusNotFound, err.Error())
}
