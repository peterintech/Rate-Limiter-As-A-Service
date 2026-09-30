package events

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Status struct {
	StreamLength int64
	PendingCount int64
	OldestEvent  time.Time
}

func (p *Processor) Ready(ctx context.Context) error {
	if err := p.redis.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("ping Redis: %w", err)
	}
	if err := p.pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping PostgreSQL: %w", err)
	}
	return nil
}

func (p *Processor) Status(ctx context.Context) (Status, error) {
	pipe := p.redis.Pipeline()
	lengthCommand := pipe.XLen(ctx, p.config.Stream)
	pendingCommand := pipe.XPending(ctx, p.config.Stream, p.config.Group)
	oldestCommand := pipe.XRangeN(ctx, p.config.Stream, "-", "+", 1)
	if _, err := pipe.Exec(ctx); err != nil {
		return Status{}, fmt.Errorf("read approval event status: %w", err)
	}

	status := Status{
		StreamLength: lengthCommand.Val(),
		PendingCount: pendingCommand.Val().Count,
	}
	oldest := oldestCommand.Val()
	if len(oldest) == 0 {
		return status, nil
	}

	milliseconds, _, found := strings.Cut(oldest[0].ID, "-")
	if !found {
		return Status{}, fmt.Errorf("approval event has invalid stream ID %q", oldest[0].ID)
	}
	timestamp, err := strconv.ParseInt(milliseconds, 10, 64)
	if err != nil {
		return Status{}, fmt.Errorf("approval event has invalid stream ID %q: %w", oldest[0].ID, err)
	}
	status.OldestEvent = time.UnixMilli(timestamp)
	return status, nil
}
