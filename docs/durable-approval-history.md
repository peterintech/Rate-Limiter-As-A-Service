# Durable approval history

V12 made every reported approval recoverable by appending it to a Redis Stream inside the token-bucket decision. It also deliberately left the stream untrimmed. V13 adds the consumer that makes the event permanent before removing it from Redis.

## Why Redis cannot remain the history database

Redis holds the active token buckets because those decisions need low latency. Approval history has different requirements: it grows continuously, must support reporting, and should survive beyond the lifetime of an enforcement key.

At 1,000 approvals per second, an untrimmed stream receives 86.4 million entries per day. Adding a larger Redis memory limit only postpones the same problem. V13 therefore treats the stream as a temporary delivery buffer and PostgreSQL as the durable system of record.

## Options considered

### Write PostgreSQL inside the HTTP request

This would make the event durable immediately, but every rate-limit decision would inherit PostgreSQL latency and availability. A reporting database outage could stop quota enforcement even though Redis was healthy.

### Delete events by stream length or age

`XTRIM MAXLEN` or age-based deletion would bound memory, but it could remove events that had not reached PostgreSQL during a long database outage. That is unacceptable for billing history.

### Persist asynchronously and delete exact committed entries

The selected design gives the worker a Redis consumer group. It stores each delivered event in PostgreSQL and removes only the exact stream IDs whose database transaction committed.

The safety rule is:

```text
not committed to PostgreSQL -> never delete from Redis
committed to PostgreSQL     -> eligible for acknowledgement and deletion
```

## Components and ownership

```text
API instances
    |
    | token spend and XADD in one Redis script
    v
Redis Stream
    |
    | XREADGROUP
    v
cmd/worker
    |
    | sqlc-generated query in one PostgreSQL transaction
    v
PostgreSQL
    |
    | transaction committed
    v
XACK and XDEL for those exact stream IDs
```

The worker is a separate process instead of a goroutine inside each API. API and worker instances can restart and scale independently, and a worker failure does not stop HTTP decisions. V13 does not introduce a generic message-bus abstraction; it implements the one approval-history workflow currently required.

## Database organization

```text
sql/schema/                 Goose migrations
sql/queries/                handwritten SQL queries
internal/database/          sqlc-generated Go code
internal/store/postgres.go  pgx pool construction
```

Goose owns ordered and reversible schema changes. sqlc validates the handwritten queries and generates type-safe Go methods. pgx provides the runtime PostgreSQL pool. No ORM or application-startup auto-migration is added.

The first table uses the Redis stream ID as its primary key. The insert uses `ON CONFLICT (stream_id) DO NOTHING`, so a repeated delivery cannot create a second billing row.

Generate database code after changing a query or schema:

```text
go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1
sqlc generate
```

For a database outside Compose, install Goose and apply the migrations explicitly:

```text
go install github.com/pressly/goose/v3/cmd/goose@v3.28.0
goose -dir sql/schema postgres "postgres://rate_limiter:rate_limiter@localhost:5433/rate_limiter?sslmode=disable" up
```

In the local Compose environment, a dedicated one-shot `migrate` container runs `goose up` before the worker starts. The API and worker never modify the database schema themselves.

## Delivery guarantee

The worker provides at-least-once delivery with idempotent database writes. It does not claim exactly-once delivery.

For each batch it:

1. Parses every event before opening a transaction.
2. Inserts the events through sqlc-generated queries.
3. Commits the PostgreSQL transaction.
4. Atomically sends `XACK` and `XDEL` for the committed stream IDs through a Redis transaction.

If the worker stops before the database commit, the event remains pending. If it stops after the commit but before Redis cleanup, Redis can deliver the event again and the primary key prevents a duplicate row. Cleanup failure can temporarily leak Redis memory, but it cannot erase the committed PostgreSQL history.

V13 has one durable consumer group. Deleting an entry after that group commits is therefore safe. A future second consumer group would require coordinating retention across every group rather than deleting after only one group acknowledges it.

