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

## Intentionally unmet requirements

The service is not production-ready:

- Sliding-log state grows with the number of approved requests inside the active interval; the next phase will compare it with a bounded-state token bucket.
- In-memory state disappears on restart.
- Separate processes do not share state, so this is not yet a global limiter.
- There is no Redis, Postgres, queue, durable logging, analytics dashboard, Nginx, containerization, or HA/fail-safe strategy yet.
- Load, performance, and race-condition tests are deferred until their corresponding failure milestones. Ordinary correctness tests belong to V1.

## Current acceptance criteria

- Requests through a known policy are allowed until its capacity is exhausted.
- The next request receives HTTP 429 and a positive `retry_after_ms`.
- Capacity returns as approved costs leave the rolling window.
- Client/resource counters remain independent.
- Weighted costs are atomic in sequential execution; rejected costs do not consume capacity.
- `go test ./...` passes.
- `go test -race ./...` passes when run with a race-enabled Go toolchain.
- `go vet ./...` and `go build ./...` pass.
