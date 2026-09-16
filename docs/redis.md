# Shared Redis token bucket

Phase 7 demonstrated that two in-memory limiters each grant a complete copy of the configured quota. Redis is introduced to solve that measured problem: every API instance must read and update one shared bucket for a client/resource pair.

## Ownership

`internal/store` constructs the Redis client. `cmd/api` configures it, verifies the connection, owns its lifecycle, and injects it into the Redis token bucket. `internal/ratelimiter/redis-token-bucket.go` implements the existing `ratelimiter.Limiter` interface alongside the other isolated algorithm files. HTTP handlers therefore do not know whether a decision comes from memory or Redis.

The application uses these environment values:

| Variable | Default | Purpose |
| --- | --- | --- |
| `REDIS_ADDR` | `localhost:6379` | Redis server address |
| `REDIS_PASSWORD` | empty | Redis authentication password |
| `REDIS_DB` | `0` | Redis logical database |

Startup fails immediately if Redis cannot be reached. Runtime outage behavior is deliberately not hidden by a fallback yet; that failure is the evidence required for the next phase.

## Bucket state

One encoded Redis key represents one client/resource policy:

```text
rate_limit:<encoded-client-id>:<encoded-resource>
```

The hash stores two fields:

```text
tokens
last_refill_ms
```

The state remains constant in size regardless of how many requests use the bucket. It does not retain request history.

## Atomic decision

One Lua script executes the complete decision inside Redis:

1. Read Redis server time and the existing bucket.
2. Create a full bucket when no state exists.
3. Refill tokens according to elapsed time, capped at the policy limit.
4. Check whether the complete weighted cost is available.
5. Deduct the cost only when the request is allowed.
6. Store the new balance and refill time.
7. Refresh the bucket expiry and return the decision metadata.

Using separate `GET` and `SET` commands would leave a race between instances: both could read the same balance and both could approve it. Redis runs the Lua script atomically, so no other decision can modify that key midway through the operation.

Redis server time prevents application clock differences from producing inconsistent refill calculations. It also keeps time acquisition inside the same atomic operation as the state transition.

## Key expiry

Every decision sets the key TTL to twice the policy window. An empty bucket needs one complete window to refill, so expiry occurs only after it would already be full. Recreating an expired bucket at full capacity therefore preserves the rate-limit meaning while allowing inactive client/resource keys to leave Redis.

Repeated traffic refreshes the TTL because the bucket is still active. This phase adds no Redis persistence: a Redis restart still resets all buckets, and that limitation remains explicit.

## Local verification

Start Redis and run the suite:

```bash
docker compose up -d
go test -v ./...
```

`TestRedisSharesQuotaAcrossInstances` constructs two applications with separate Redis clients. Requests alternate between their HTTP routers. Their combined approvals equal the configured quota, and the next request receives HTTP 429.

## Inspecting bucket state

The Compose stack includes Redis Commander at `http://localhost:8082`. It connects to Redis through the Compose service name and requires no separate host configuration.

After calling `POST /v1/check`, open the `rate_limit:*` key to inspect its `tokens` and `last_refill_ms` hash fields. Inactive keys disappear after their TTL, so make a new request if the expected key is no longer visible.
