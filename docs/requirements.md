# V0/V1 Requirements

The qualification brief is the destination: a highly available global rate-limiter service with cluster-wide accuracy, millisecond decisions, degraded-mode behavior, durable approved-request logging, analytics, dashboarding, tests, and one-command deployment.

This repository deliberately starts before that destination. New components are added only after a test or experiment demonstrates the failure that requires them.

[`design-journey.md`](design-journey.md) records the alternatives, decisions, evidence, and accepted tradeoffs that connect these requirements from one phase to the next.

## V0: contract

- A caller asks whether a positive request cost may be spent for a `client_id` and `resource`.
- A configured client/resource pair has a positive limit and fixed duration.
- An allowed decision consumes the requested cost and reports the remaining capacity.
- A denied decision does not consume capacity and reports when another attempt may succeed.
- Unknown policies and malformed requests are explicit client errors.
- Time is UTC in the HTTP contract.

## V1: accepted scope

- One Go process.
- Static, in-process policies so different clients can have different limits.
- One in-memory fixed-window counter per client/resource pair.
- A small HTTP API and a focused test of its rate-limit behavior.
- No external runtime dependencies.

## Application boundaries

The project follows the same wiring pattern as Psocial:

- `cmd/api` owns startup, configuration, dependency construction, routing, HTTP handlers, JSON/error helpers, and graceful shutdown.
- `internal/ratelimiter` owns the limiter contract and keeps each algorithm implementation in its own file.
- `internal/store` owns external storage client construction, following the same dependency-wiring boundary used by Psocial.
- Handlers depend on the narrow `ratelimiter.Limiter` interface through an `application` struct rather than constructing implementations themselves.
- There is no service layer yet. The two handlers are small enough to orchestrate their use cases directly.

## V2: concurrent safety

A concurrent HTTP workload demonstrated that the unsynchronized limiter could crash with `fatal error: concurrent map writes`. The fixed-window implementation now keeps each read, capacity check, and spend in one critical section.

- Concurrent requests cannot corrupt the in-memory window map.
- A policy cannot allow more than its configured capacity within a window.
- Synchronization remains an implementation detail of the fixed-window algorithm.
- The public limiter contract and HTTP API are unchanged.

## V3: fixed-window boundary burst

A deterministic HTTP test places three requests immediately before an aligned one-minute boundary and three immediately after it. The fixed-window limiter approves all six requests within 200 milliseconds even though the configured limit is three requests per minute.

This phase records the behavior without changing the algorithm. The measured burst will justify selecting and comparing a smoother algorithm in the next phase.

## V4: sliding-log enforcement

The application now uses an in-memory sliding log. Each client/resource policy keeps the timestamp and weighted cost of approved requests that remain inside the rolling interval.

- Requests do not receive fresh capacity merely because an aligned boundary was crossed.
- Expired request entries are removed before each decision.
- The capacity check and request recording remain one atomic operation.
- Fixed-window and sliding-log types remain isolated in their own implementation files.
- The public limiter contract and HTTP API are unchanged.

## V5: sliding-log state growth

A bounded benchmark compares fixed-window and sliding-log enforcement with 100, 1,000, and 10,000 approved requests at a fixed time. Keeping time fixed ensures every sliding-log entry remains active for the complete batch.

- Fixed-window allocation remains constant as request count increases.
- Sliding-log allocation grows with the number of approved requests retained in the rolling interval.
- The experiment changes no production behavior.
- Token bucket remains deferred until this state-growth limitation has been demonstrated.

## V6: in-memory token bucket

The application now uses an in-memory token bucket. A policy's limit is both its maximum token capacity and the number of tokens replenished continuously over its configured window.

- Each client/resource retains only its token balance and last-refill timestamp.
- Weighted costs consume the corresponding number of tokens.
- Refill, capacity checking, and spending remain one atomic operation.
- Crossing an aligned time boundary does not restore the full quota.
- A cost larger than the bucket capacity returns a client error because it can never succeed.
- The public response reports whole tokens that can be spent immediately and when full capacity is expected to return.

## V7: horizontal scaling limits

An end-to-end Vegeta test establishes a verified local capacity floor of 2,000 HTTP requests per second for one API instance under the selected latency and response criteria. Fixed-rate stages measure how many requests the server receives and answers while the real token bucket remains active. Higher rates are not repeatable on the same-machine test environment, so the result is documented as a floor rather than an exact maximum.

Two independently constructed application instances then receive the same policy and fixed clock. Each instance approves the policy's full capacity because each owns a separate in-memory token bucket.

