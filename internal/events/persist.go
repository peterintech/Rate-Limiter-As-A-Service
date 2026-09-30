package events

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/peterintech/global-rate-limiter/internal/database"
	"github.com/redis/go-redis/v9"
)

func (p *Processor) persistMessages(ctx context.Context, messages []redis.XMessage) (int, error) {
	if len(messages) == 0 {
		return 0, nil
	}

	requests := make([]database.CreateApprovedRequestParams, 0, len(messages))
	ids := make([]string, 0, len(messages))
	for _, message := range messages {
		request, err := approvalRequest(message)
		if err != nil {
			return 0, err
		}
		requests = append(requests, request)
		ids = append(ids, message.ID)
	}

	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("begin approval event transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	queries := database.New(tx)
	for _, request := range requests {
		if err := queries.CreateApprovedRequest(ctx, request); err != nil {
			return 0, fmt.Errorf("persist approval event %s: %w", request.StreamID, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit approval events: %w", err)
	}

	pipe := p.redis.TxPipeline()
	pipe.XAck(ctx, p.config.Stream, p.config.Group, ids...)
	pipe.XDel(ctx, p.config.Stream, ids...)
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, fmt.Errorf("remove persisted approval events: %w", err)
	}

	return len(messages), nil
}
