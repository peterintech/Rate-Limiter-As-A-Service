# Approval backlog protection

V13 made Redis a temporary delivery buffer: the API appends approved-request events and a separate worker moves them into PostgreSQL. That keeps database latency off the decision path, but it introduces a finite queue between two systems. If the worker or PostgreSQL remains unavailable, approvals can arrive faster than events leave and Redis memory can eventually be exhausted.

V14 makes that failure explicit before memory pressure chooses the outcome for us.

## Options considered

### Keep accepting until Redis runs out of memory

This maximizes short-term availability but has no controlled stopping point. Once Redis is full, quota updates and event writes can fail unpredictably, affecting every client rather than only new approvals.

### Trim or evict old events

Length-based trimming would keep memory bounded, but an event not yet stored in PostgreSQL could disappear permanently. Redis eviction is worse: it may remove token buckets or queued billing events without following the worker's commit-before-delete rule.

### Put PostgreSQL back on the request path

Writing synchronously would remove the queue, but a slow or unavailable analytics database would add latency to every decision. It would undo the isolation V13 deliberately introduced.

### Bound the queue and apply backpressure

The chosen design sets a maximum Redis Stream length. An otherwise approvable request is refused once the stream reaches that limit. This preserves existing events, protects Redis from unbounded queue growth, and lets the worker drain the backlog before approvals resume.

## Atomic admission rule

`RATE_LIMIT_EVENT_MAX_BACKLOG` defaults to `100000`. The Redis Lua script applies this order:

1. Refill the client's token bucket.
2. If the request already exceeds its quota, return the normal HTTP 429 decision.
3. If it could be approved, read the shared stream length.
4. If the stream is at its limit, return a backlog-full result without spending tokens or appending an event.
5. Otherwise spend the tokens and append the approval event in the same Redis execution.

The length check and `XADD` run atomically, so concurrent API instances cannot all observe the final free slot and overfill it. A full backlog maps to HTTP 503 because the service temporarily cannot accept a durable approval. It does not count as a Redis dependency failure and therefore does not open the circuit breaker.

Quota rejection keeps its original meaning. A request that lacks tokens still returns HTTP 429 even while the history backlog is full because it would not create an event.

## Why Redis uses `noeviction`

Both Redis data nodes have a 256 MiB memory ceiling and `maxmemory-policy noeviction`. The ceiling makes the local deployment's resource budget explicit. `noeviction` makes a failed write visible instead of silently deleting authoritative quota state or unpersisted history.

The stream-entry limit is the primary guard. Entry count is deliberately simple and deterministic, but it is not an exact byte measurement: event field sizes and Redis overhead vary. A production limit must be chosen from measured average and worst-case entry size with headroom for token buckets, replication, persistence, and operational commands. Redis memory alerts remain necessary.

## Worker operational endpoints

The worker listens on port `8081` inside Compose and `8085` on the host.

| Endpoint | Meaning |
| --- | --- |
| `GET /v1/health` | The worker process is alive. It does not contact dependencies. |
| `GET /v1/readiness` | Redis and PostgreSQL are reachable within the request deadline. |
| `GET /v1/status` | Current stream length, pending count, oldest-event age, configured limit, and percentage used. |

Example:

```text
curl http://localhost:8085/v1/status
```

The monitor logs only state transitions rather than repeating the same warning every interval:

- the warning threshold is crossed;
- the backlog becomes full and admission stops;
- the backlog falls below the limit and admission resumes;
- the warning threshold is cleared;
- a non-empty backlog becomes fully drained.

The warning threshold defaults to 80 percent and is configured with `APPROVAL_WORKER_BACKLOG_WARNING_PERCENT`. These logs are local signals, not an external paging system.

## Proof

Application-level Redis tests establish the safety properties with isolated streams:

- two approvals fill a two-entry backlog;
- a quota-invalid request still returns 429;
- the next approvable request returns 503;
- that 503 neither spends its final token nor appends an event;
- deleting one test event allows the same request to succeed;
- two API instances issuing requests concurrently create exactly five events against a shared five-entry limit, with every other approvable request refused;
- the worker status endpoint reports stream length, pending entries, configured limit, and utilization.

The Compose experiment ran on 2026-09-29 with a disposable API and worker using an isolated stream and a deliberately small three-entry limit. The normal worker was stopped so the test represented a real consumer outage.

- The first three requests returned HTTP 200 and the isolated stream reached length three.
- The fourth and a repeated request returned HTTP 503.
- The Redis token value was `4999` immediately before and after the repeated blocked request, proving the refusal did not spend quota.
- A disposable worker persisted all three entries and reduced stream length to zero.
- The next request returned HTTP 200 without restarting the API.
- The restored normal worker reported an empty stream and healthy status with the production-shaped limit of 100000.

The experiment containers were removed afterward. It proves the admission and recovery mechanics on one computer; it does not select a production limit or establish how long operators may leave the pipeline unavailable.

## Tradeoff carried forward

Backpressure protects data integrity and Redis stability by reducing decision availability during a prolonged history-pipeline outage. This is intentional fail-closed behavior: the service never reports an approval whose history event it could not accept. The next operational improvement is to send the worker's warning and full signals to a real monitoring system so an operator can act before clients see HTTP 503.