- Request rate is measured separately from users and connections and is not presented as a universal production limit.
- Every HTTP response counts as handled traffic regardless of status; missing responses and transport errors are recorded separately.
- A policy intended to allow three requests across the cluster permits three requests on each instance.
- Two instances therefore approve six requests before both reject further traffic.
- The experiment changes no production behavior or public contract.
- Cluster-wide enforcement requires shared state with one atomic decision across all instances.
- Redis remains deferred until the next phase; this phase establishes the failure that justifies it.

## V8: shared atomic token bucket

The application now stores token balances and refill timestamps in Redis. Every API instance uses the same client/resource key, so adding instances no longer creates additional quota.

- Each bucket stores only its token balance and last-refill timestamp.
- A Lua script performs refill, capacity checking, spending, persistence, and expiry as one atomic Redis operation.
- Redis server time provides one refill clock for every application instance.
- Rejected requests update elapsed-time refill state but do not spend tokens.
- A bucket key expires after twice its refill window. Once a full refill window has elapsed, recreating an expired bucket at full capacity is equivalent to retaining its fully refilled state.
- Client and resource values are encoded before they become Redis key segments.
- Two applications with separate Redis clients collectively approve no more than one configured quota.
- Traffic experiments compare an unrestricted direct API, one four-CPU API through Nginx, and two four-CPU APIs through Nginx. The measured results and limitations are recorded in `docs/load-testing.md`.
- Redis connectivity is required at startup. Outage behavior after startup is intentionally deferred to the next experiment.
- The HTTP request and response contract is unchanged.

## V9: Redis outage failure

A deterministic application-level integration test and a real Compose outage experiment demonstrate what happens when Redis becomes unavailable after startup.

- A normal distributed rate-limit decision returns HTTP 200 before the outage.
- Distributed rate-limit checks return HTTP 500 while the shared Redis dependency is unavailable.
- Failed decisions took between 2.360 and 4.435 seconds because the Redis client attempted to reconnect.
- The bounded HTTP response does not expose internal Redis errors.
- Both API processes remain alive and `/v1/health` continues returning HTTP 200.
- Existing Redis clients reconnect after Redis becomes healthy; the API instances do not need to restart.
- The experiment adds no fallback, circuit breaker, local emergency bucket, or readiness endpoint.
- Failing open, failing closed, and independent full-capacity fallbacks remain explicit design choices for the next phase.
- The public request and successful-decision contract is unchanged.

## V10: Redis high availability and state durability

The single Redis process is replaced by one primary, one replica, and three Sentinels. Both APIs discover the writable node through the same Sentinel master name. Both data nodes persist state through AOF using `appendfsync everysec` and separate named volumes.

- Three Sentinels use quorum two to agree that the primary is unavailable.
- Stopping the primary automatically promotes the replica without restarting either API.
- The measured promotion completed approximately 6.3 seconds after primary loss; the first successful HTTP decision arrived after 7.966 seconds.
- The former primary rejoins as an online replica of the promoted node.
- Removing and recreating every Redis and Sentinel container while retaining the named volumes preserves the bucket's token balance, last-refill timestamp, and positive TTL.
- Replication is asynchronous, so failover is not claimed to be lossless.
- AOF `everysec` can lose approximately the most recent second of writes during a severe crash.
- Every component still runs on one computer; this demonstrates failover mechanics, not host or availability-zone resilience.
- Dependency failures still use the existing HTTP 500 behavior. Timeouts, circuit breaking, 503 mapping, and readiness remain deferred.
- The public request and successful-decision contract is unchanged.

## V11: bounded Redis failure and readiness

V9 showed that a complete Redis outage held decisions for several seconds. V10 reduced the chance of that outage through replication and automatic failover, but the application still needed protection when the entire Redis deployment was unavailable. V11 bounds that failure inside each API instance.

- Every Redis connection, read, and write has an explicit timeout, and automatic command retries are disabled so they cannot extend the decision unpredictably.
- The complete rate-limit decision has a 300 millisecond deadline.
- Three consecutive dependency failures open a circuit breaker in each API process for five seconds.
- An open circuit does not call Redis and does not create local quota. It returns HTTP 503 immediately.
- HTTP 429 is a valid quota decision and does not count as a dependency failure.
- After the open interval, one request probes the dependency. Success closes the circuit; failure opens it again.
- `/v1/health` remains a process-liveness endpoint and stays healthy during a Redis outage.
- `/v1/readiness` checks whether the API can currently reach Redis and attempt an authoritative decision.
- A complete local Redis outage produced six approximately 304 millisecond failures across two API instances before their independent breakers opened. Later responses completed in approximately 3 milliseconds.
- During the outage, liveness returned HTTP 200, readiness returned HTTP 503, and decisions returned a bounded public HTTP 503 response.
- After Redis restarted, readiness and normal HTTP 200 decisions recovered without restarting either API.

## V12: atomic approved-request event stream

