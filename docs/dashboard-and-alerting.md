# Dashboard and alerting

V16 made the decision path measurable, but an operator still had to know PromQL and inspect PostgreSQL manually. V17 turns those raw signals into one provisioned Grafana dashboard and a small set of tested Prometheus alerts.

## Why Grafana instead of an HTMX dashboard

An HTMX page would give the project complete control over its presentation, but it would also require us to build authentication, charting, time-range controls, datasource clients, refresh behaviour, and alert visualization. None of those are rate-limiter capabilities.

Grafana already provides those operational features and can query both stores without adding another application endpoint:

```text
API instances ──metrics──> Prometheus ──┐
                                       ├──> Grafana :3000
approved requests ───────> PostgreSQL ──┘
```

Prometheus answers short-lived operational questions: Is an instance up? How many decisions are being made? Are latency or dependency failures rising? PostgreSQL answers durable business questions: How many requests were approved, and what weighted cost was consumed? Grafana presents both; it is not the source of truth for either.

The Compose stack pins `grafana/grafana:13.2.3` rather than following a moving `latest` tag. Dashboard and datasource provisioning are stored in `deploy/grafana`, so deleting Grafana's local volume and recreating the container produces the same operational view.

## Access and permissions

Open `http://localhost:3000`. Anonymous users receive the Viewer role for convenient local demonstrations. The local administrator defaults to `admin` / `rate-limiter-admin`; set `GRAFANA_ADMIN_USER` and `GRAFANA_ADMIN_PASSWORD` before using the stack outside a disposable learning environment.

Grafana connects to PostgreSQL as `grafana_reader`, not as the application owner. The setup grants that role `SELECT` on `approved_requests` and no write privileges. The role setup is a deployment concern rather than a Goose schema migration because its password comes from the environment and differs between deployments. Goose remains responsible for the database schema.

Set `GRAFANA_DATABASE_PASSWORD` to replace the local reader password. The one-shot `grafana-database-role` service creates or updates the role after migrations have completed and before Grafana starts.

## What the dashboard shows

The **Global Rate Limiter** dashboard has three layers:

1. **Current service health** shows healthy API instances, decision rate, cluster p95 latency, firing alerts, in-flight checks, and the quota-rejection ratio.
2. **Decision traffic and latency** breaks outcomes, latency percentiles, resources, and operational failures down far enough to diagnose the live service without introducing unbounded labels.
3. **Approved usage history** queries PostgreSQL for approval counts, weighted cost, first/latest approval, daily policy trends, and a policy table. Its 10, 15, and 30-day selector matches the reporting periods established in V15.

The `resource`, `outcome`, and `instance` dashboard variables use finite values. There is deliberately no `client_id` Prometheus variable because V16 excluded client identifiers to prevent unbounded time-series growth. Per-client approved history remains available from PostgreSQL-backed reporting.

## Alert policy

An alert is a request for human attention, not another graph. The initial rules therefore cover loss of service, correctness dependencies, irreversible admission pressure, and sustained performance symptoms.

| Alert | Threshold and duration | Why this threshold exists |
|---|---|---|
| `RateLimiterInstanceDown` | One API target down for 1 minute | Avoids paging on one missed five-second scrape while still identifying lost redundancy. |
| `RateLimiterDeploymentDown` | No API target available for 30 seconds | Complete loss is urgent and needs a shorter confirmation window. |
| `RateLimiterDependencyUnavailable` | Dependency-unavailable decisions continue for 1 minute | One transient Redis failover can recover; continuing failures mean authoritative decisions are unavailable. |
| `RateLimiterApprovalBacklogFull` | At least one backlog refusal in 5 minutes | V14 established that this outcome refuses an otherwise valid approval, so one occurrence is actionable. |
| `RateLimiterHighDecisionLatency` | Cluster p95 above 10 ms for 5 minutes | V8 used p99 below 10 ms as the local acceptance target. A sustained p95 breach means the slow tail has become normal behaviour rather than a short spike. |
| `RateLimiterHighQuotaRejectionRatio` | More than 25% rejected for 5 minutes while traffic exceeds 1 request/second | The traffic floor avoids noisy ratios at idle; duration distinguishes a real capacity or client-behaviour change from a brief burst. |

These are starting policies, not universal production constants. A real deployment should revise them from its service-level objectives, traffic shape, and incident history. V17 intentionally does not add Alertmanager, email, chat, or pager delivery. The rules evaluate and appear in Prometheus/Grafana, but external routing needs deployment-specific ownership and credentials.

## How the rules are tested

Prometheus rule tests use synthetic time series, so alert semantics do not depend on waiting for a real incident:

```text
docker run --rm --entrypoint=/bin/promtool -v "$PWD/deploy/prometheus:/etc/prometheus:ro" -w /etc/prometheus prom/prometheus:v3.5.5 test rules alerts_test.yml
```

The suite proves that a healthy deployment stays quiet, one lost instance warns without declaring a total outage, a total outage becomes critical, dependency failures must persist, one backlog refusal fires immediately, and high rejection/latency signals must remain above threshold for their complete duration.

The five-minute rate alerts need more than five minutes of synthetic input: Prometheus first establishes the five-minute rate window and then observes the configured five-minute `for` duration. Encoding both periods prevents a deceptively short test.

## Runtime evidence

The Compose verification produced these results:

- Grafana reported version 13.2.3 and provisioned the `Global Rate Limiter` dashboard.
- Both Prometheus and PostgreSQL datasource health checks returned `OK`.
- PostgreSQL showed `grafana_reader=r` on `approved_requests`; the reader had no insert, update, or delete grant.
- Stopping `api-2` for 75 seconds fired `RateLimiterInstanceDown` for `api-2:8080`, while the distributed gateway continued returning HTTP 200 through `api-1`.
- Stopping Grafana left both the API and Prometheus healthy with HTTP 200.
- Stopping Prometheus left the API healthy and PostgreSQL queries available.
- Stopping PostgreSQL left the Redis-backed rate-limit decision and Prometheus healthy with HTTP 200. As established in V14, a prolonged database outage can eventually fill the bounded event backlog and then deliberately stop new approvals.

This proves failure isolation, not monitoring high availability. Grafana, Prometheus, PostgreSQL, both API instances, and both Redis nodes still share one computer. Losing that host loses the dashboard and local Prometheus history together with the application. Production deployment needs independent hosts or availability zones, protected credentials, backups, replicated monitoring storage where required, and external alert delivery.

## Useful checks

```text
docker compose up -d --build
curl http://localhost:3000/api/health
curl http://localhost:9090/api/v1/rules
curl http://localhost:9090/api/v1/alerts
```

The dashboard is available at `http://localhost:3000/d/global-rate-limiter/global-rate-limiter`. Prometheus remains available at `http://localhost:9090` for inspecting the underlying expressions.
