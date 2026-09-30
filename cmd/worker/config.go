package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/peterintech/global-rate-limiter/internal/env"
	"github.com/peterintech/global-rate-limiter/internal/events"
)

type config struct {
	addr         string
	databaseURL  string
	redis        redisConfig
	events       events.Config
	maxBacklog   int64
	claimEvery   time.Duration
	statusEvery  time.Duration
	warningLevel int
	retryBackoff time.Duration
}

type redisConfig struct {
	addr          string
	masterName    string
	sentinelAddrs []string
	password      string
	db            int
	dialTimeout   time.Duration
	readTimeout   time.Duration
	writeTimeout  time.Duration
}

func loadConfig() (config, error) {
	redisCfg, err := loadRedisConfig()
	if err != nil {
		return config{}, err
	}

	batchSize, err := positiveInt("APPROVAL_WORKER_BATCH_SIZE", "100")
	if err != nil {
		return config{}, err
	}
	block, err := positiveDuration("APPROVAL_WORKER_BLOCK", "1s")
	if err != nil {
		return config{}, err
	}
	claimIdle, err := positiveDuration("APPROVAL_WORKER_CLAIM_IDLE", "30s")
	if err != nil {
		return config{}, err
	}
	claimEvery, err := positiveDuration("APPROVAL_WORKER_CLAIM_INTERVAL", "10s")
	if err != nil {
		return config{}, err
	}
	retryBackoff, err := positiveDuration("APPROVAL_WORKER_RETRY_BACKOFF", "1s")
	if err != nil {
		return config{}, err
	}
	statusEvery, err := positiveDuration("APPROVAL_WORKER_STATUS_INTERVAL", "5s")
	if err != nil {
		return config{}, err
	}
	maxBacklog, err := positiveInt("RATE_LIMIT_EVENT_MAX_BACKLOG", "100000")
	if err != nil {
		return config{}, err
	}
	warningLevel, err := positiveInt("APPROVAL_WORKER_BACKLOG_WARNING_PERCENT", "80")
	if err != nil || warningLevel > 100 {
		return config{}, errors.New("APPROVAL_WORKER_BACKLOG_WARNING_PERCENT must be between 1 and 100")
	}

	consumer := strings.TrimSpace(env.GetEnv("APPROVAL_WORKER_CONSUMER", ""))
	if consumer == "" {
		hostname, err := os.Hostname()
		if err != nil {
			return config{}, fmt.Errorf("read hostname for worker identity: %w", err)
		}
		consumer = fmt.Sprintf("%s-%d", hostname, os.Getpid())
	}

	databaseURL := strings.TrimSpace(env.GetEnv("DATABASE_URL", ""))
	if databaseURL == "" {
		return config{}, errors.New("DATABASE_URL is required")
	}

	return config{
		addr:        fmt.Sprintf(":%s", env.GetEnv("WORKER_PORT", "8081")),
		databaseURL: databaseURL,
		redis:       redisCfg,
		events: events.Config{
			Stream:    env.GetEnv("RATE_LIMIT_EVENT_STREAM", "rate_limit:approved_requests"),
			Group:     env.GetEnv("APPROVAL_WORKER_GROUP", "approval-history"),
			Consumer:  consumer,
			BatchSize: int64(batchSize),
			Block:     block,
			ClaimIdle: claimIdle,
		},
		claimEvery:   claimEvery,
		statusEvery:  statusEvery,
		maxBacklog:   int64(maxBacklog),
		warningLevel: warningLevel,
		retryBackoff: retryBackoff,
	}, nil
}

func loadRedisConfig() (redisConfig, error) {
	masterName := strings.TrimSpace(env.GetEnv("REDIS_MASTER_NAME", ""))
	sentinelValue := env.GetEnv("REDIS_SENTINEL_ADDRS", "")

	dialTimeout, err := positiveDuration("REDIS_DIAL_TIMEOUT", "500ms")
	if err != nil {
		return redisConfig{}, err
	}
	readTimeout, err := positiveDuration("REDIS_READ_TIMEOUT", "3s")
	if err != nil {
		return redisConfig{}, err
	}
	writeTimeout, err := positiveDuration("REDIS_WRITE_TIMEOUT", "200ms")
	if err != nil {
		return redisConfig{}, err
	}

	cfg := redisConfig{
		addr:         env.GetEnv("REDIS_ADDR", "localhost:6379"),
		masterName:   masterName,
		password:     env.GetEnv("REDIS_PASSWORD", ""),
		db:           env.GetEnvAsInt("REDIS_DB", 0),
		dialTimeout:  dialTimeout,
		readTimeout:  readTimeout,
		writeTimeout: writeTimeout,
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

func positiveDuration(name, fallback string) (time.Duration, error) {
	value := env.GetEnv(name, fallback)
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return duration, nil
}

func positiveInt(name, fallback string) (int, error) {
	value := env.GetEnv(name, fallback)
	number, err := strconv.Atoi(value)
	if err != nil || number <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return number, nil
}
