package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/peterintech/global-rate-limiter/internal/ratelimiter"
	"go.uber.org/zap"
)

type application struct {
	config      config
	logger      *zap.SugaredLogger
	rateLimiter ratelimiter.Limiter
}

type config struct {
	addr                string
	env                 string
	fixedWindowPolicies ratelimiter.FixedWindowPolicies
}

func (app *application) mount() *chi.Mux {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Logger)

	r.Route("/v1", func(r chi.Router) {
		r.Get("/health", app.healthCheckHandler)
		r.Post("/check", app.checkRateLimitHandler)
	})

	return r
}

func (app *application) run(handler http.Handler) error {
	server := &http.Server{
		Addr:              app.config.addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       time.Minute,
	}

	shutdown := make(chan error, 1)
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		sig := <-quit

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		app.logger.Infow("shutting down server", "signal", sig.String())
		shutdown <- server.Shutdown(ctx)
	}()

	app.logger.Infow("server started", "addr", app.config.addr, "env", app.config.env)

	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return <-shutdown
}
