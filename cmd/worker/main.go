package main

import (
	"context"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/peterintech/global-rate-limiter/internal/events"
	"github.com/peterintech/global-rate-limiter/internal/store"
	"go.uber.org/zap"
)

func main() {
	godotenv.Load(".env")

	logger := zap.Must(zap.NewProduction()).Sugar()
	defer logger.Sync()

	cfg, err := loadConfig()
	if err != nil {
		logger.Fatalw("invalid worker configuration", "error", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	redisClient := newRedisClient(cfg.redis)
	defer redisClient.Close()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		logger.Fatalw("failed to connect to Redis", "error", err)
	}

	postgresPool, err := store.NewPostgresPool(ctx, cfg.databaseURL)
	if err != nil {
		logger.Fatalw("failed to connect to PostgreSQL", "error", err)
	}
	defer postgresPool.Close()

	processor, err := events.NewProcessor(redisClient, postgresPool, cfg.events)
	if err != nil {
		logger.Fatalw("failed to create approval event processor", "error", err)
	}

	app := &application{config: cfg, logger: logger, processor: processor}
	if err := app.run(ctx); err != nil {
		logger.Fatalw("approval worker stopped unexpectedly", "error", err)
	}
}
