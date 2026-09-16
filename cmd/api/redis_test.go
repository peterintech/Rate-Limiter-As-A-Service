package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/peterintech/global-rate-limiter/internal/ratelimiter"
	"github.com/peterintech/global-rate-limiter/internal/store"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

func TestRedisSharesQuotaAcrossInstances(t *testing.T) {
	const limit = 10
	const requests = 50

	redisAddr := os.Getenv("REDIS_TEST_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	clients := []*redis.Client{
		store.NewRedisClient(redisAddr, "", 0),
		store.NewRedisClient(redisAddr, "", 0),
	}
	for _, client := range clients {
		t.Cleanup(func() { client.Close() })
	}
	if err := clients[0].Ping(ctx).Err(); err != nil {
		t.Skipf("Redis integration test requires Redis at %s: %v", redisAddr, err)
	}

	policies := ratelimiter.TokenBucketPolicies{
		{ClientID: "client-a", Resource: "openai"}: {Limit: limit, Window: time.Minute},
	}
	prefix := fmt.Sprintf("rate_limit_test:%d", time.Now().UnixNano())
	t.Cleanup(func() {
		keys, err := clients[0].Keys(context.Background(), prefix+":*").Result()
		if err == nil && len(keys) > 0 {
			clients[0].Del(context.Background(), keys...)
		}
	})

	applications := make([]*application, 0, len(clients))
	for _, client := range clients {
		limiter, err := ratelimiter.NewRedisTokenBucketRateLimiter(client, policies, ratelimiter.RedisTokenBucketConfig{KeyPrefix: prefix})
		if err != nil {
			t.Fatal(err)
		}

		applications = append(applications, &application{
			config: config{
				addr: ":0",
				env:  "test",
			},
			logger:      zap.NewNop().Sugar(),
			rateLimiter: limiter,
		})
	}

	muxes := []*chi.Mux{applications[0].mount(), applications[1].mount()}
	start := make(chan struct{})
	results := make(chan int, requests)

	for requestNumber := range requests {
		mux := muxes[requestNumber%len(muxes)]
		go func() {
			<-start

			request := httptest.NewRequest(
				http.MethodPost,
				"/v1/check",
				bytes.NewBufferString(`{"client_id":"client-a","resource":"openai","cost":1}`),
			)
			response := executeRequest(request, mux)
			results <- response.Code
		}()
	}

	close(start)

	allowed := 0
	rejected := 0
	for range requests {
		switch status := <-results; status {
		case http.StatusOK:
			allowed++
		case http.StatusTooManyRequests:
			rejected++
		default:
			t.Errorf("unexpected response code %d", status)
		}
	}

	checkResponse(t, "combined approvals", limit, allowed)
	checkResponse(t, "combined rejections", requests-limit, rejected)
	t.Logf("shared quota=%d; instances=%d; concurrent requests=%d; combined approvals=%d; rejected=%d", limit, len(applications), requests, allowed, rejected)

	keys, err := clients[0].Keys(context.Background(), prefix+":*").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected one shared Redis key; got %d", len(keys))
	}

	ttl, err := clients[0].PTTL(context.Background(), keys[0]).Result()
	if err != nil {
		t.Fatal(err)
	}
	if ttl <= 0 {
		t.Fatalf("expected the shared bucket to expire; got TTL %s", ttl)
	}
	t.Logf("shared bucket TTL=%s", ttl.Round(time.Second))
}