The token bucket previously retained only its current balance and refill time. Individual approvals were overwritten by later aggregate state and disappeared completely when the bucket expired. V12 adds one Redis Stream event for every approval without adding a second network request or a durable database write to the decision path.

- The existing Lua script validates the bucket and stream key types before mutation.
- An allowed decision stores the bucket and appends one event before returning HTTP 200.
- A weighted request creates one event containing its complete cost.
- HTTP 429 decisions append no event.
- Both API instances write to one configurable stream using Redis server time.
- Events contain client, resource, cost, approval time, and remaining capacity.
- The stream is intentionally untrimmed because no durable consumer exists yet.
- A failover experiment preserved three earlier events and appended a fourth after Sentinel promoted the replica.
- Replacing every Redis and Sentinel container while retaining the AOF volumes restored all four observed events.
- A controlled 100 RPS comparison found no measurable low-load regression; higher-rate local tail latency was too variable to isolate the `XADD` cost.
- Redis Lua isolation prevents command interleaving but does not provide rollback after every possible runtime failure.
- Stream durability still inherits asynchronous replication and AOF `everysec` loss windows.

## V13: durable approval-event consumer

The Redis Stream is now a temporary delivery buffer instead of the permanent history store.

- A separate worker reads approvals with a Redis consumer group.
- PostgreSQL schema changes are managed by Goose under `sql/schema`.
- Handwritten queries live under `sql/queries` and sqlc generates their Go implementation in `internal/database`.
- A PostgreSQL transaction commits a complete batch before Redis cleanup begins.
- The Redis stream ID is the database primary key, making repeated delivery idempotent.
- Cleanup acknowledges and deletes only the exact committed stream IDs; length- and age-based trimming are not used.
- A replacement worker uses `XAUTOCLAIM` to recover sufficiently idle pending entries.
- Stopping the worker allows Redis to buffer approvals without changing API responses; restarting it drains the backlog.
- Stopping PostgreSQL also leaves rate-limit decisions available while events remain in Redis.
- The system provides at-least-once delivery with idempotent storage, not an exactly-once claim.
- PostgreSQL runs on host port `5433` in Compose so it can coexist with another local database on the conventional `5432` port.

## V14: bounded approval-event backlog

The Redis Stream is now a finite delivery buffer with an explicit fail-closed admission rule.

- `RATE_LIMIT_EVENT_MAX_BACKLOG` sets one shared maximum stream length and defaults to 100000.
- The Lua script checks stream capacity only for requests that otherwise have enough tokens.
- The capacity check, token spend, and event append remain one atomic Redis operation across every API instance.
- A full backlog returns HTTP 503 without spending tokens or creating an event.
- An exhausted quota still returns HTTP 429 because a rejected decision does not add to the backlog.
- Backlog refusal is an application-capacity result, not a Redis dependency failure, and does not open the circuit breaker.
- Redis data nodes use a 256 MiB limit with `noeviction`; authoritative buckets and unpersisted history are never silently evicted.
- The worker exposes process health, Redis/PostgreSQL readiness, and backlog status on host port `8085`.
- Status includes stream length, pending count, oldest-event age, configured limit, and capacity percentage.
- Transition logs identify warning, full, resumed, below-warning, and fully-drained states without logging the same state every polling interval.
- A Compose outage experiment filled a three-entry stream, observed HTTP 503 with an unchanged token value, then drained it and restored HTTP 200 without restarting the API.
- Entry count bounds queue cardinality but does not replace byte-level Redis memory monitoring or production capacity sizing.

## V15: historical approved-usage reporting

Durable approval history is now available through read-only HTTP endpoints.

- Summary and daily-trend routes accept only 10, 15, or 30-day periods.
- Optional client and resource parameters apply exact filters.
- Summary reports approval count, weighted cost, first/latest approval, and per-policy totals.
- Trends report daily approval count and weighted cost per policy.
- Empty reports return stable empty arrays, zero totals, and null timestamps.
- Handwritten reporting SQL is compiled into Go code by sqlc.
- The API uses a reconnecting PostgreSQL pool and a one-second analytics deadline.
- PostgreSQL availability is not required for `/v1/check`; analytics returns bounded HTTP 503 independently.
- The current approved-request schema cannot report rejected attempts or HTTP response time, and V15 does not infer them.
- `EXPLAIN ANALYZE` completed the current one-page scan in 0.099 milliseconds, so no additional index was justified yet.
- During the Compose outage experiment, analytics returned 503 in 1.008 seconds while a rate-limit decision returned 200 in 13.8 milliseconds and was later persisted after recovery.

## V16: operational decision metrics

The API now exposes Prometheus metrics for the complete live decision path while keeping billing history in PostgreSQL.

