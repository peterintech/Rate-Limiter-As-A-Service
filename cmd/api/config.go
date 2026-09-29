package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/peterintech/global-rate-limiter/internal/env"
	"github.com/peterintech/global-rate-limiter/internal/ratelimiter"
)

type config struct {
	addr                string
	env                 string
	redisCfg            redisConfig
	circuitBreakerCfg   ratelimiter.CircuitBreakerConfig
	tokenBucketPolicies ratelimiter.TokenBucketPolicies
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
	circuitBreakerCfg, err := loadCircuitBreakerConfig()
	if err != nil {
		return config{}, err
	}

	return config{
		addr:              fmt.Sprintf(":%s", env.GetEnv("PORT", "8080")),
		env:               env.GetEnv("ENV", "development"),
		redisCfg:          redisCfg,
		circuitBreakerCfg: circuitBreakerCfg,
		tokenBucketPolicies: ratelimiter.TokenBucketPolicies{
			{ClientID: "client-a", Resource: "openai"}: {Limit: 100, Window: time.Minute},
			{ClientID: "client-b", Resource: "stripe"}: {Limit: 5000, Window: time.Minute},
		},
	}, nil
}

func loadRedisConfig() (redisConfig, error) {
	masterName := strings.TrimSpace(env.GetEnv("REDIS_MASTER_NAME", ""))
	sentinelValue := env.GetEnv("REDIS_SENTINEL_ADDRS", "")

	dialTimeout, err := positiveDuration("REDIS_DIAL_TIMEOUT", "500ms")
	if err != nil {
		return redisConfig{}, err
	}
	readTimeout, err := positiveDuration("REDIS_READ_TIMEOUT", "200ms")
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

func loadCircuitBreakerConfig() (ratelimiter.CircuitBreakerConfig, error) {
	failureThreshold, err := positiveInt("CIRCUIT_BREAKER_FAILURE_THRESHOLD", "3")
	if err != nil {
		return ratelimiter.CircuitBreakerConfig{}, err
	}
	openTimeout, err := positiveDuration("CIRCUIT_BREAKER_OPEN_TIMEOUT", "5s")
	if err != nil {
		return ratelimiter.CircuitBreakerConfig{}, err
	}
	decisionTimeout, err := positiveDuration("RATE_LIMIT_DECISION_TIMEOUT", "300ms")
	if err != nil {
		return ratelimiter.CircuitBreakerConfig{}, err
	}

	return ratelimiter.CircuitBreakerConfig{
		FailureThreshold: failureThreshold,
		OpenTimeout:      openTimeout,
		DecisionTimeout:  decisionTimeout,
	}, nil
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
