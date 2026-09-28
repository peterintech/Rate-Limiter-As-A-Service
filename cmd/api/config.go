package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/peterintech/global-rate-limiter/internal/env"
	"github.com/peterintech/global-rate-limiter/internal/ratelimiter"
)

type config struct {
	addr                string
	env                 string
	redisCfg            redisConfig
	tokenBucketPolicies ratelimiter.TokenBucketPolicies
}

type redisConfig struct {
	addr          string
	masterName    string
	sentinelAddrs []string
	password      string
	db            int
}

func loadConfig() (config, error) {
	redisCfg, err := loadRedisConfig()
	if err != nil {
		return config{}, err
	}

	return config{
		addr:     fmt.Sprintf(":%s", env.GetEnv("PORT", "8080")),
		env:      env.GetEnv("ENV", "development"),
		redisCfg: redisCfg,
		tokenBucketPolicies: ratelimiter.TokenBucketPolicies{
			{ClientID: "client-a", Resource: "openai"}: {Limit: 100, Window: time.Minute},
			{ClientID: "client-b", Resource: "stripe"}: {Limit: 5000, Window: time.Minute},
		},
	}, nil
}

func loadRedisConfig() (redisConfig, error) {
	masterName := strings.TrimSpace(env.GetEnv("REDIS_MASTER_NAME", ""))
	sentinelValue := env.GetEnv("REDIS_SENTINEL_ADDRS", "")

	cfg := redisConfig{
		addr:       env.GetEnv("REDIS_ADDR", "localhost:6379"),
		masterName: masterName,
		password:   env.GetEnv("REDIS_PASSWORD", ""),
		db:         env.GetEnvAsInt("REDIS_DB", 0),
	}

	if masterName == "" && strings.TrimSpace(sentinelValue) == "" {
		return cfg, nil
	}
	if masterName == "" || strings.TrimSpace(sentinelValue) == "" {
		return redisConfig{}, errors.New("REDIS_MASTER_NAME and REDIS_SENTINEL_ADDRS must be configured together")
	}

	for _, addr := range strings.Split(sentinelValue, ",") {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			return redisConfig{}, errors.New("REDIS_SENTINEL_ADDRS cannot contain an empty address")
		}
		cfg.sentinelAddrs = append(cfg.sentinelAddrs, addr)
	}

	return cfg, nil
}
