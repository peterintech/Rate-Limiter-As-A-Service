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

An end-to-end Vegeta test establishes a verified local capacity floor of 2,000 HTTP requests per second for one API instance under the selected latency and response criteria. Fixed-rate stages measure how many requests the server receives and answers while the real token bucket remains active. Higher rates are not repeatable on the same-machine test environment, so the result is documented as a floor rather than an exact maximum.

Two independently constructed application instances then receive the same policy and fixed clock. Each instance approves the policy's full capacity because each owns a separate in-memory token bucket.

- Request rate is measured separately from users and connections and is not presented as a universal production limit.
- Every HTTP response counts as handled traffic regardless of status; missing responses and transport errors are recorded separately.
- A policy intended to allow three requests across the cluster permits three requests on each instance.
- Two instances therefore approve six requests before both reject further traffic.
- The experiment changes no production behavior or public contract.
- Cluster-wide enforcement requires shared state with one atomic decision across all instances.
- Redis remains deferred until the next phase; this phase establishes the failure that justifies it.

## Intentionally unmet requirements

The service is not production-ready:

- In-memory state disappears on restart.
- Separate processes do not share state; the multi-instance experiment demonstrates the resulting quota multiplication.
- There is no Redis, Postgres, queue, durable logging, analytics dashboard, Nginx, containerization, or HA/fail-safe strategy yet.
- Distributed load, Redis-outage, and failover experiments remain deferred until their corresponding components exist.

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
- `go test ./...` passes.
- `go test -race ./...` passes when run with a race-enabled Go toolchain.
- `go vet ./...` and `go build ./...` pass.
