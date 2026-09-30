package main

import (
	"context"
	"time"
)

type backlogState uint8

const (
	backlogNormal backlogState = iota
	backlogWarning
	backlogFull
)

func (app *application) monitorBacklog(ctx context.Context) {
	ticker := time.NewTicker(app.config.statusEvery)
	defer ticker.Stop()

	state := backlogNormal
	var previousLength int64
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			status, err := app.processor.Status(ctx)
			if err != nil {
				continue
			}
			next := app.backlogState(status.StreamLength)
			if next != state {
				app.logBacklogTransition(state, next, status.StreamLength)
				state = next
			}
			if previousLength > 0 && status.StreamLength == 0 {
				app.logger.Info("approval event backlog fully drained")
			}
			previousLength = status.StreamLength
		}
	}
}

func (app *application) backlogState(length int64) backlogState {
	if length >= app.config.maxBacklog {
		return backlogFull
	}
	if length*100 >= app.config.maxBacklog*int64(app.config.warningLevel) {
		return backlogWarning
	}
	return backlogNormal
}

func (app *application) logBacklogTransition(previous, next backlogState, length int64) {
	fields := []any{"stream_length", length, "configured_limit", app.config.maxBacklog}
	switch next {
	case backlogWarning:
		app.logger.Warnw("approval event backlog warning threshold crossed", fields...)
	case backlogFull:
		app.logger.Errorw("approval admission stopped because event backlog is full", fields...)
	case backlogNormal:
		if previous == backlogFull {
			app.logger.Infow("approval admission resumed after event backlog drained", fields...)
			return
		}
		app.logger.Infow("approval event backlog returned below warning threshold", fields...)
	}
}
