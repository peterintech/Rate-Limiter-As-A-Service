package main

import (
	"context"

	"github.com/joho/godotenv"
	"github.com/peterintech/global-rate-limiter/internal/ratelimiter"
	"github.com/peterintech/global-rate-limiter/internal/store"
	"go.uber.org/zap"
)

const version = "0.1.0"

func main() {
	godotenv.Load(".env")

	logger := zap.Must(zap.NewProduction()).Sugar()
	defer logger.Sync()

	cfg, err := loadConfig()
	if err != nil {
		logger.Fatalw("invalid application configuration", "error", err)
	}

	redisClient := newRedisClient(cfg.redisCfg)
	defer redisClient.Close()

	ctx, cancel := context.WithTimeout(context.Background(), cfg.circuitBreakerCfg.DecisionTimeout)
	defer cancel()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		logger.Fatalw("failed to connect to Redis", "error", err)
	}
	postgresPool, err := store.OpenPostgresPool(context.Background(), cfg.databaseURL)
	if err != nil {
		logger.Fatalw("invalid PostgreSQL configuration", "error", err)
	}
	defer postgresPool.Close()

	redisLimiter, err := ratelimiter.NewRedisTokenBucketRateLimiter(redisClient, cfg.tokenBucketPolicies, cfg.redisLimiterCfg)
	if err != nil {
		logger.Fatalw("failed to create rate limiter", "error", err)
	}
	limiter, err := ratelimiter.NewCircuitBreakerLimiter(redisLimiter, cfg.circuitBreakerCfg)
	if err != nil {
		logger.Fatalw("failed to protect rate limiter", "error", err)
	}

	app := &application{
		config:         cfg,
		logger:         logger,
		rateLimiter:    limiter,
		readinessCheck: newRedisReadinessCheck(redisClient, limiter, cfg.circuitBreakerCfg.DecisionTimeout),
		database:       postgresPool,
	}

	if err := app.run(app.mount()); err != nil {
		logger.Fatalw("server stopped unexpectedly", "error", err)
	}
}
