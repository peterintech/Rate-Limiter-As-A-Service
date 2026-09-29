package events

import (
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peterintech/global-rate-limiter/internal/database"
	"github.com/redis/go-redis/v9"
)

func approvalRequest(message redis.XMessage) (database.CreateApprovedRequestParams, error) {
	cost, err := integerField(message, "cost")
	if err != nil {
		return database.CreateApprovedRequestParams{}, err
	}

	approvedAtMS, err := integerField(message, "approved_at_ms")
	if err != nil {
		return database.CreateApprovedRequestParams{}, err
	}

	remaining, err := integerField(message, "remaining")
	if err != nil {
		return database.CreateApprovedRequestParams{}, err
	}

	clientID, err := stringField(message, "client_id")
	if err != nil {
		return database.CreateApprovedRequestParams{}, err
	}

	resource, err := stringField(message, "resource")
	if err != nil {
		return database.CreateApprovedRequestParams{}, err
	}

	if cost <= 0 || cost > math.MaxInt32 {
		return database.CreateApprovedRequestParams{}, fmt.Errorf("approval event %s has out-of-range cost", message.ID)
	}
	if remaining < 0 || remaining > math.MaxInt32 {
		return database.CreateApprovedRequestParams{}, fmt.Errorf("approval event %s has out-of-range remaining", message.ID)
	}

	return database.CreateApprovedRequestParams{
		StreamID:   message.ID,
		ClientID:   clientID,
		Resource:   resource,
		Cost:       int32(cost),
		ApprovedAt: pgtype.Timestamptz{Time: time.UnixMilli(approvedAtMS), Valid: true},
		Remaining:  int32(remaining),
	}, nil
}

func stringField(message redis.XMessage, field string) (string, error) {
	value, ok := message.Values[field]
	if !ok || fmt.Sprint(value) == "" {
		return "", fmt.Errorf("approval event %s has no %s", message.ID, field)
	}
	return fmt.Sprint(value), nil
}

func integerField(message redis.XMessage, field string) (int64, error) {
	value, err := stringField(message, field)
	if err != nil {
		return 0, err
	}
	number, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("approval event %s has invalid %s: %w", message.ID, field, err)
	}
	return number, nil
}
