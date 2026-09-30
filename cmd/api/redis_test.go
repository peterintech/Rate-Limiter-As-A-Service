package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
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

func TestRedisRecordsApprovedRequests(t *testing.T) {
	const limit = 3

	clients := []*redis.Client{
		newRedisTestClient(t),
		newRedisTestClient(t),
	}
	policies := newRedisTestPolicies(limit)
	prefix := newRedisTestPrefix(t, clients[0], "approved_events")
	muxes := []*chi.Mux{
		newRedisTestMux(t, clients[0], policies, prefix),
		newRedisTestMux(t, clients[1], policies, prefix),
	}

	checkResponse(t, "weighted approval", http.StatusOK, executeRateLimitCheckWithCost(muxes[0], 2).Code)
	checkResponse(t, "approval through second instance", http.StatusOK, executeRateLimitCheckWithCost(muxes[1], 1).Code)
	checkResponse(t, "rejected request", http.StatusTooManyRequests, executeRateLimitCheckWithCost(muxes[0], 1).Code)

	events, err := clients[0].XRange(context.Background(), redisTestEventStream(prefix), "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("expected one event for each of two approvals; got %d", len(events))
	}

	checkStreamField(t, events[0], "client_id", "client-a")
	checkStreamField(t, events[0], "resource", "openai")
	checkStreamField(t, events[0], "cost", "2")
	checkStreamField(t, events[0], "remaining", "1")
	checkStreamField(t, events[1], "cost", "1")
	checkStreamField(t, events[1], "remaining", "0")
	for _, event := range events {
		approvedAt, err := strconv.ParseInt(fmt.Sprint(event.Values["approved_at_ms"]), 10, 64)
		if err != nil || approvedAt <= 0 {
			t.Errorf("event %s has invalid approval timestamp %q", event.ID, event.Values["approved_at_ms"])
		}
	}

	t.Logf("two API instances approved two weighted requests; Redis recorded two events; the rejected request recorded none")
}

func TestRedisStopsApprovalsWhenEventBacklogIsFull(t *testing.T) {
	client := newRedisTestClient(t)
	prefix := newRedisTestPrefix(t, client, "backlog_full")
	mux := newRedisTestMuxWithBacklog(t, client, newRedisTestPolicies(3), prefix, 2)

	checkResponse(t, "first approval", http.StatusOK, executeRateLimitCheck(mux).Code)
	checkResponse(t, "second approval", http.StatusOK, executeRateLimitCheck(mux).Code)
	checkResponse(t, "quota rejection while backlog is full", http.StatusTooManyRequests, executeRateLimitCheckWithCost(mux, 2).Code)
	tokensBeforeBlockedApproval := redisTokens(t, client, prefix)
	checkResponse(t, "approval blocked by full backlog", http.StatusServiceUnavailable, executeRateLimitCheck(mux).Code)

	checkResponse(t, "events retained at configured capacity", 2, redisStreamLength(t, client, redisTestEventStream(prefix)))
	if tokensAfterBlockedApproval := redisTokens(t, client, prefix); tokensAfterBlockedApproval != tokensBeforeBlockedApproval {
		t.Errorf("blocked approval changed tokens from %s to %s", tokensBeforeBlockedApproval, tokensAfterBlockedApproval)
	}

	events, err := client.XRangeN(context.Background(), redisTestEventStream(prefix), "-", "+", 1).Result()
	if err != nil {
		t.Fatal(err)
	}
	if err := client.XDel(context.Background(), redisTestEventStream(prefix), events[0].ID).Err(); err != nil {
		t.Fatal(err)
	}

	checkResponse(t, "approval after backlog drains", http.StatusOK, executeRateLimitCheck(mux).Code)
	t.Log("full backlog returned 503 without spending the final token; approval resumed after one event was removed")
}

func TestRedisBacklogLimitIsSharedAcrossInstances(t *testing.T) {
	const maxBacklog = 5
	const requests = 20

	clients := []*redis.Client{newRedisTestClient(t), newRedisTestClient(t)}
	prefix := newRedisTestPrefix(t, clients[0], "shared_backlog")
	policies := newRedisTestPolicies(requests)
	muxes := []*chi.Mux{
		newRedisTestMuxWithBacklog(t, clients[0], policies, prefix, maxBacklog),
		newRedisTestMuxWithBacklog(t, clients[1], policies, prefix, maxBacklog),
	}

	start := make(chan struct{})
	statuses := make(chan int, requests)
	for requestNumber := range requests {
		mux := muxes[requestNumber%len(muxes)]
		go func() {
			<-start
			statuses <- executeRateLimitCheck(mux).Code
		}()
	}
	close(start)

	approved := 0
	blocked := 0
	for range requests {
		switch status := <-statuses; status {
		case http.StatusOK:
			approved++
		case http.StatusServiceUnavailable:
			blocked++
		default:
			t.Errorf("unexpected response code %d", status)
		}
	}

	checkResponse(t, "combined approvals", maxBacklog, approved)
	checkResponse(t, "combined backlog rejections", requests-maxBacklog, blocked)
	checkResponse(t, "shared event backlog", maxBacklog, redisStreamLength(t, clients[0], redisTestEventStream(prefix)))
	t.Logf("instances=%d; concurrent requests=%d; backlog capacity=%d; approvals=%d; blocked=%d", len(muxes), requests, maxBacklog, approved, blocked)
}

