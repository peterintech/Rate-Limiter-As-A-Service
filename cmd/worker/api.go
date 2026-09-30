package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func (app *application) mount() *chi.Mux {
	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(middleware.Recoverer)
	router.Route("/v1", func(router chi.Router) {
		router.Get("/health", app.healthCheckHandler)
		router.Get("/readiness", app.readinessCheckHandler)
		router.Get("/status", app.statusHandler)
	})
	return router
}

func (app *application) run(ctx context.Context) error {
	if err := app.processor.EnsureGroup(ctx); err != nil {
		return err
	}

	server := &http.Server{
		Addr:              app.config.addr,
		Handler:           app.mount(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       time.Minute,
	}

	stopped := make(chan error, 2)
	go func() {
		if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			stopped <- err
			return
		}
		stopped <- nil
	}()
	go func() { stopped <- app.process(ctx) }()
	go app.monitorBacklog(ctx)

	app.logger.Infow("worker operations server started", "addr", app.config.addr)

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return <-stopped
	case err := <-stopped:
		return err
	}
}
