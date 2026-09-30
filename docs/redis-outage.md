# Redis outage experiment

Redis is a synchronous dependency of every rate-limit decision. This experiment records the current behavior when Redis becomes unavailable after both API instances have started. It does not add fallback behavior.

## Why this experiment exists

The qualification goal requires the service to continue handling controlled traffic during a temporary cache outage. Before choosing a fallback strategy, the current failure must be visible and measurable.

Three obvious reactions all have different risks:

- Failing open keeps callers moving but can exceed a provider's quota and create financial penalties.
- Failing closed protects the quota but turns a Redis outage into a complete availability outage.
- Giving every API instance a full in-memory fallback bucket recreates V7's quota-multiplication failure.

The evidence in this phase is used to design a bounded degraded mode in the following phase. No strategy is selected here.

## Automated evidence

`TestUnavailableRedisStopsRateLimitChecks` is an application-level integration test. It makes a successful request through the mounted HTTP router, closes the Redis client used by that application, and makes the same request again.

The test verifies that:

- the normal decision returns `200`;
- the unavailable dependency causes `/v1/check` to return `500`;
- the response body is the bounded `{"error":"rate-limit check failed"}` message rather than a Redis error;
- `/v1/health` still returns `200`, proving that it currently reports process liveness rather than dependency readiness.

Closing the client gives the automated suite a deterministic unavailable-dependency condition. It does not simulate reconnection or a Redis server outage. The Compose experiment below verifies the real runtime outage and recovery behavior.

## Reproduce the operational experiment

Run these commands from the repository root. They use the distributed route, so requests pass through Nginx and can reach either API instance.

```text
docker compose up -d --build
curl -i -X POST http://localhost:8084/v1/check -H "Content-Type: application/json" -d '{"client_id":"client-a","resource":"openai","cost":1}'
docker compose stop redis-primary redis-replica
curl -i -X POST http://localhost:8084/v1/check -H "Content-Type: application/json" -d '{"client_id":"client-a","resource":"openai","cost":1}'
curl -i http://localhost:8084/v1/health
docker compose ps
docker compose up -d --wait --wait-timeout 60 redis-primary redis-replica
curl -i -X POST http://localhost:8084/v1/check -H "Content-Type: application/json" -d '{"client_id":"client-a","resource":"openai","cost":1}'
```

Stopping both data nodes recreates the complete dependency outage in the current V10 topology. The failed request can take a few seconds because the Redis client attempts to reconnect before returning an error. Keep both API containers running throughout the experiment.

## Recorded result

The experiment was repeated on 2026-09-27 using the two four-CPU API containers behind the `8084` Nginx route.

| Condition | Operation | Result | Duration |
| --- | --- | ---: | ---: |
| Redis available | rate-limit check | HTTP 200 | 15.486 ms |
| Redis stopped | rate-limit check 1 | HTTP 500 | 4.435 s |
| Redis stopped | rate-limit check 2 | HTTP 500 | 2.360 s |
| Redis stopped | health check | HTTP 200 | 5.781 ms |
| Redis healthy again | first rate-limit check | HTTP 200 | 5.729 ms |

Both API containers remained running and Docker continued to mark them healthy while every tested rate-limit decision failed. Nginx therefore had no healthy alternative to select: both replicas depended on the same unavailable Redis service. The first failed request took longer while the Redis client made its initial reconnection attempts; the following failure still took more than two seconds.

The clients reconnected automatically after Redis became healthy; neither API process needed to restart. The first pre-outage and post-restart responses both reported 99 tokens remaining, but that value cannot prove whether the original bucket survived. A retained bucket and a newly created full bucket can both report 99 after spending one token. Persistence and replication remain separate experiments.

## Conclusion

The process is alive during a Redis outage, but the rate-limiting capability is unavailable. Returning `500` after several seconds for every decision violates the requirement to continue serving controlled traffic during temporary cache outages.

The existing health endpoint is correctly useful as a liveness signal, but it is not a readiness signal for the decision path. V10 reduces single-node outages through automatic failover. Complete Redis unavailability still requires the fail-fast and readiness behavior planned for the next phase.
