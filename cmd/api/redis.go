package main

import (
	"github.com/peterintech/global-rate-limiter/internal/store"
	"github.com/redis/go-redis/v9"
)

func newRedisClient(cfg redisConfig) *redis.Client {
	if cfg.masterName != "" {
		return store.NewRedisFailoverClient(cfg.masterName, cfg.sentinelAddrs, cfg.password, cfg.db)
	}

	return store.NewRedisClient(cfg.addr, cfg.password, cfg.db)
}