func TestUnavailableRedisFailsClosed(t *testing.T) {
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
	checkResponse(t, "response while Redis is unavailable", http.StatusServiceUnavailable, response.Code)

	const expectedBody = "{\"error\":\"rate-limit service temporarily unavailable\"}\n"
	if body := response.Body.String(); body != expectedBody {
		t.Errorf("expected bounded outage response %q; got %q", expectedBody, body)
	}

	healthRequest := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	healthResponse := executeRequest(healthRequest, mux)

	checkResponse(t, "health response while Redis is unavailable", http.StatusOK, healthResponse.Code)

	readinessRequest := httptest.NewRequest(http.MethodGet, "/v1/readiness", nil)
	readinessResponse := executeRequest(readinessRequest, mux)

	checkResponse(t, "readiness response while Redis is unavailable", http.StatusServiceUnavailable, readinessResponse.Code)

	t.Logf(
		"before dependency failure=%d; while dependency is unavailable=%d; failure latency=%s; process health=%d; readiness=%d",
		http.StatusOK,
		response.Code,
		failureLatency.Round(time.Microsecond),
		healthResponse.Code,
		readinessResponse.Code,
	)
}

func newRedisTestClient(t *testing.T) *redis.Client {
	t.Helper()

	redisAddr := os.Getenv("REDIS_TEST_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}

	client := store.NewRedisClient(redisAddr, store.RedisOptions{
		DialTimeout:  time.Second,
		ReadTimeout:  time.Second,
		WriteTimeout: time.Second,
	})
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
		keys, err := client.Keys(context.Background(), prefix+"*").Result()
		if err == nil && len(keys) > 0 {
			client.Del(context.Background(), keys...)
		}
	})

	return prefix
}

func newRedisTestMux(t *testing.T, client *redis.Client, policies ratelimiter.TokenBucketPolicies, prefix string) *chi.Mux {
	return newRedisTestMuxWithBacklog(t, client, policies, prefix, 100000)
}

func newRedisTestMuxWithBacklog(t *testing.T, client *redis.Client, policies ratelimiter.TokenBucketPolicies, prefix string, maxBacklog int) *chi.Mux {
	t.Helper()

	limiter, err := ratelimiter.NewRedisTokenBucketRateLimiter(
		client,
		policies,
		ratelimiter.RedisTokenBucketConfig{
			KeyPrefix:       prefix,
			EventStream:     redisTestEventStream(prefix),
			MaxEventBacklog: maxBacklog,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	breakerConfig := ratelimiter.CircuitBreakerConfig{
		FailureThreshold: 3,
		OpenTimeout:      time.Second,
		DecisionTimeout:  time.Second,
	}
	protectedLimiter, err := ratelimiter.NewCircuitBreakerLimiter(limiter, breakerConfig)
	if err != nil {
		t.Fatal(err)
	}

	app := newTestApplicationWithLimiter(config{
		addr:              ":0",
		env:               "test",
		circuitBreakerCfg: breakerConfig,
	}, protectedLimiter)
	app.readinessCheck = newRedisReadinessCheck(client, protectedLimiter, breakerConfig.DecisionTimeout)
	return app.mount()
}

func redisStreamLength(t *testing.T, client *redis.Client, stream string) int {
	t.Helper()

	length, err := client.XLen(context.Background(), stream).Result()
	if err != nil {
		t.Fatal(err)
	}
	return int(length)
}

func redisTokens(t *testing.T, client *redis.Client, prefix string) string {
	t.Helper()

	keys, err := client.Keys(context.Background(), prefix+":*").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected one bucket key; got %d", len(keys))
	}
	tokens, err := client.HGet(context.Background(), keys[0], "tokens").Result()
	if err != nil {
		t.Fatal(err)
	}
	return tokens
}

func executeRateLimitCheck(mux *chi.Mux) *httptest.ResponseRecorder {
	return executeRateLimitCheckWithCost(mux, 1)
}

func executeRateLimitCheckWithCost(mux *chi.Mux, cost int) *httptest.ResponseRecorder {
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/check",
		bytes.NewBufferString(fmt.Sprintf(`{"client_id":"client-a","resource":"openai","cost":%d}`, cost)),
	)
	return executeRequest(request, mux)
}

func redisTestEventStream(prefix string) string {
	return prefix + "_approved_requests"
}

func checkStreamField(t *testing.T, event redis.XMessage, field, expected string) {
	t.Helper()

	actual := fmt.Sprint(event.Values[field])
	if actual != expected {
		t.Errorf("event %s: expected %s=%q; got %q", event.ID, field, expected, actual)
	}
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
