package events

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type Config struct {
	Stream    string
	Group     string
	Consumer  string
	BatchSize int64
	Block     time.Duration
	ClaimIdle time.Duration
}

type Processor struct {
	redis  *redis.Client
	pool   *pgxpool.Pool
	config Config
}

func NewProcessor(redisClient *redis.Client, pool *pgxpool.Pool, config Config) (*Processor, error) {
	if redisClient == nil {
		return nil, errors.New("redis client is required")
	}
	if pool == nil {
		return nil, errors.New("postgres pool is required")
	}
	if config.Stream == "" || config.Group == "" || config.Consumer == "" {
		return nil, errors.New("stream, group, and consumer are required")
	}
	if config.BatchSize <= 0 || config.Block <= 0 || config.ClaimIdle <= 0 {
		return nil, errors.New("batch size, block duration, and claim idle duration must be positive")
	}

	return &Processor{redis: redisClient, pool: pool, config: config}, nil
}

func (p *Processor) EnsureGroup(ctx context.Context) error {
	err := p.redis.XGroupCreateMkStream(ctx, p.config.Stream, p.config.Group, "0").Err()
	if err != nil && !errors.Is(err, redis.Nil) && !isBusyGroup(err) {
		return fmt.Errorf("create approval consumer group: %w", err)
	}
	return nil
}

func (p *Processor) ConsumeNew(ctx context.Context) (int, error) {
	streams, err := p.redis.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    p.config.Group,
		Consumer: p.config.Consumer,
		Streams:  []string{p.config.Stream, ">"},
		Count:    p.config.BatchSize,
		Block:    p.config.Block,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read approval events: %w", err)
	}

	return p.persistStreams(ctx, streams)
}

func (p *Processor) RecoverPending(ctx context.Context) (int, error) {
	messages, _, err := p.redis.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   p.config.Stream,
		Group:    p.config.Group,
		Consumer: p.config.Consumer,
		MinIdle:  p.config.ClaimIdle,
		Start:    "0-0",
		Count:    p.config.BatchSize,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("claim abandoned approval events: %w", err)
	}
	if len(messages) == 0 {
		return 0, nil
	}

	return p.persistMessages(ctx, messages)
}

func (p *Processor) persistStreams(ctx context.Context, streams []redis.XStream) (int, error) {
	processed := 0
	for _, stream := range streams {
		count, err := p.persistMessages(ctx, stream.Messages)
		if err != nil {
			return processed, err
		}
		processed += count
	}
	return processed, nil
}

func isBusyGroup(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "BUSYGROUP")
}
