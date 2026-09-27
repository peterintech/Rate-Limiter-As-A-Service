# V0/V1 Requirements

The qualification brief is the destination: a highly available global rate-limiter service with cluster-wide accuracy, millisecond decisions, degraded-mode behavior, durable approved-request logging, analytics, dashboarding, tests, and one-command deployment.

This repository deliberately starts before that destination. New components are added only after a test or experiment demonstrates the failure that requires them.

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
- `internal/ratelimiter` owns the limiter contract and its fixed-window implementation.
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

Distribution is adopted for a specific reason, and this phase measures whether that reason holds. Three questions are separated because they have different answers and different evidence.

**Capacity.** A containerized two-route experiment puts one Nginx in front of two independent API processes, each limited to four CPUs, and compares it against a single instance through the same proxy. Offered rates from 250 to 10,000 requests per second are measured with three 30-second trials per rate and a 60-second confirmation at the candidate boundary, while the real token bucket remains active. The complete local harness sustained 7,000 requests per second on the single route and 6,000 on the distributed route under the chosen criteria. These are not isolated API capacity floors. The load generator, proxy, APIs, and unrelated containers competed for the same eight logical CPUs, while the API containers remained below their CPU limits. The experiment therefore cannot prove whether adding instances raises application capacity.

**Availability.** A steady 100 requests per second is held for 150 seconds while instances are stopped and restarted twice mid-run. The rate is chosen so the quota never exhausts, so every failure is an availability failure. The distributed route lost no client-visible request. A single-instance control lost 850 requests during the one outage that affected it. Distribution buys availability, which is the reason to adopt it.

**Global correctness.** Each instance holds a private in-memory token bucket, so a policy intended to allow one quota across the cluster allows one quota per instance. At 600 requests per second against a 5,000-per-minute policy, the single route approved 8,332 requests where the distributed route approved 16,664 — a factor of exactly 2.00. Two deterministic application instances with a fixed clock show the same result: a policy intended to allow three requests cluster-wide allows three on each instance and six in total.

- Request rate is measured separately from users and connections and is not presented as a universal production limit.
- Every HTTP response counts as handled traffic regardless of status; missing responses and transport errors are recorded separately.
- Latency figures are read from Vegeta's JSON report, which emits nanoseconds, and are divided by 1,000,000 when reported as milliseconds.
- Trials are recorded only after a preflight confirms the single-instance route reaches exactly one process and the distributed route reaches both.
- The API processes are restarted once per topology-and-rate group rather than before each trial, because a cold process penalizes a 30-second window. The documented results use that corrected method.
- The experiment changes no production behavior or public contract. The measured deployments are measurement scaffolding, not a shipped orchestration.
- A capacity claim requires the load generator and system under test to run on isolated resources without unrelated workloads.
- Cluster-wide enforcement requires shared state with one atomic decision across all instances.
- Redis remains deferred until the next phase; this phase establishes the failure that justifies it.

## Intentionally unmet requirements

The service is not production-ready:

- In-memory state disappears on restart.
- Separate processes do not share state; both the deterministic test and the end-to-end runs demonstrate the resulting quota multiplication.
- The Nginx and Compose topology exists to measure the system, not to run it. It has no image publishing, no TLS, no health-gated rollout, and no restart policy.
- There is no Redis, Postgres, queue, durable logging, or analytics dashboard.
- There is no shared-state component, so no Redis-outage, degraded-mode, or fail-safe experiment is possible yet.
- The distributed capacity result is bounded by a single shared host. A claim that scaling out adds capacity requires load generators and application instances on separate hosts.

## Current acceptance criteria

- Requests through a known policy are allowed until its capacity is exhausted.
- The next request receives HTTP 429 and a positive `retry_after_ms`.
- Capacity returns as approved costs leave the rolling window.
- Client/resource counters remain independent.
- Weighted costs are atomic in sequential execution; rejected costs do not consume capacity.
- Token capacity refills continuously according to elapsed time.
- Costs greater than token capacity are rejected as invalid requests.
- Single-instance and distributed request handling and latency are measured under a fixed-rate HTTP workload.
- A single instance and a two-instance pair are compared at equal total application CPU, and the result is reported whether or not distribution wins.
- Stopping one instance produces no failed request on the distributed route, and the single-instance control is measured to show the contrast.
- The cluster-wide quota multiplication factor is measured end to end, not only asserted in a unit test.
- `go test ./...` passes.
- `go test -race ./...` passes when run with a race-enabled Go toolchain.
- `go vet ./...` and `go build ./...` pass.
