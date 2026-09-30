# Global Rate Limiter

An intentionally evolutionary Go implementation of the qualification brief. The final destination is a highly available, cluster-accurate global rate limiter. Each new component is justified by a demonstrated failure in the preceding version.

## How to read this project

Read [`docs/design-journey.md`](docs/design-journey.md) before treating the current branch as the complete story. It explains the measured failure in every phase, the alternatives considered, why one option was selected, and the tradeoff deliberately carried into the next phase. The phase branches are implementation checkpoints; the design journey is the guide connecting them.

## Current scope: V17

- Contract and explicit assumptions in [`docs/requirements.md`](docs/requirements.md)
- Phase-by-phase decisions and tradeoffs in [`docs/design-journey.md`](docs/design-journey.md)
- Final qualification target in [`docs/goal.md`](docs/goal.md)
- HTTP contract in [`docs/api.md`](docs/api.md)
- Static per-client/resource policies
- In-memory token buckets with continuous refill
- An HTTP test that exercises the configured limiter through quota exhaustion
- Atomic rate-limit decisions within one process
- A concurrent HTTP test that verifies the configured limit is not exceeded
- A deterministic boundary test that demonstrates fixed-window bursting
- A sliding-log implementation that prevents the measured boundary burst
- A benchmark that demonstrates sliding-log state growth under load
- A token-bucket comparison that demonstrates bounded state
- A staged Vegeta test that measures one instance's HTTP traffic capacity
- A multi-instance experiment that demonstrates quota multiplication with isolated in-memory state
- A Redis-backed token bucket shared by every API instance
- One atomic Lua decision using Redis server time
- Expiring client/resource bucket keys with bounded state
- Comparable one-instance and two-instance Nginx routes for traffic testing
- A runtime Redis-outage experiment that records decision failure and automatic reconnection
- Redis primary/replica replication with three-Sentinel automatic failover
- AOF-backed quota recovery after every Redis process is replaced
- Bounded Redis operation and complete-decision timeouts
- Per-instance circuit breakers that fail closed without local quota
- HTTP 503 dependency responses with a retry hint
- Separate liveness and dependency-aware readiness endpoints
- One Redis Stream event for every approved request
- Approval events appended inside the token-bucket Lua execution
- Shared event history across both API instances and Redis failover
- AOF recovery of unconsumed approval events
- A separate approval-event worker using a Redis consumer group
- PostgreSQL-backed permanent approval history
- Goose-managed schema migrations and sqlc-generated query code
- At-least-once delivery with idempotent `stream_id` inserts
- Recovery of abandoned pending entries with `XAUTOCLAIM`
- Exact Redis cleanup only after a successful PostgreSQL commit
- A shared admission limit that stops new approvals before the event backlog can grow without bound
- Redis memory configured to reject writes instead of evicting authoritative quota or billing state
- Worker liveness, dependency readiness, and backlog status endpoints
- Backlog warning, full, recovery, and fully-drained transition logs
- PostgreSQL-backed approved-usage summaries for trailing 10, 15, and 30-day periods
- Daily approval and weighted-cost trends with optional client/resource filters
- A reconnecting analytics dependency that can fail without stopping rate-limit decisions
- Per-instance Prometheus counters for every decision outcome
- Aggregatable decision-duration histograms and an in-flight request gauge
- Bounded `outcome` and `resource` labels without client identifiers
- Private scraping of both API instances with 30-day local retention
- Monitoring failure isolated from rate-limit decisions
- A provisioned Grafana dashboard combining live Prometheus signals with durable PostgreSQL usage
- A dedicated read-only PostgreSQL role for dashboard queries
- Tested Prometheus alerts for availability, dependency failure, backlog refusal, latency, and quota rejection
- Runtime proof that losing one API instance fires an alert while the distributed gateway remains available
- Application wiring with Chi, injected interfaces, Zap logging, environment configuration, and graceful shutdown

Not included: multi-host deployment, replicated monitoring storage, external alert delivery, or production credential management.

## Run

```text
docker compose up -d --build
```

The Compose stack runs two API instances against a Sentinel-managed Redis primary and replica. A separate worker moves approval events into PostgreSQL after Goose applies the schema. PostgreSQL is exposed at `localhost:5433`. The comparison gateways expose one API instance at `localhost:8083` and both instances at `localhost:8084`. Running the API directly requires Go 1.26 or newer; use `go run ./cmd/api` and configure a standalone Redis address through `.env` or the process environment.

