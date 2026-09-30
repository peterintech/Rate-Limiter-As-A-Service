# Operational decision metrics

V15 made approved usage queryable from PostgreSQL, but that history cannot answer operational questions such as:

- How many decisions are allowed, quota-rejected, or failing right now?
- How long does a complete rate-limit check take?
- Is one API instance behaving differently from another?
- How many checks are currently in progress?

This phase adds Prometheus metrics for those questions. It does not turn billing history into monitoring data or monitoring data into billing history.

## Why Prometheus

Three approaches were considered:

- Query PostgreSQL. This is correct for durable approved usage, but rejected and failed decisions are deliberately absent from that database. Adding them would also make an analytics schema carry an operational monitoring workload.
- Derive metrics from logs. Logs are valuable for individual failures, but counting and calculating percentiles from them requires another ingestion and query system. The current logs also do not form a stable metrics contract.
- Expose Prometheus metrics from each API process. Counters, histograms, and gauges match the required signals, and Prometheus can aggregate the same metric across replicated instances.

The project uses `client_golang` v1.24.1 and the Prometheus 3.5 long-term-support series, pinned to `prom/prometheus:v3.5.5`. Choosing the LTS series instead of the newest feature release favors a longer maintenance window for an operational dependency. The server stores samples in a named local volume for 30 days.

That local storage is suitable for this single-host demonstration, not a production durability claim. Prometheus local storage is not replicated; losing the Docker host or its volume loses the monitoring history. Production can add remote storage or a separately operated Prometheus deployment when that requirement is demonstrated.

## Metrics contract

| Metric | Type | Meaning |
| --- | --- | --- |
| `rate_limiter_decisions_total` | Counter | Completed checks partitioned by outcome and configured resource |
| `rate_limiter_decision_duration_seconds` | Histogram | Complete HTTP decision time, including parsing, validation, limiter work, and response encoding |
| `rate_limiter_in_flight_requests` | Gauge | Checks currently being processed by one API instance |

Decision outcomes are deliberately finite:

- `allowed`
- `quota_rejected`
- `invalid_request`
- `unknown_policy`
- `backlog_full`
- `dependency_unavailable`
- `internal_error`

`resource` is included because operators need to distinguish provider workloads such as `openai` and `stripe`. Only configured resource names are exported. An arbitrary or unknown resource becomes `resource="unknown"`.

`client_id` is intentionally excluded. A client label would create a new time series for every customer and every outcome. That unbounded cardinality could make the monitoring system itself expensive or unstable. Per-client approved usage remains available through the PostgreSQL analytics API.

## Scraping and exposure

Prometheus scrapes both API containers every five seconds over the private Compose network:

```text
Prometheus
  ├── api-1:8080/metrics
  └── api-2:8080/metrics
```

The endpoint is not part of the public API. Both Nginx gateways return `404` for `/metrics`; only direct service-network traffic can scrape it. The Prometheus query UI is available locally at `http://localhost:9090` for this learning environment.

Useful PromQL queries:

```promql
# Decision rate across all API instances.
sum by (outcome, resource) (rate(rate_limiter_decisions_total[5m]))

# Overall p95 decision latency across all API instances.
histogram_quantile(
  0.95,
  sum by (le) (rate(rate_limiter_decision_duration_seconds_bucket[5m]))
)

# Average decision latency over the selected window.
sum(rate(rate_limiter_decision_duration_seconds_sum[5m]))
/
sum(rate(rate_limiter_decision_duration_seconds_count[5m]))

# Work currently executing across the API deployment.
sum(rate_limiter_in_flight_requests)

# Scrape health for each API instance.
up{job="rate-limiter-api"}
```

Histograms were selected instead of summaries because bucket counts from multiple API instances can be aggregated before calculating a percentile. Averaging precomputed instance percentiles would not produce a valid cluster percentile.

Counters reset when an API process restarts. Dashboards and alerts should use `rate()` or `increase()` over a time range rather than treating a raw counter as permanent history. Prometheus retains the earlier samples, and these functions account for a reset observed between scrapes.

## Evidence

The application-level test produced one allowed decision, one quota rejection, one invalid request, and one unknown policy. It verified a duration observation for the decision, a final in-flight value of zero, and the absence of client identifiers and arbitrary resource values from the exposition.

The Compose experiment then proved the deployed boundaries:

- Prometheus reported two healthy scrape targets: `api-1:8080` and `api-2:8080`.
- Requests sent through the distributed gateway produced metrics from both API instances.
- Spending a complete 100-token bucket returned HTTP 200; the next request returned HTTP 429 and incremented `quota_rejected`.
- Invalid cost returned HTTP 400 and incremented `invalid_request`.
- An unconfigured resource returned HTTP 404 and appeared only as `resource="unknown"`.
- The distributed gateway returned HTTP 404 for `/metrics`.
- After Prometheus was stopped, a rate-limit decision and `/v1/health` both still returned HTTP 200.
- Prometheus restarted and resumed scraping both API instances without an API restart.

The outage result is intentional: metrics are observed in process and scraped by Prometheus, so Prometheus is not on the decision path. While Prometheus is down, each running API still holds its counters in memory. Samples are unavailable until scraping resumes, and observations can be lost if an API also restarts before they are scraped. Metrics are operational evidence, not a billing ledger.

## How to inspect it

Start the stack:

```text
docker compose up -d --build
```

Open `http://localhost:9090` and run one of the PromQL queries above. To confirm the public boundary:

```text
curl -i http://localhost:8084/metrics
```

The expected response is HTTP 404. Inside the Compose network, Prometheus uses the private service names instead.

## Tradeoff carried forward

V16 provides queryable operational signals, but a query console is not an operator workflow. There are no curated panels, thresholds, or alert rules yet. V17 can now build a Grafana dashboard from measured, bounded metrics rather than inventing a dashboard before the signals exist.

## References

- [Prometheus instrumentation guidance](https://prometheus.io/docs/practices/instrumentation/)
- [Prometheus histogram and aggregation guidance](https://prometheus.io/docs/practices/histograms/)
- [Prometheus local-storage guarantees and retention](https://prometheus.io/docs/prometheus/latest/storage/)
- [Prometheus 3.5 LTS release schedule](https://github.com/prometheus/prometheus/blob/main/RELEASE.md)
- [Prometheus Go client v1.24.1](https://github.com/prometheus/client_golang/releases/tag/v1.24.1)