- `rate_limiter_decisions_total` counts completed decisions by bounded outcome and configured resource.
- `rate_limiter_decision_duration_seconds` records complete handler latency in aggregatable histogram buckets.
- `rate_limiter_in_flight_requests` reports checks currently executing in each API process.
- Outcomes distinguish allowed, quota-rejected, invalid-request, unknown-policy, backlog-full, dependency-unavailable, and unexpected internal failures.
- `client_id` is never a metric label. Unknown or unconfigured resources are normalized to `unknown` rather than becoming attacker-controlled time series.
- Prometheus scrapes both API instances every five seconds and retains samples in a named local volume for 30 days.
- Both Nginx gateways return HTTP 404 for `/metrics`; scraping occurs only through private Compose service addresses.
- Histograms are used because their buckets can be aggregated across API instances before calculating a percentile.
- Counters reset when an API process restarts; operational queries use `rate()` or `increase()` rather than treating raw values as durable history.
- The Compose experiment observed two healthy scrape targets and per-instance decision counts from traffic through the distributed gateway.
- Stopping Prometheus did not interrupt rate-limit decisions or liveness, and scraping recovered without restarting either API.
- Local Prometheus storage is neither replicated nor a billing ledger. Losing its volume can lose operational history without changing authoritative rate-limit or approved-usage state.

## V17: dashboard and alert evaluation

The raw operational and historical signals now have one reproducible operator view.

- Grafana is provisioned from repository files and combines Prometheus service health with PostgreSQL approved-usage history.
- Anonymous local access is Viewer-only; administrator credentials remain environment-configurable.
- A dedicated `grafana_reader` role has SELECT access to approved history and no write grant.
- Dashboard filters preserve bounded metric labels and do not introduce client identifiers into Prometheus.
- Prometheus evaluates availability, dependency, backlog, latency, and rejection-ratio alerts.
- Every alert rule has deterministic `promtool` coverage for its threshold and persistence behaviour.
- Stopping one API instance fires a warning while the distributed gateway continues serving through the remaining instance.
- Grafana, Prometheus, and PostgreSQL outage checks show that monitoring and historical reporting are not synchronous dependencies of a rate-limit decision.
- Alertmanager and external notification delivery are intentionally not configured because routing requires deployment-specific ownership and credentials.
- Grafana and Prometheus still run on the application host, so this phase does not claim monitoring failure-domain independence.

## Intentionally unmet requirements

The service is not production-ready:

- The local Redis topology does not survive loss of the Docker host and does not provide synchronous, lossless replication.
- Complete Redis unavailability still prevents authoritative decisions, but now fails closed within the configured deadline.
- There is no multi-host failure isolation, replicated monitoring storage, external alert delivery, or alert routing yet.
- A prolonged worker or PostgreSQL outage stops new approvals at the configured backlog limit; external alert delivery is not implemented yet.

## Current acceptance criteria

- Requests through a known policy are allowed until its capacity is exhausted.
- The next request receives HTTP 429 and a positive `retry_after_ms`.
- Capacity returns as approved costs leave the rolling window.
- Client/resource counters remain independent.
- Weighted costs are atomic in sequential execution; rejected costs do not consume capacity.
- Token capacity refills continuously according to elapsed time.
- Costs greater than token capacity are rejected as invalid requests.
- Single-instance request handling and latency are measured under a fixed-rate HTTP workload.
- Independent instances are shown to multiply the intended cluster-wide quota.
- Redis-backed instances share one quota and make each decision atomically.
- Idle Redis bucket keys expire instead of accumulating indefinitely.
- Runtime Redis loss returns bounded HTTP 503 responses while liveness remains healthy and readiness reports unavailable.
- Every successful approval creates one shared Redis Stream event; rejected decisions create none.
- Every consumed approval is stored once in PostgreSQL before its exact Redis entry is removed.
- Abandoned pending approvals can be claimed by another worker without duplicating history.
- A full approval-event backlog refuses new approvals without spending quota, and admission resumes after the worker drains it.
- Approved usage and weighted-cost trends are queryable for 10, 15, and 30-day periods without making PostgreSQL part of rate-limit enforcement.
- Every decision outcome and complete handler latency are exposed with bounded metric labels and can be aggregated across API instances.
- Prometheus failure does not stop rate-limit decisions, and public gateways do not expose the metrics endpoint.
- A provisioned dashboard displays live cluster health and durable approved usage without giving Grafana write access to the ledger.
- Availability and decision-quality alert rules pass deterministic tests, and one lost API instance alerts without stopping the distributed route.
- Redis primary loss promotes the replica and restores shared decisions without restarting the APIs.
- Redis bucket state survives complete Redis process replacement when the named AOF volumes are retained.
- `go test ./...` passes.
- `go test -race ./...` passes when run with a race-enabled Go toolchain.
- `go vet ./...` and `go build ./...` pass.
