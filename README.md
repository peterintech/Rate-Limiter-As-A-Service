# Global Rate Limiter

An intentionally evolutionary Go implementation of the qualification brief. The final destination is a highly available, cluster-accurate global rate limiter, but the current `V1` is deliberately a naive single-process fixed-window limiter. Each later component must be justified by a demonstrated failure in the preceding version.

## Current scope: V0/V1

- Contract and explicit assumptions in [`docs/requirements.md`](docs/requirements.md)
- Final qualification target in [`docs/goal.md`](docs/goal.md)
- HTTP contract in [`docs/api.md`](docs/api.md)
- Static per-client/resource policies
- In-memory aligned fixed-window counters
- An HTTP test that exercises the configured limiter through quota exhaustion
- Application wiring with Chi, injected interfaces, Zap logging, environment configuration, and graceful shutdown

Not included yet: synchronization, Redis, Postgres, queues, analytics, dashboard, Nginx, Docker, HA, or fail-safe behavior.

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
```

Do not treat `go test -race ./...` as a V1 acceptance check. The lack of synchronization is intentional and is the failure V2 will make reproducible before fixing it.

## Repository layout

```text
cmd/api/                  composition, config, routing, handlers, HTTP helpers, lifecycle
internal/env/             environment lookup helper
internal/ratelimiter/     limiter interface and V1 fixed-window implementation
docs/                     requirements and API contract
```

The HTTP layer owns request parsing and orchestration. Rate limiting sits behind a narrow interface and is injected into the application during startup. As in Psocial, no pass-through service layer is added before workflow complexity justifies one.

## Next milestone

V2 will add a repeatable concurrent workload that exposes V1's race/correctness failure. Only then will synchronization be introduced and benchmarked.
