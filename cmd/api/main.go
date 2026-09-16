package main

import (
	"context"
	"fmt"
	"time"

	"github.com/joho/godotenv"
	"github.com/peterintech/global-rate-limiter/internal/env"
	"github.com/peterintech/global-rate-limiter/internal/ratelimiter"
	"github.com/peterintech/global-rate-limiter/internal/store"
	"go.uber.org/zap"
)

const version = "0.1.0"

func main() {
	godotenv.Load(".env")

	logger := zap.Must(zap.NewProduction()).Sugar()
	defer logger.Sync()

	cfg := config{
		addr: fmt.Sprintf(":%s", env.GetEnv("PORT", "8080")),
		env:  env.GetEnv("ENV", "development"),
		redisCfg: redisConfig{
			addr:     env.GetEnv("REDIS_ADDR", "localhost:6379"),
			password: env.GetEnv("REDIS_PASSWORD", ""),
			db:       env.GetEnvAsInt("REDIS_DB", 0),
		},
		tokenBucketPolicies: ratelimiter.TokenBucketPolicies{
			{ClientID: "client-a", Resource: "openai"}: {Limit: 100, Window: time.Minute},
			{ClientID: "client-b", Resource: "stripe"}: {Limit: 5000, Window: time.Minute},
		},
	}

	redisClient := store.NewRedisClient(cfg.redisCfg.addr, cfg.redisCfg.password, cfg.redisCfg.db)
	defer redisClient.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		logger.Fatalw("failed to connect to Redis", "addr", cfg.redisCfg.addr, "error", err)
	}

	limiter, err := ratelimiter.NewRedisTokenBucketRateLimiter(redisClient, cfg.tokenBucketPolicies, ratelimiter.RedisTokenBucketConfig{})
	if err != nil {
		logger.Fatalw("failed to create rate limiter", "error", err)
	}

	app := &application{
		config:      cfg,
		logger:      logger,
		rateLimiter: limiter,
	}

	if err := app.run(app.mount()); err != nil {
		logger.Fatalw("server stopped unexpectedly", "error", err)
	}
}
