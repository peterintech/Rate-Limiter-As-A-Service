# Global Rate Limiter

An intentionally evolutionary Go implementation of the qualification brief. The final destination is a highly available, cluster-accurate global rate limiter. Each new component is justified by a demonstrated failure in the preceding version.

## Current scope: V7

- Contract and explicit assumptions in [`docs/requirements.md`](docs/requirements.md)
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
- Application wiring with Chi, injected interfaces, Zap logging, environment configuration, and graceful shutdown

Not included yet: Redis, Postgres, queues, analytics, dashboard, Nginx, Docker, HA, or fail-safe behavior.

## Run

Requires Go 1.26 or newer.

```bash
go run ./cmd/api
```

The service listens on `:8080`. Copy `.env.example` to `.env` to configure `PORT` and `ENV`, or set them in the process environment.

```bash
curl -i -X POST http://localhost:8080/v1/check \
  -H "Content-Type: application/json" \
  -d '{"client_id":"client-a","resource":"openai","cost":1}'
```

The demo policies are `client-a/openai` at 100 requests/minute and `client-b/stripe` at 5000 requests/minute.

## Test

```bash
go test ./...
go test -race ./...
go test -run '^$' -bench=BenchmarkLimiterState -benchmem -benchtime=20x ./benchmarks
```

The race test requires a race-enabled Go toolchain with a C compiler installed.
See [`docs/benchmarks.md`](docs/benchmarks.md) for the state-growth experiment and representative results.
See [`docs/load-testing.md`](docs/load-testing.md) for the single-instance capacity experiment, Vegeta commands, results, and interpretation.

## Repository layout

```text
cmd/api/                  composition, config, routing, handlers, HTTP helpers, lifecycle
benchmarks/               algorithm experiments and HTTP load-test inputs
internal/env/             environment lookup helper
internal/ratelimiter/     limiter contract and isolated algorithm implementations
docs/                     requirements and API contract
```

The HTTP layer owns request parsing and orchestration. Rate limiting sits behind a narrow interface and is injected into the application during startup. As in Psocial, no pass-through service layer is added before workflow complexity justifies one.

## Next milestone

Single-instance measurements justify horizontal scaling, while the multi-instance experiment shows why isolated memory cannot preserve a global quota. The next phase will introduce shared Redis state so every application instance participates in one atomic token-bucket decision. Redis outage behavior and degraded-mode fallback remain deferred until shared enforcement is working and its failure can be demonstrated.
