package store

import (
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisOptions struct {
	Password     string
	DB           int
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

func NewRedisClient(addr string, options RedisOptions) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     options.Password,
		DB:           options.DB,
		DialTimeout:  options.DialTimeout,
		ReadTimeout:  options.ReadTimeout,
		WriteTimeout: options.WriteTimeout,
		MaxRetries:   -1,
	})
}

func NewRedisFailoverClient(masterName string, sentinelAddrs []string, options RedisOptions) *redis.Client {
	return redis.NewFailoverClient(&redis.FailoverOptions{
		MasterName:    masterName,
		SentinelAddrs: sentinelAddrs,
		Password:      options.Password,
		DB:            options.DB,
		DialTimeout:   options.DialTimeout,
		ReadTimeout:   options.ReadTimeout,
		WriteTimeout:  options.WriteTimeout,
		MaxRetries:    -1,
	})
}
