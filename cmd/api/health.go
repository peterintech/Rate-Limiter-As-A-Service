package main

import "net/http"

func (app *application) healthCheckHandler(w http.ResponseWriter, r *http.Request) {
	response := map[string]string{
		"status":  "ok",
		"env":     app.config.env,
		"version": version,
	}

	if err := writeJSON(w, http.StatusOK, response); err != nil {
		app.internalServerError(w, r, err)
	}
}

func (app *application) readinessCheckHandler(w http.ResponseWriter, r *http.Request) {
	status := http.StatusOK
	response := map[string]string{
		"status":  "ready",
		"env":     app.config.env,
		"version": version,
	}

	if app.readinessCheck != nil {
		if err := app.readinessCheck(r.Context()); err != nil {
			app.logger.Warnw("readiness check failed", "error", err)
			status = http.StatusServiceUnavailable
			response["status"] = "not ready"
		}
	}

	if err := writeJSON(w, status, response); err != nil {
		app.internalServerError(w, r, err)
	}
}
