# HTTP API contract

## `POST /v1/check`

Request:

```json
{
  "client_id": "client-a",
  "resource": "openai",
  "cost": 1
}
```

`cost` is a positive integer and defaults to nothing: callers must send it explicitly. The configured policy is selected by the exact `(client_id, resource)` pair.

Allowed response (`200 OK`):

```json
{
  "allowed": true,
  "limit": 100,
  "remaining": 99,
  "reset_at": "2026-09-12T12:01:00.000Z",
  "retry_after_ms": 0
}
```

Denied response (`429 Too Many Requests`) uses the same schema with `allowed: false` and a positive `retry_after_ms`. A denied request does not consume quota.

`reset_at` is the estimated time when the active limiter regains full capacity. For a denied request, `retry_after_ms` reports when enough capacity exists for that complete cost.

Before an allowed response is returned, the Redis-backed limiter appends one internal approved-request event containing the client, resource, weighted cost, Redis approval time, and remaining capacity. This does not change the public response schema. Denied requests append no event.

Errors use `{ "error": "message" }`:

- `400` for invalid JSON or missing/invalid fields.
- `404` when the client/resource pair has no configured policy.
- `503` when the shared rate-limit dependency is temporarily unavailable or the approval-event backlog is full. This response includes `Retry-After`, never grants local fallback capacity, and does not spend quota.
- `500` for an unexpected internal failure.

## `GET /v1/health`

Returns `200 OK` with the service status, environment, and version:

```json
{
  "status": "ok",
  "env": "development",
  "version": "0.1.0"
}
```

This is only a process liveness signal.

## `GET /v1/readiness`

Returns `200 OK` when the API can reach Redis and the circuit breaker can attempt a rate-limit decision:

```json
{
  "status": "ready",
  "env": "development",
  "version": "0.1.0"
}
```

Returns `503 Service Unavailable` with `status: "not ready"` when the API cannot currently make authoritative decisions. Readiness does not spend quota.

## `GET /v1/analytics/summary`

Returns approved usage from PostgreSQL for a trailing 10, 15, or 30-day period:

```text
GET /v1/analytics/summary?days=10
GET /v1/analytics/summary?days=30&client_id=client-b&resource=stripe
```

`client_id` and `resource` are optional exact-match filters. The response contains total approvals, total weighted cost, the first and latest approval, and a client/resource breakdown. Empty results return zero totals, `null` timestamps, and an empty `policies` array.

## `GET /v1/analytics/trends`

Returns daily approved-request counts and weighted costs for the same periods and optional filters:

```text
GET /v1/analytics/trends?days=15&client_id=client-b
```

Both analytics endpoints return `400` unless `days` is exactly `10`, `15`, or `30`. They return a bounded `503` when PostgreSQL is unavailable. Analytics failure does not affect `/v1/check` because PostgreSQL is not part of the rate-limit decision path.

These endpoints report approved usage only. The current durable event contract does not contain rejected attempts or HTTP response latency, so those values are not inferred from incomplete data.