Redis Commander is available at `http://localhost:8082` and lists both Redis data nodes. Their names describe their startup roles; Sentinel may reverse those roles after failover.
The worker exposes liveness, readiness, and backlog status at `http://localhost:8085/v1/health`, `/v1/readiness`, and `/v1/status`.
Prometheus is available at `http://localhost:9090` and scrapes both APIs inside the Compose network. The public gateways deliberately return 404 for `/metrics`.
Grafana is available at `http://localhost:3000` with a provisioned `Global Rate Limiter` dashboard. Anonymous access is read-only; local administrator and PostgreSQL reader credentials are configured through `.env`.

```text
curl -i -X POST http://localhost:8083/v1/check -H "Content-Type: application/json" -d '{"client_id":"client-a","resource":"openai","cost":1}'
```

The demo policies are `client-a/openai` at 100 requests/minute and `client-b/stripe` at 5000 requests/minute.

## Test

```text
go test ./...
go test -race ./...
go test -run '^$' -bench=BenchmarkLimiterState -benchmem -benchtime=20x ./benchmarks
```

The race test requires a race-enabled Go toolchain with a C compiler installed.
The shared-quota integration test runs when Redis is available at `REDIS_TEST_ADDR` or `localhost:6379`; otherwise it is skipped.
See [`docs/benchmarks.md`](docs/benchmarks.md) for the state-growth experiment and representative results.
See [`docs/load-testing.md`](docs/load-testing.md) for the single-instance capacity experiment and the replicated-topology comparison.
See [`docs/running-load-tests.md`](docs/running-load-tests.md) for step-by-step commands to run all three routes and save Vegeta reports. Each containerized API has a four-CPU limit.
See [`docs/redis.md`](docs/redis.md) for the shared token-bucket design and atomic decision flow.
See [`docs/redis-outage.md`](docs/redis-outage.md) for the measured runtime outage and recovery behavior.
See [`docs/redis-high-availability.md`](docs/redis-high-availability.md) for the Sentinel failover and AOF recovery experiments.
See [`docs/redis-resilience.md`](docs/redis-resilience.md) for bounded failure, circuit breaking, readiness, and recovery evidence.
See [`docs/approval-events.md`](docs/approval-events.md) for the approval-event alternatives, guarantees, limitations, failover proof, and latency comparison.
See [`docs/durable-approval-history.md`](docs/durable-approval-history.md) for PostgreSQL persistence, Goose/sqlc organization, at-least-once delivery, safe cleanup, and outage recovery.
See [`docs/backlog-protection.md`](docs/backlog-protection.md) for the V14 failure policy, atomic admission rule, worker signals, and recovery evidence.
See [`docs/historical-usage-reporting.md`](docs/historical-usage-reporting.md) for V15's reporting contract, query-plan evidence, outage isolation, and explicit data limitations.
See [`docs/operational-metrics.md`](docs/operational-metrics.md) for V16's metrics contract, cardinality choices, PromQL examples, and outage-isolation evidence.
See [`docs/dashboard-and-alerting.md`](docs/dashboard-and-alerting.md) for V17's dashboard, alert rationale, rule tests, permissions, and runtime failure-isolation evidence.

## Repository layout

```text
cmd/api/                  composition, config, routing, handlers, HTTP helpers, lifecycle
cmd/worker/               approval consumer composition, configuration, and lifecycle
benchmarks/               algorithm experiments and HTTP load-test inputs
deploy/nginx/             comparable single-instance and distributed gateways
deploy/redis/             Sentinel configuration template
deploy/prometheus/        Prometheus scrape configuration
deploy/grafana/           provisioned datasources and operational dashboard
deploy/postgres/          deployment-specific read-only dashboard role
internal/env/             environment lookup helper
internal/events/          Redis consumption, PostgreSQL transaction, and cleanup workflow
internal/database/        sqlc-generated PostgreSQL models and queries
internal/ratelimiter/     limiter contract and isolated algorithm implementations
internal/store/           external storage client construction
sql/schema/               Goose database migrations
sql/queries/              SQL source used by sqlc
docs/                     requirements and API contract
Dockerfile                production-style API image
docker-compose.yaml       Redis HA and replicated API topology
```

The HTTP layer owns request parsing and orchestration. Rate limiting sits behind a narrow interface and is injected into the application during startup. As in Psocial, no pass-through service layer is added before workflow complexity justifies one.

## Next milestone

The core build is complete: enforcement, distribution, resilience, durable history, reporting, metrics, dashboarding, and alert evaluation are all represented with evidence. The remaining work is a release pass—run the complete end-to-end verification, review the teaching narrative, produce the final architecture image and archive, then integrate the approved phase branches into the central branch. External alert delivery and true multi-host deployment remain production extensions rather than another core phase.
