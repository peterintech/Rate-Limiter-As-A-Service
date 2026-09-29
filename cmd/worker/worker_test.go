package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterintech/global-rate-limiter/internal/database"
	"github.com/peterintech/global-rate-limiter/internal/events"
	"github.com/peterintech/global-rate-limiter/internal/store"
	"github.com/redis/go-redis/v9"
)

func TestWorkerPersistsAndRemovesApprovalEvent(t *testing.T) {
	redisClient, postgresPool, stream := newWorkerTestDependencies(t)
	processor := newTestProcessor(t, redisClient, postgresPool, stream, "active-worker", time.Minute)

	if err := processor.EnsureGroup(context.Background()); err != nil {
		t.Fatal(err)
	}
	eventID := addApprovalEvent(t, redisClient, stream)

	processed, err := processor.ConsumeNew(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	checkCount(t, "processed events", 1, int64(processed))
	checkStoredOnce(t, postgresPool, eventID)
	checkStreamDrained(t, redisClient, stream)
	t.Logf("event %s was committed to PostgreSQL before it was removed from Redis", eventID)
}

func TestWorkerRecoversPendingEventWithoutDuplicatingHistory(t *testing.T) {
	redisClient, postgresPool, stream := newWorkerTestDependencies(t)
	processor := newTestProcessor(t, redisClient, postgresPool, stream, "replacement-worker", time.Millisecond)

	if err := processor.EnsureGroup(context.Background()); err != nil {
		t.Fatal(err)
	}
	eventID := addApprovalEvent(t, redisClient, stream)
	deliverToStoppedWorker(t, redisClient, stream)
	persistApprovalEvent(t, postgresPool, eventID, stream)
	time.Sleep(2 * time.Millisecond)

	processed, err := processor.RecoverPending(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	checkCount(t, "recovered events", 1, int64(processed))
	checkStoredOnce(t, postgresPool, eventID)
	checkStreamDrained(t, redisClient, stream)
	t.Logf("replacement worker recovered %s; its existing PostgreSQL row prevented a duplicate", eventID)
}

func TestWorkerStatusReportsItsBacklog(t *testing.T) {
	redisClient, postgresPool, stream := newWorkerTestDependencies(t)
	processor := newTestProcessor(t, redisClient, postgresPool, stream, "status-worker", time.Minute)

	if err := processor.EnsureGroup(context.Background()); err != nil {
		t.Fatal(err)
	}
	addApprovalEvent(t, redisClient, stream)
	deliverToStoppedWorker(t, redisClient, stream)

	app := &application{
		config:    config{maxBacklog: 10},
		processor: processor,
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	response := httptest.NewRecorder()
	app.mount().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status code %d; got %d", http.StatusOK, response.Code)
	}
	var status statusResponse
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	checkCount(t, "stream length", 1, status.StreamLength)
	checkCount(t, "pending events", 1, status.PendingEvents)
	checkCount(t, "configured limit", 10, status.ConfiguredLimit)
	if status.CapacityUsedPercent != 10 {
		t.Errorf("expected capacity used=10%%; got %.2f%%", status.CapacityUsedPercent)
	}
	if status.OldestEventAgeMS < 0 {
		t.Errorf("expected a non-negative oldest event age; got %d", status.OldestEventAgeMS)
	}

	t.Logf("worker reports stream=%d, pending=%d, used=%.0f%%", status.StreamLength, status.PendingEvents, status.CapacityUsedPercent)
}

func newWorkerTestDependencies(t *testing.T) (*redis.Client, *pgxpool.Pool, string) {
	t.Helper()

	redisAddr := os.Getenv("REDIS_TEST_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}
	redisClient := store.NewRedisClient(redisAddr, store.RedisOptions{
		DialTimeout:  time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: time.Second,
	})
	t.Cleanup(func() { redisClient.Close() })
	if err := redisClient.Ping(context.Background()).Err(); err != nil {
		t.Skipf("worker integration test requires Redis at %s: %v", redisAddr, err)
	}

	databaseURL := os.Getenv("DATABASE_TEST_URL")
	if databaseURL == "" {
		databaseURL = "postgres://rate_limiter:rate_limiter@localhost:5433/rate_limiter?sslmode=disable"
	}
	postgresPool, err := store.NewPostgresPool(context.Background(), databaseURL)
	if err != nil {
		t.Skipf("worker integration test requires PostgreSQL at DATABASE_TEST_URL: %v", err)
	}
	t.Cleanup(postgresPool.Close)
	if _, err := postgresPool.Exec(context.Background(), "SELECT 1 FROM approved_requests LIMIT 1"); err != nil {
		t.Skipf("worker integration test requires applied Goose migrations: %v", err)
	}

	stream := fmt.Sprintf("rate_limit:worker_test:%d", time.Now().UnixNano())
	t.Cleanup(func() {
		redisClient.Del(context.Background(), stream)
		postgresPool.Exec(context.Background(), "DELETE FROM approved_requests WHERE resource = $1", stream)
	})

	return redisClient, postgresPool, stream
}

func newTestProcessor(t *testing.T, redisClient *redis.Client, postgresPool *pgxpool.Pool, stream, consumer string, claimIdle time.Duration) *events.Processor {
	t.Helper()

	processor, err := events.NewProcessor(redisClient, postgresPool, events.Config{
		Stream:    stream,
		Group:     "approval-history-test",
		Consumer:  consumer,
		BatchSize: 10,
		Block:     50 * time.Millisecond,
		ClaimIdle: claimIdle,
	})
	if err != nil {
		t.Fatal(err)
	}
	return processor
}

func addApprovalEvent(t *testing.T, client *redis.Client, stream string) string {
	t.Helper()

	eventID, err := client.XAdd(context.Background(), &redis.XAddArgs{
		Stream: stream,
		Values: map[string]any{
			"client_id":      stream,
			"resource":       stream,
			"cost":           "2",
			"approved_at_ms": fmt.Sprint(time.Now().UnixMilli()),
			"remaining":      "8",
		},
	}).Result()
	if err != nil {
		t.Fatal(err)
	}
	return eventID
}

func deliverToStoppedWorker(t *testing.T, client *redis.Client, stream string) {
	t.Helper()

	streams, err := client.XReadGroup(context.Background(), &redis.XReadGroupArgs{
		Group:    "approval-history-test",
		Consumer: "stopped-worker",
		Streams:  []string{stream, ">"},
		Count:    1,
	}).Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(streams) != 1 || len(streams[0].Messages) != 1 {
		t.Fatalf("expected stopped worker to leave one pending event")
	}
}

func persistApprovalEvent(t *testing.T, pool *pgxpool.Pool, eventID, resource string) {
	t.Helper()

	err := database.New(pool).CreateApprovedRequest(context.Background(), database.CreateApprovedRequestParams{
		StreamID:   eventID,
		ClientID:   "already-persisted",
		Resource:   resource,
		Cost:       2,
		ApprovedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		Remaining:  8,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func checkStoredOnce(t *testing.T, pool *pgxpool.Pool, eventID string) {
	t.Helper()

	var count int64
	if err := pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM approved_requests WHERE stream_id = $1", eventID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	checkCount(t, "stored rows", 1, count)
}

func checkStreamDrained(t *testing.T, client *redis.Client, stream string) {
	t.Helper()

	length, err := client.XLen(context.Background(), stream).Result()
	if err != nil {
		t.Fatal(err)
	}
	checkCount(t, "remaining Redis events", 0, length)

	pending, err := client.XPending(context.Background(), stream, "approval-history-test").Result()
	if err != nil {
		t.Fatal(err)
	}
	checkCount(t, "pending Redis events", 0, pending.Count)
}

func checkCount(t *testing.T, name string, expected, actual int64) {
	t.Helper()
	if actual != expected {
		t.Errorf("expected %s=%d; got %d", name, expected, actual)
	}
}
