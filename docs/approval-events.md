# Atomic approved-request event stream

V12 adds the history that quota enforcement alone cannot provide. Every HTTP 200 rate-limit approval now leaves one event in a Redis Stream named `rate_limit:approved_requests` by default.

## The missing-history evidence

Before V12, a client/resource bucket contained only:

```text
tokens
last_refill_ms
```

Those fields describe the bucket's latest state. They cannot answer which requests were approved, when they were approved, or how much each request cost. Every new decision overwrites the aggregate fields, and the whole bucket expires after inactivity. This made the design suitable for enforcement but unsuitable for billing or historical analytics.

## Options considered

### Direct PostgreSQL writes

Writing a row before returning every decision would produce durable history immediately, but database latency and availability would become part of the rate-limit decision path. A reporting database outage could then stop enforcement.

### An in-process Go channel

A goroutine and channel would keep the request path fast, but buffered events would disappear during a crash, deployment, or forced restart. Separate API instances would also own separate volatile buffers.

### A separate message broker

RabbitMQ or Kafka could provide strong event-delivery features, but publishing after a Redis approval creates a dual-write gap. The process could stop after spending tokens and before publishing. Publishing first creates the opposite gap: an event could exist for a decision Redis never approved.

### Redis Streams inside the decision script

The chosen design uses `XADD` inside the existing Redis Lua script. It reuses the dependency already required for the decision, adds no second network round trip, and places the event next to the state mutation that produced it. A background worker and PostgreSQL remain deferred until the stream's growth demonstrates their need.

## Event contract

Each successful approval appends these fields:

| Field | Meaning |
| --- | --- |
| Redis stream ID | Ordered Redis-generated event identifier |
| `client_id` | Client whose quota was spent |
| `resource` | Protected external resource |
| `cost` | Weighted cost spent by this request |
| `approved_at_ms` | Approval time from Redis server time |
| `remaining` | Whole tokens available immediately after approval |

A weighted cost creates one event containing that cost. It does not create one event per token. HTTP 429 decisions create no event because no request was approved and no tokens were spent.

The stream name is configurable:

```text
RATE_LIMIT_EVENT_STREAM=rate_limit:approved_requests
```

## Decision flow

The Lua script now:

1. Verifies the bucket key is either absent or a Redis hash.
2. Verifies the event key is either absent or a Redis Stream.
3. Reads Redis server time.
4. Refills the token bucket.
5. Checks the complete weighted cost.
6. Stores the bucket state and refreshes its TTL.
7. Appends one stream event only when the decision is allowed.
8. Returns the existing decision response.

The type checks happen before any mutation. They prevent a predictable wrong-key-type error from occurring after the bucket has already changed.

## What “atomic” means here

Redis does not interleave another command while the Lua script is running. Both API instances therefore observe one ordered sequence of bucket mutations and stream appends.

Redis scripts are not rollback transactions. If an unexpected runtime failure occurs after an earlier write, Redis does not automatically undo that earlier write. The script performs all predictable validation before writing and places `XADD` as the final command before returning. Consequently, the API does not report HTTP 200 unless the stream append completed, but an exceptional server failure could consume tokens without returning an approval.

This phase also inherits V10's durability limits. Replication is asynchronous, and AOF `everysec` can lose approximately the latest second during a severe crash. The stream is a recoverable delivery buffer, not yet the permanent billing database.

## Why the stream is not trimmed

V12 had no durable consumer. Deleting or trimming entries at that point could have removed the only historical copy of an approval.

The deliberate V12 tradeoff is:

```text
every approval is retained
        ↓
the Redis Stream grows
        ↓
V13 needs a durable consumer and PostgreSQL
```

V13 now persists events idempotently and deletes only exact entries whose PostgreSQL transaction committed. Blind length- or age-based trimming is still not used. See [`durable-approval-history.md`](durable-approval-history.md) for the worker, delivery semantics, cleanup rule, and outage evidence.

## Application-level proof

`TestRedisRecordsApprovedRequests` constructs two applications with separate Redis clients and one isolated stream. One instance approves a weighted cost of two, the second approves a cost of one, and the next request is rejected. The test confirms:

- two approvals produce exactly two events;
- both API instances write to the same stream;
- the weighted costs are recorded as `2` and `1`;
- client and resource identities are recorded;
- Redis server approval time is present;
- the rejected request adds no event.

The existing shared-quota and Redis-outage tests continue to use isolated stream names, so tests do not leave production-shaped event data behind.

## Failover and persistence evidence

The Compose experiment ran on 2026-09-29.

Three approvals through the two-instance gateway produced a stream length of three on the initial primary. After that primary was killed, Sentinel promoted the replica. A new approval returned HTTP 200 and the promoted node reported a stream length of four. This proves that approvals before and after promotion continued in one replicated stream.

A separate experiment began with fresh volumes. Four approvals produced four entries. Both Redis data containers and all three Sentinel containers were then removed and recreated without deleting the named volumes. The restored primary still contained all four stream entries, including the original stream ID and fields. This proves AOF recovery for the observed experiment, subject to the documented `everysec` loss window.

## Latency comparison

V11 and V12 images were run against the same Redis container with identical four-CPU limits. Vegeta sent only requests that remained within the 5,000-token policy, so every measured request exercised the approval path and V12 executed `XADD`.

At 100 requests per second, three five-second trials produced these median-of-trial percentiles:

| Version | p50 | p95 | p99 | Responses |
| --- | ---: | ---: | ---: | ---: |
| V11 without the stream | 2.850 ms | 5.194 ms | 7.299 ms | 500 per trial |
| V12 with `XADD` | 2.878 ms | 5.027 ms | 7.542 ms | 500 per trial |

All requests received HTTP 200. The 0.028 millisecond p50 difference is too small to separate from local measurement noise. No low-load regression was demonstrated.

At 500 and 1,000 requests per second, both versions achieved the offered rate in almost every trial, but tail latency varied substantially between repeated runs. One 1,000 RPS V12 trial issued 3,996 of 4,000 intended requests. Because the API containers, Redis, Docker, and Vegeta shared one laptop, these higher-rate results cannot isolate `XADD` cost reliably. They are not presented as a production capacity claim.

## Inspecting the stream

After approving a request, inspect the stream with:

```text
docker compose exec redis-primary redis-cli XLEN rate_limit:approved_requests
docker compose exec redis-primary redis-cli XRANGE rate_limit:approved_requests - +
```

After Sentinel promotion, run the same commands against whichever data node Sentinel reports as the current primary.
