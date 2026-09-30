package main

import (
	"bytes"
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
	"github.com/peterintech/global-rate-limiter/internal/ratelimiter"
	"github.com/peterintech/global-rate-limiter/internal/store"
)

func TestApprovalReportingReadsStoredUsage(t *testing.T) {
	pool, clientID := newAnalyticsTestData(t)
	app := newTestApplication(t, analyticsTestConfig(), time.Now)
	app.database = pool

	request := httptest.NewRequest(http.MethodGet, "/v1/analytics/summary?days=10&client_id="+clientID, nil)
	response := executeRequest(request, app.mount())
	checkResponse(t, "analytics summary", http.StatusOK, response.Code)

	var summary approvalSummaryResponse
	if err := json.NewDecoder(response.Body).Decode(&summary); err != nil {
		t.Fatal(err)
	}
	checkResponse(t, "total approvals", 2, int(summary.TotalApprovals))
	checkResponse(t, "total weighted cost", 5, int(summary.TotalCost))
	checkResponse(t, "policy groups", 1, len(summary.Policies))
	if summary.Policies[0].ClientID != clientID || summary.Policies[0].Resource != "openai" {
		t.Errorf("unexpected policy summary: %+v", summary.Policies[0])
	}
	if summary.FirstApprovalAt == nil || summary.LatestApprovalAt == nil {
		t.Error("expected first and latest approval timestamps")
	}

	trendRequest := httptest.NewRequest(http.MethodGet, "/v1/analytics/trends?days=10&client_id="+clientID, nil)
	trendResponse := executeRequest(trendRequest, app.mount())
	checkResponse(t, "analytics trends", http.StatusOK, trendResponse.Code)
	var trends approvalTrendsResponse
	if err := json.NewDecoder(trendResponse.Body).Decode(&trends); err != nil {
		t.Fatal(err)
	}
	checkResponse(t, "daily trend rows", 2, len(trends.Trends))
	var trendApprovals, trendCost int64
	for _, trend := range trends.Trends {
		trendApprovals += trend.TotalApprovals
		trendCost += trend.TotalCost
	}
	checkResponse(t, "trend approvals", 2, int(trendApprovals))
	checkResponse(t, "trend weighted cost", 5, int(trendCost))

	t.Logf("client=%s; approvals=%d; weighted cost=%d; daily rows=%d", clientID, summary.TotalApprovals, summary.TotalCost, len(trends.Trends))
}

func TestAnalyticsRejectsUnsupportedPeriod(t *testing.T) {
	app := newTestApplication(t, analyticsTestConfig(), time.Now)
	request := httptest.NewRequest(http.MethodGet, "/v1/analytics/summary?days=7", nil)
	response := executeRequest(request, app.mount())

	checkResponse(t, "unsupported analytics period", http.StatusBadRequest, response.Code)
}

func TestPostgresFailureDoesNotStopRateLimitChecks(t *testing.T) {
	pool := newAnalyticsTestPool(t)
	app := newTestApplication(t, analyticsTestConfig(), time.Now)
	app.database = pool
	pool.Close()

	analyticsRequest := httptest.NewRequest(http.MethodGet, "/v1/analytics/summary?days=10", nil)
	analyticsResponse := executeRequest(analyticsRequest, app.mount())
	checkResponse(t, "analytics with unavailable PostgreSQL", http.StatusServiceUnavailable, analyticsResponse.Code)

	checkRequest := httptest.NewRequest(http.MethodPost, "/v1/check", bytes.NewBufferString(
		`{"client_id":"client-a","resource":"openai","cost":1}`,
	))
	checkResponseRecorder := executeRequest(checkRequest, app.mount())
	checkResponse(t, "rate-limit check with unavailable PostgreSQL", http.StatusOK, checkResponseRecorder.Code)

	t.Log("PostgreSQL failure stopped analytics while the independent rate-limit decision remained available")
}

func analyticsTestConfig() config {
	return config{
		env:              "test",
		analyticsTimeout: time.Second,
		tokenBucketPolicies: ratelimiter.TokenBucketPolicies{
			{ClientID: "client-a", Resource: "openai"}: {Limit: 10, Window: time.Minute},
		},
	}
}

func newAnalyticsTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("DATABASE_TEST_URL")
	if databaseURL == "" {
		databaseURL = "postgres://rate_limiter:rate_limiter@localhost:5433/rate_limiter?sslmode=disable"
	}
	pool, err := store.NewPostgresPool(context.Background(), databaseURL)
	if err != nil {
		t.Skipf("analytics integration test requires PostgreSQL at DATABASE_TEST_URL: %v", err)
	}
	return pool
}

func newAnalyticsTestData(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	pool := newAnalyticsTestPool(t)
	t.Cleanup(pool.Close)
	clientID := fmt.Sprintf("analytics-test-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		pool.Exec(context.Background(), "DELETE FROM approved_requests WHERE client_id = $1", clientID)
	})

	queries := database.New(pool)
	for index, approval := range []struct {
		cost int32
		age  time.Duration
	}{{cost: 2, age: time.Hour}, {cost: 3, age: 24 * time.Hour}, {cost: 7, age: 31 * 24 * time.Hour}} {
		err := queries.CreateApprovedRequest(context.Background(), database.CreateApprovedRequestParams{
			StreamID: fmt.Sprintf("%s-%d", clientID, index), ClientID: clientID, Resource: "openai",
			Cost: approval.cost, ApprovedAt: pgtype.Timestamptz{Time: time.Now().Add(-approval.age), Valid: true}, Remaining: 10,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return pool, clientID
}
