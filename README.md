# Global Rate Limiter

An intentionally evolutionary Go implementation of the qualification brief. The final destination is a highly available, cluster-accurate global rate limiter. Each new component is justified by a demonstrated failure in the preceding version.

## Current scope: V2

- Contract and explicit assumptions in [`docs/requirements.md`](docs/requirements.md)
- Final qualification target in [`docs/goal.md`](docs/goal.md)
- HTTP contract in [`docs/api.md`](docs/api.md)
- Static per-client/resource policies
- In-memory aligned fixed-window counters
- An HTTP test that exercises the configured limiter through quota exhaustion
- Atomic rate-limit decisions within one process
- A concurrent HTTP test that verifies the configured limit is not exceeded
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
```

The race test requires a race-enabled Go toolchain with a C compiler installed.

## Repository layout

```text
cmd/api/                  composition, config, routing, handlers, HTTP helpers, lifecycle
internal/env/             environment lookup helper
internal/ratelimiter/     limiter interface and fixed-window implementation
docs/                     requirements and API contract
```

The HTTP layer owns request parsing and orchestration. Rate limiting sits behind a narrow interface and is injected into the application during startup. As in Psocial, no pass-through service layer is added before workflow complexity justifies one.

## Next milestone

The next phase will measure the burst allowed across a fixed-window boundary before introducing or comparing another algorithm.
