package main

import (
	"github.com/peterintech/global-rate-limiter/internal/store"
	"github.com/redis/go-redis/v9"
)

func newRedisClient(cfg redisConfig) *redis.Client {
	options := store.RedisOptions{
		Password:     cfg.password,
		DB:           cfg.db,
		DialTimeout:  cfg.dialTimeout,
		ReadTimeout:  cfg.readTimeout,
		WriteTimeout: cfg.writeTimeout,
	}
	if cfg.masterName != "" {
		return store.NewRedisFailoverClient(cfg.masterName, cfg.sentinelAddrs, options)
	}
	return store.NewRedisClient(cfg.addr, options)
}
