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

## Intentionally unmet requirements

V1 is not production-ready:

- The map is deliberately unsynchronized. Concurrent HTTP requests may race; V2 will demonstrate this with the Go race detector before adding synchronization.
- Fixed windows permit boundary bursts; a later experiment will make that behavior measurable before comparing algorithms.
- Counters grow without cleanup and disappear on restart.
- Separate processes do not share state, so this is not yet a global limiter.
- There is no Redis, Postgres, queue, durable logging, analytics dashboard, Nginx, containerization, or HA/fail-safe strategy yet.
- Load, performance, and race-condition tests are deferred until their corresponding failure milestones. Ordinary correctness tests belong to V1.

## V1 acceptance criteria

- Requests through a known policy are allowed until its capacity is exhausted.
- The next request receives HTTP 429 and a positive `retry_after_ms`.
- Capacity resets at the next aligned window boundary.
- Client/resource counters remain independent.
- Weighted costs are atomic in sequential execution; rejected costs do not consume capacity.
- `go test ./...` passes.
- `go vet ./...` and `go build ./...` pass.
