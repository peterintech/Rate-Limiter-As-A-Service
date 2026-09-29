package main

import (
	"context"
	"time"

	"github.com/peterintech/global-rate-limiter/internal/events"
	"go.uber.org/zap"
)

type application struct {
	config    config
	logger    *zap.SugaredLogger
	processor *events.Processor
}

func (app *application) run(ctx context.Context) error {
	if err := app.processor.EnsureGroup(ctx); err != nil {
		return err
	}

	app.logger.Infow(
		"approval worker started",
		"stream", app.config.events.Stream,
		"group", app.config.events.Group,
		"consumer", app.config.events.Consumer,
	)

	claimTicker := time.NewTicker(app.config.claimEvery)
	defer claimTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			app.logger.Info("approval worker stopped")
			return nil
		case <-claimTicker.C:
			count, err := app.processor.RecoverPending(ctx)
			if err != nil {
				app.logFailure(ctx, "recover abandoned approval events", err)
				continue
			}
			if count > 0 {
				app.logger.Infow("recovered approval events", "count", count)
			}
		default:
			count, err := app.processor.ConsumeNew(ctx)
			if err != nil {
				app.logFailure(ctx, "consume approval events", err)
				continue
			}
			if count > 0 {
				app.logger.Infow("persisted approval events", "count", count)
			}
		}
	}
}

func (app *application) logFailure(ctx context.Context, operation string, err error) {
	if ctx.Err() != nil {
		return
	}
	app.logger.Errorw(operation, "error", err, "retry_in", app.config.retryBackoff)

	timer := time.NewTimer(app.config.retryBackoff)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
