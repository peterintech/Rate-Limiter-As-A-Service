package main

import (
	"net/http"
	"time"

	"github.com/peterintech/global-rate-limiter/internal/ratelimiter"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	unknownMetricResource              = "unknown"
	metricOutcomeAllowed               = "allowed"
	metricOutcomeQuotaRejected         = "quota_rejected"
	metricOutcomeInvalidRequest        = "invalid_request"
	metricOutcomeUnknownPolicy         = "unknown_policy"
	metricOutcomeBacklogFull           = "backlog_full"
	metricOutcomeDependencyUnavailable = "dependency_unavailable"
	metricOutcomeInternalError         = "internal_error"
)

var metricOutcomes = []string{
	metricOutcomeAllowed,
	metricOutcomeQuotaRejected,
	metricOutcomeInvalidRequest,
	metricOutcomeUnknownPolicy,
	metricOutcomeBacklogFull,
	metricOutcomeDependencyUnavailable,
	metricOutcomeInternalError,
}

type apiMetrics struct {
	decisions      *prometheus.CounterVec
	duration       *prometheus.HistogramVec
	inFlight       prometheus.Gauge
	knownResources map[string]struct{}
	registry       *prometheus.Registry
}

func newAPIMetrics(policies ratelimiter.TokenBucketPolicies, includeRuntimeMetrics bool) *apiMetrics {
	registry := prometheus.NewRegistry()
	metrics := &apiMetrics{
		decisions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "rate_limiter",
			Name:      "decisions_total",
			Help:      "Completed rate limit decisions by outcome and configured resource.",
		}, []string{"outcome", "resource"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "rate_limiter",
			Name:      "decision_duration_seconds",
			Help:      "Complete HTTP rate limit decision duration in seconds.",
			Buckets:   []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
		}, []string{"outcome", "resource"}),
		inFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "rate_limiter",
			Name:      "in_flight_requests",
			Help:      "Rate limit checks currently being processed.",
		}),
		knownResources: map[string]struct{}{unknownMetricResource: {}},
		registry:       registry,
	}
	for key := range policies {
		metrics.knownResources[key.Resource] = struct{}{}
	}
	for resource := range metrics.knownResources {
		for _, outcome := range metricOutcomes {
			metrics.decisions.WithLabelValues(outcome, resource)
			metrics.duration.WithLabelValues(outcome, resource)
		}
	}
	registry.MustRegister(metrics.decisions, metrics.duration, metrics.inFlight)
	if includeRuntimeMetrics {
		registry.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	}
	return metrics
}

func (m *apiMetrics) handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

func (m *apiMetrics) observe(resource, outcome string, started time.Time) {
	resource = m.resourceLabel(resource)
	m.decisions.WithLabelValues(outcome, resource).Inc()
	m.duration.WithLabelValues(outcome, resource).Observe(time.Since(started).Seconds())
}

func (m *apiMetrics) resourceLabel(resource string) string {
	if _, exists := m.knownResources[resource]; exists {
		return resource
	}
	return unknownMetricResource
}
