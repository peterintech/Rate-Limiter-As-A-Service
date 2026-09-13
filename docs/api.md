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

Errors use `{ "error": "message" }`:

- `400` for invalid JSON or missing/invalid fields.
- `404` when the client/resource pair has no configured policy.
- `500` when the limiter cannot make a decision.

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