## Recovering a stopped worker

Redis records delivered but unacknowledged entries in the consumer group's Pending Entries List. A replacement worker periodically uses `XAUTOCLAIM` to take entries that have been idle for at least `APPROVAL_WORKER_CLAIM_IDLE`.

The default 30-second idle period avoids stealing work from a slow but active consumer. It also means recovery is deliberately not instantaneous: an event already delivered when PostgreSQL fails may wait for that interval after PostgreSQL returns.

## Configuration

| Variable                         | Default                 | Purpose                                            |
| -------------------------------- | ----------------------- | -------------------------------------------------- |
| `DATABASE_URL`                   | required                | PostgreSQL connection string                       |
| `APPROVAL_WORKER_GROUP`          | `approval-history`      | Durable Redis consumer-group name                  |
| `APPROVAL_WORKER_CONSUMER`       | hostname and process ID | Unique worker identity                             |
| `APPROVAL_WORKER_BATCH_SIZE`     | `100`                   | Maximum events processed per database transaction  |
| `APPROVAL_WORKER_BLOCK`          | `1s`                    | Maximum blocking wait for new events               |
| `APPROVAL_WORKER_CLAIM_IDLE`     | `30s`                   | Minimum idle time before abandoned work is claimed |
| `APPROVAL_WORKER_CLAIM_INTERVAL` | `10s`                   | Frequency of abandoned-work checks                 |
| `APPROVAL_WORKER_RETRY_BACKOFF`  | `1s`                    | Delay after a dependency failure                   |

The worker uses its own Redis connection. Its read timeout is longer than its blocking-read duration, so a normal `XREADGROUP` wait is not mistaken for a dependency timeout.

## Application-level proof

The worker tests use real Redis and PostgreSQL dependencies:

- one event is committed to PostgreSQL before its stream entry is removed;
- an event left pending by a stopped consumer is claimed by a replacement;
- an already-existing `stream_id` remains one database row after redelivery;
- successful cleanup leaves both stream length and pending count at zero.

Run them against the Compose dependencies:

```text
REDIS_TEST_ADDR=localhost:6381 DATABASE_TEST_URL="postgres://rate_limiter:rate_limiter@localhost:5433/rate_limiter?sslmode=disable" go test -v ./cmd/worker
```

The environment-prefix syntax above is POSIX-shell notation. The values may instead be placed in the process environment using the terminal's normal environment mechanism.

## Outage evidence

The Compose experiment ran on 2026-09-29.

### Worker unavailable

The worker was stopped while both APIs and their dependencies remained healthy. PostgreSQL contained 501 rows. Three requests through the distributed gateway all returned HTTP 200. PostgreSQL remained at 501 while Redis stream length grew to three.

After the worker restarted, PostgreSQL contained 504 rows and Redis stream length returned to zero. No API restart was required.

### PostgreSQL unavailable

PostgreSQL was stopped while the APIs, Redis, and worker stayed active. Two requests both returned HTTP 200, proving durable history remains outside the synchronous decision path. Redis retained two entries.

After PostgreSQL returned, one not-yet-delivered entry persisted immediately. The other had already entered Redis's pending list during the outage. Once its 30-second claim-idle period elapsed, the worker reclaimed it. PostgreSQL increased from 504 to 506 rows, stream length returned to zero, and the consumer group had no pending entries.

These results prove buffering and recovery on one computer. They do not establish a safe maximum backlog or a production recovery-time objective.

## Remaining limitation

Redis is now drained during normal operation, but an indefinitely unavailable PostgreSQL database still causes the stream to grow. There is no alert on stream age, pending count, worker lag, or Redis memory pressure yet. Blind trimming remains forbidden because it would trade memory safety for silent billing-data loss.

V14 will measure and expose that backlog, then define an explicit backpressure policy before Redis reaches its memory limit.
