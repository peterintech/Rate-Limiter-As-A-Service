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
)

func TestRedisSharesQuotaAcrossInstances(t *testing.T) {
	const limit = 10
	const requests = 50

	clients := []*redis.Client{
		newRedisTestClient(t),
		newRedisTestClient(t),
	}
	policies := newRedisTestPolicies(limit)
	prefix := newRedisTestPrefix(t, clients[0], "shared_quota")
	muxes := make([]*chi.Mux, 0, len(clients))
	for _, client := range clients {
		muxes = append(muxes, newRedisTestMux(t, client, policies, prefix))
	}

	allowed, rejected := sendConcurrentRateLimitChecks(t, muxes, requests)

	checkResponse(t, "combined approvals", limit, allowed)
	checkResponse(t, "combined rejections", requests-limit, rejected)
	ttl := checkRedisBucketExpiry(t, clients[0], prefix)

	t.Logf("shared quota=%d; instances=%d; concurrent requests=%d; combined approvals=%d; rejected=%d", limit, len(muxes), requests, allowed, rejected)
	t.Logf("shared bucket TTL=%s", ttl.Round(time.Second))
}

func TestUnavailableRedisStopsRateLimitChecks(t *testing.T) {
	adminClient := newRedisTestClient(t)
	limiterClient := newRedisTestClient(t)
	policies := newRedisTestPolicies(10)
	prefix := newRedisTestPrefix(t, adminClient, "unavailable")
	mux := newRedisTestMux(t, limiterClient, policies, prefix)

	response := executeRateLimitCheck(mux)
	checkResponse(t, "response before Redis becomes unavailable", http.StatusOK, response.Code)

	if err := limiterClient.Close(); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	response = executeRateLimitCheck(mux)
	failureLatency := time.Since(started)
	checkResponse(t, "response while Redis is unavailable", http.StatusInternalServerError, response.Code)

	const expectedBody = "{\"error\":\"rate-limit check failed\"}\n"
	if body := response.Body.String(); body != expectedBody {
		t.Errorf("expected bounded outage response %q; got %q", expectedBody, body)
	}

	healthRequest := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	healthResponse := executeRequest(healthRequest, mux)
	checkResponse(t, "health response while Redis is unavailable", http.StatusOK, healthResponse.Code)

	t.Logf(
		"before dependency failure=%d; while dependency is unavailable=%d; failure latency=%s; process health=%d",
		http.StatusOK,
		response.Code,
		failureLatency.Round(time.Microsecond),
		healthResponse.Code,
	)
}

func newRedisTestClient(t *testing.T) *redis.Client {
	t.Helper()

	redisAddr := os.Getenv("REDIS_TEST_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}

	client := store.NewRedisClient(redisAddr, "", 0)
	t.Cleanup(func() { client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis integration test requires Redis at %s: %v", redisAddr, err)
	}

	return client
}

func newRedisTestPolicies(limit int) ratelimiter.TokenBucketPolicies {
	return ratelimiter.TokenBucketPolicies{
		{ClientID: "client-a", Resource: "openai"}: {Limit: limit, Window: time.Minute},
	}
}

func newRedisTestPrefix(t *testing.T, client *redis.Client, name string) string {
	t.Helper()

	prefix := fmt.Sprintf("rate_limit_%s_test:%d", name, time.Now().UnixNano())
	t.Cleanup(func() {
		keys, err := client.Keys(context.Background(), prefix+":*").Result()
		if err == nil && len(keys) > 0 {
			client.Del(context.Background(), keys...)
		}
	})

	return prefix
}

func newRedisTestMux(t *testing.T, client *redis.Client, policies ratelimiter.TokenBucketPolicies, prefix string) *chi.Mux {
	t.Helper()

	limiter, err := ratelimiter.NewRedisTokenBucketRateLimiter(
		client,
		policies,
		ratelimiter.RedisTokenBucketConfig{KeyPrefix: prefix},
	)
	if err != nil {
		t.Fatal(err)
	}

	app := newTestApplicationWithLimiter(config{addr: ":0", env: "test"}, limiter)
	return app.mount()
}

func executeRateLimitCheck(mux *chi.Mux) *httptest.ResponseRecorder {
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/check",
		bytes.NewBufferString(`{"client_id":"client-a","resource":"openai","cost":1}`),
	)
	return executeRequest(request, mux)
}

func sendConcurrentRateLimitChecks(t *testing.T, muxes []*chi.Mux, requests int) (int, int) {
	t.Helper()

	start := make(chan struct{})
	results := make(chan int, requests)

	for requestNumber := range requests {
		mux := muxes[requestNumber%len(muxes)]
		go func(mux *chi.Mux) {
			<-start
			results <- executeRateLimitCheck(mux).Code
		}(mux)
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

	return allowed, rejected
}

func checkRedisBucketExpiry(t *testing.T, client *redis.Client, prefix string) time.Duration {
	t.Helper()

	keys, err := client.Keys(context.Background(), prefix+":*").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected one shared Redis key; got %d", len(keys))
	}

	ttl, err := client.PTTL(context.Background(), keys[0]).Result()
	if err != nil {
		t.Fatal(err)
	}
	if ttl <= 0 {
		t.Fatalf("expected the shared bucket to expire; got TTL %s", ttl)
	}

	return ttl
}
