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
- A two-route Nginx and Compose topology that measures a single instance against a distributed pair
- A capacity experiment that records the limits of the shared local test harness without presenting them as application capacity
- An availability experiment that stops instances mid-run and measures failed requests on each route
- An end-to-end measurement of the cluster-wide quota multiplication factor
- A multi-instance experiment that demonstrates quota multiplication with isolated in-memory state
- Application wiring with Chi, injected interfaces, Zap logging, environment configuration, and graceful shutdown

The Nginx and Compose files are measurement scaffolding for the load experiments, not a shipped deployment. There is no image publishing, TLS, health-gated rollout, or restart policy.

Not included yet: Redis, Postgres, queues, analytics, dashboard, HA, or fail-safe behavior.

## What V7 established

| Question | Answer |
| --- | --- |
| Does distributing raise capacity? | Inconclusive in this environment. The local harness sustained 7,000 RPS on the single route and 6,000 RPS on the distributed route, but the load generator, proxy, APIs, and unrelated containers competed for the same 8 logical CPUs. |
| Does distributing raise availability? | Yes. Zero failed requests across two 30-second instance outages, against 850 failures for a single instance. |
| Does the cluster still enforce one quota? | No. The effective quota multiplied by exactly 2.00 with two instances. |

The capacity numbers describe this complete local harness, not the API alone. They neither prove that distribution raises capacity nor prove that it cannot. See [`docs/load-testing.md`](docs/load-testing.md) for the experiment, the results, and the decision they justify.

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
deploy/nginx/             measurement front door, quiet and observability variants
internal/env/             environment lookup helper
internal/ratelimiter/     limiter contract and isolated algorithm implementations
docs/                     requirements and API contract
compose.yaml              V7 measurement topology
Dockerfile                API image for the measurement topology
```

The HTTP layer owns request parsing and orchestration. Rate limiting sits behind a narrow interface and is injected into the application during startup. As in Psocial, no pass-through service layer is added before workflow complexity justifies one.

## Next milestone

Distribution is justified here by availability: one API process can stop while the other continues making rate-limit decisions. Capacity remains unproven because the test environment did not isolate the system from the load generator and unrelated workloads. The remaining correctness failure is quantified end to end: every instance grants another full copy of the quota, so the effective cluster-wide allowance multiplies by the instance count. The next phase introduces shared Redis state so every application instance participates in one atomic token-bucket decision. Redis outage behavior and degraded-mode fallback remain deferred until shared enforcement is working and its failure can be demonstrated.
