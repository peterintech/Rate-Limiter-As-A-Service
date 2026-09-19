# Global Rate Limiter

An intentionally evolutionary Go implementation of the qualification brief. The final destination is a highly available, cluster-accurate global rate limiter. Each new component is justified by a demonstrated failure in the preceding version.

## Current scope: V8

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
- A Redis-backed token bucket shared by every API instance
- One atomic Lua decision using Redis server time
- Expiring client/resource bucket keys with bounded state
- Comparable one-instance and two-instance Nginx routes for traffic testing
- Application wiring with Chi, injected interfaces, Zap logging, environment configuration, and graceful shutdown

Not included yet: Redis persistence or replication, degraded-mode behavior, Postgres, queues, analytics, dashboard, or HA.

## Run

```text
docker compose up -d --build
```

The Compose stack runs two API instances. Its comparison gateways expose one instance at `localhost:8083` and both instances at `localhost:8084`. Running the API directly requires Go 1.26 or newer; use `go run ./cmd/api` and configure its Redis address through `.env` or the process environment.

Redis Commander is available at `http://localhost:8082`. After making a rate-limit request, use it to inspect the bucket's token balance, last-refill timestamp, and remaining TTL.

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

## Repository layout

```text
cmd/api/                  composition, config, routing, handlers, HTTP helpers, lifecycle
benchmarks/               algorithm experiments and HTTP load-test inputs
deploy/nginx/             comparable single-instance and distributed gateways
internal/env/             environment lookup helper
internal/ratelimiter/     limiter contract and isolated algorithm implementations
internal/store/           external storage client construction
docs/                     requirements and API contract
Dockerfile                production-style API image
docker-compose.yaml       local Redis and replicated traffic-test topology
```

The HTTP layer owns request parsing and orchestration. Rate limiting sits behind a narrow interface and is injected into the application during startup. As in Psocial, no pass-through service layer is added before workflow complexity justifies one.

## Next milestone

Shared Redis state now preserves one quota across independent API instances. The next phase will demonstrate what happens when Redis becomes unavailable and use that evidence to design degraded-mode behavior.
