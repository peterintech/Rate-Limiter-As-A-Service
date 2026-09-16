package main

import (
	"fmt"
	"time"

	"github.com/joho/godotenv"
	"github.com/peterintech/global-rate-limiter/internal/env"
	"github.com/peterintech/global-rate-limiter/internal/ratelimiter"
	"go.uber.org/zap"
)

const version = "0.1.0"

func main() {
	godotenv.Load(".env")

	cfg := config{
		addr: fmt.Sprintf(":%s", env.GetEnv("PORT", "8080")),
		env:  env.GetEnv("ENV", "development"),
		tokenBucketPolicies: ratelimiter.TokenBucketPolicies{
			{ClientID: "client-a", Resource: "openai"}: {Limit: 100, Window: time.Minute},
			{ClientID: "client-b", Resource: "stripe"}: {Limit: 5000, Window: time.Minute},
		},
	}

	logger := zap.Must(zap.NewProduction()).Sugar()
	defer logger.Sync()

	limiter, err := ratelimiter.NewTokenBucketRateLimiter(cfg.tokenBucketPolicies, time.Now)
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
