package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/peterintech/global-rate-limiter/internal/database"
)

var errInvalidAnalyticsPeriod = errors.New("days must be one of 10, 15, or 30")

type analyticsFilter struct {
	Days     int32
	ClientID string
	Resource string
}

type approvalSummaryResponse struct {
	PeriodDays       int32                   `json:"period_days"`
	ClientID         string                  `json:"client_id,omitempty"`
	Resource         string                  `json:"resource,omitempty"`
	TotalApprovals   int64                   `json:"total_approvals"`
	TotalCost        int64                   `json:"total_cost"`
	FirstApprovalAt  *time.Time              `json:"first_approval_at"`
	LatestApprovalAt *time.Time              `json:"latest_approval_at"`
	Policies         []approvalPolicySummary `json:"policies"`
}

type approvalPolicySummary struct {
	ClientID       string `json:"client_id"`
	Resource       string `json:"resource"`
	TotalApprovals int64  `json:"total_approvals"`
	TotalCost      int64  `json:"total_cost"`
}

type approvalTrendsResponse struct {
	PeriodDays int32                `json:"period_days"`
	ClientID   string               `json:"client_id,omitempty"`
	Resource   string               `json:"resource,omitempty"`
	Trends     []dailyApprovalTrend `json:"trends"`
}

type dailyApprovalTrend struct {
	Day            string `json:"day"`
	ClientID       string `json:"client_id"`
	Resource       string `json:"resource"`
	TotalApprovals int64  `json:"total_approvals"`
	TotalCost      int64  `json:"total_cost"`
}

func (app *application) approvalSummaryHandler(w http.ResponseWriter, r *http.Request) {
	filter, err := readAnalyticsFilter(r)
	if err != nil {
		app.badRequestError(w, r, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), app.config.analyticsTimeout)
	defer cancel()
	queries := database.New(app.database)

	summary, err := queries.GetApprovalSummary(ctx, database.GetApprovalSummaryParams{
		Days: filter.Days, ClientID: filter.ClientID, Resource: filter.Resource,
	})
	if err != nil {
		app.analyticsUnavailableError(w, r, err)
		return
	}
	policies, err := queries.ListApprovalSummaryByPolicy(ctx, database.ListApprovalSummaryByPolicyParams{
		Days: filter.Days, ClientID: filter.ClientID, Resource: filter.Resource,
	})
	if err != nil {
		app.analyticsUnavailableError(w, r, err)
		return
	}

	response := approvalSummaryResponse{
		PeriodDays:       filter.Days,
		ClientID:         filter.ClientID,
		Resource:         filter.Resource,
		TotalApprovals:   summary.TotalApprovals,
		TotalCost:        summary.TotalCost,
		FirstApprovalAt:  timeFromMilliseconds(summary.FirstApprovalAtMs),
		LatestApprovalAt: timeFromMilliseconds(summary.LatestApprovalAtMs),
		Policies:         make([]approvalPolicySummary, 0, len(policies)),
	}
	for _, policy := range policies {
		response.Policies = append(response.Policies, approvalPolicySummary{
			ClientID: policy.ClientID, Resource: policy.Resource,
			TotalApprovals: policy.TotalApprovals, TotalCost: policy.TotalCost,
		})
	}

	if err := writeJSON(w, http.StatusOK, response); err != nil {
		app.logger.Errorw("write analytics summary", "error", err)
	}
}

func (app *application) approvalTrendsHandler(w http.ResponseWriter, r *http.Request) {
	filter, err := readAnalyticsFilter(r)
	if err != nil {
		app.badRequestError(w, r, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), app.config.analyticsTimeout)
	defer cancel()
	rows, err := database.New(app.database).ListDailyApprovalTrends(ctx, database.ListDailyApprovalTrendsParams{
		Days: filter.Days, ClientID: filter.ClientID, Resource: filter.Resource,
	})
	if err != nil {
		app.analyticsUnavailableError(w, r, err)
		return
	}

	response := approvalTrendsResponse{
		PeriodDays: filter.Days,
		ClientID:   filter.ClientID,
		Resource:   filter.Resource,
		Trends:     make([]dailyApprovalTrend, 0, len(rows)),
	}
	for _, row := range rows {
		response.Trends = append(response.Trends, dailyApprovalTrend{
			Day: row.Day.Time.Format(time.DateOnly), ClientID: row.ClientID, Resource: row.Resource,
			TotalApprovals: row.TotalApprovals, TotalCost: row.TotalCost,
		})
	}

	if err := writeJSON(w, http.StatusOK, response); err != nil {
		app.logger.Errorw("write analytics trends", "error", err)
	}
}

func readAnalyticsFilter(r *http.Request) (analyticsFilter, error) {
	days, err := strconv.Atoi(r.URL.Query().Get("days"))
	if err != nil || (days != 10 && days != 15 && days != 30) {
		return analyticsFilter{}, errInvalidAnalyticsPeriod
	}
	return analyticsFilter{
		Days: int32(days), ClientID: strings.TrimSpace(r.URL.Query().Get("client_id")),
		Resource: strings.TrimSpace(r.URL.Query().Get("resource")),
	}, nil
}

func timeFromMilliseconds(milliseconds int64) *time.Time {
	if milliseconds == 0 {
		return nil
	}
	value := time.UnixMilli(milliseconds).UTC()
	return &value
}
