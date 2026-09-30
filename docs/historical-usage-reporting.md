# Historical usage reporting

V13 created permanent approval history in PostgreSQL, but users could inspect it only by running SQL. V15 turns that durable data into a read-only HTTP contract while keeping PostgreSQL outside the synchronous rate-limit decision.

## What the stored data can prove

Each row represents one approval and includes its client, resource, weighted cost, approval time, and remaining capacity. It can accurately answer:

- how many requests were approved;
- how much weighted capacity was consumed;
- which client/resource policies produced that usage;
- how approved usage changed by day.

It cannot answer total attempts, rejected attempts, or average HTTP response time. Rejections deliberately create no billing event, and the Redis Lua script records an approval before the HTTP request has completed. V15 does not invent those missing values.

## Options considered

### Query Redis

Redis contains the active token balance and only the events not yet committed by the worker. Exact cleanup means processed history is intentionally absent, so Redis cannot provide 10, 15, or 30-day reporting.

### Add reporting fields to token buckets

Long-lived counters inside each bucket would mix temporary enforcement state with durable analytics. Expiring a correctly refilled bucket would then erase reporting history.

### Query PostgreSQL from the decision handler

PostgreSQL is the correct history store, but checking or updating it during `/v1/check` would make analytics availability part of quota enforcement.

### Add isolated read-only analytics handlers

The chosen design gives analytics handlers a reconnecting PostgreSQL pool and a one-second query deadline. The API can start and continue enforcing quotas when PostgreSQL is unavailable. Only analytics returns HTTP 503.

## API and query organization

The public endpoints are:

```text
GET /v1/analytics/summary?days=10
GET /v1/analytics/trends?days=10
```

Supported periods are exactly 10, 15, and 30 days. Optional `client_id` and `resource` parameters apply exact filters. Summary reports overall totals and per-policy totals; trends group the same measures by calendar day and policy.

Handwritten SQL remains in `sql/queries/approved_requests.sql`. sqlc generates the parameter and result types in `internal/database`. The handlers use those generated queries directly; no ORM, generic repository, or pass-through service layer was introduced.

The API uses a lazily connecting pool. An invalid database URL still fails startup because it is a configuration error. A valid but temporarily unreachable PostgreSQL server does not prevent Redis-backed enforcement from starting or continuing.

## Query-plan evidence

The existing schema already has an index on `approved_at`. No new index was added automatically.

On the local dataset, PostgreSQL selected a sequential scan because the table occupied one shared buffer page and the selected period contained most rows. The measured plan was:

```text
Seq Scan on approved_requests
Buffers: shared hit=1
Execution Time: 0.099 ms
```

An additional composite index would increase every worker insert without improving this measured query. As history grows and filters become selective, production query plans should be measured again. A client/resource/time index is a candidate only after those plans show the need.

## Failure-isolation evidence

The Compose experiment ran on 2026-09-29:

1. PostgreSQL was stopped while both APIs and Redis remained available.
2. Analytics returned bounded HTTP 503 after 1.008 seconds.
3. A real `/v1/check` request returned HTTP 200 in 13.8 milliseconds.
4. Redis retained the new approval event while PostgreSQL was unavailable.
5. PostgreSQL was restarted without restarting either API.
6. The event already delivered during the outage was recovered after the configured 30-second `XAUTOCLAIM` idle interval.
7. Redis returned to zero queued events and the report changed from 9 approvals/cost 2007 to 10 approvals/cost 2008.

This proves analytics and durable persistence can fail without blocking authoritative rate-limit decisions, subject to V14's finite backlog safety limit.

## Tradeoff carried forward

V15 provides accurate approved-usage reporting, not complete operational telemetry. Overall request volume, rejected decisions, error outcomes, and response-time percentiles require observing every HTTP decision. V16 should introduce low-cardinality operational metrics rather than overload the billing-history table with process telemetry.
