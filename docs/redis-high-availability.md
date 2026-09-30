# Redis high availability and state durability

V9 showed that one unavailable Redis process stops every rate-limit decision. V10 addresses two different failure modes: Sentinel promotes a replica when the writable node fails, and AOF restores bucket state when all Redis processes are replaced.

## Terminology

### Primary and replica

The **primary** is the Redis node that accepts changes to rate-limit state. When a request spends tokens, the Lua script updates the bucket on this node.

The **replica** is a second Redis node that continuously copies the primary's data. It normally does not accept our writes. Its purpose is to hold a recent copy that can take over if the primary fails.

Redis replication is asynchronous: the primary can accept a change shortly before failing, before the replica has copied it. Replication therefore improves availability but does not guarantee that the latest write can never be lost.

### Sentinel and quorum

A **Sentinel** is a small Redis monitoring process. It does not store token buckets or serve rate-limit decisions. It watches the data nodes, reports their health, tells clients which node is currently primary, and coordinates failover.

The stack runs three Sentinels so one mistaken or isolated monitor cannot replace the primary by itself. A **quorum** of two means at least two Sentinels must agree that the primary is unavailable. They then elect a leader to coordinate the failover.

**Promotion** is the act of changing the replica into the new writable primary. When the old primary returns, Sentinel reconfigures it to follow the promoted node as a replica.

### AOF and named volumes

Redis primarily works in memory, so a process cannot rely on RAM after it has been replaced. **AOF** means **Append Only File**. Redis records state-changing commands in an append-only log on disk. When Redis starts again, it replays that log to reconstruct its in-memory data.

This project uses `appendfsync everysec`. Redis asks the operating system to flush the AOF to disk about once per second. This is faster than forcing a disk sync after every command, but a severe crash can lose approximately the latest second of changes.

A Docker **named volume** stores the AOF outside the lifetime of a Redis container. Removing and recreating a container therefore does not remove its persisted file. Deleting the named volume does remove that stored state.

Replication and AOF solve different failures:

- Replication and Sentinel keep service available when one Redis node fails.
- AOF and named volumes recover data when Redis processes are replaced.

## Topology

```text
API instances
      |
three Sentinels (quorum 2)
      |
Redis primary ----asynchronous replication----> Redis replica
      |                                         |
primary AOF volume                        replica AOF volume
```

Sentinels monitor the logical master name `rate-limiter-primary`. The name lets clients ask for the current writable node without permanently depending on one container address. The data nodes store and replicate the existing Redis hashes; the token-bucket Lua script is unchanged.

The Compose service names describe startup roles only. After failover, `redis-replica` becomes the writable primary and `redis-primary` returns as its replica.

## Primary-loss experiment

The experiment ran on 2026-09-28 through the two-instance gateway at `localhost:8084`:

1. Confirm `redis-primary` reports `master` and `redis-replica` reports `slave`.
2. Send rate-limit traffic through the distributed gateway.
3. Stop the primary with `docker compose kill redis-primary`.
4. Observe the Sentinel logs and continue requesting decisions.
5. Restart the old primary and inspect both replication roles.

Sentinel declared the primary subjectively down after its configured five-second interval. The elected Sentinel logged replica promotion approximately 6.3 seconds after primary loss. The first successful HTTP decision arrived 7.966 seconds after the primary was killed.

The sequential recovery probe observed one HTTP 500 followed by HTTP 200. Neither API restarted: both restart counts remained zero. A request then spent the complete 100-token test capacity through one API upstream; four following requests alternated across both API addresses and all returned HTTP 429. This confirmed that the replicas continued enforcing one promoted Redis bucket rather than creating per-instance capacity. After `redis-primary` restarted, Sentinel configured it as a replica; it reached the `online` replication state while `redis-replica` remained master.

This phase deliberately retains the existing HTTP 500 dependency behavior. Short decision deadlines, circuit breaking, HTTP 503 mapping, and readiness are the next phase.

## Persistence experiment

The persistence experiment began with fresh V10 volumes:

1. Approve a weighted cost of 80 against the 100-token `client-a/openai` policy.
2. Inspect the primary hash and its TTL.
3. Stop and remove both Redis data containers and all three Sentinel containers.
4. Recreate those containers without deleting the two named data volumes.
5. Inspect the hash again before making another decision.

The stored state before replacement was:

```text
tokens          20
last_refill_ms  1790558886145
TTL             97940 ms
```

After every Redis and Sentinel process was replaced, the state was:

```text
tokens          20
last_refill_ms  1790558886145
TTL             80540 ms
```

The identical hash proves that AOF restored the bucket rather than creating a new full bucket. The lower, still-positive TTL reflects elapsed wall time. A later decision refills from `last_refill_ms` according to normal token-bucket behavior, so its displayed `remaining` value is not an appropriate persistence assertion.

## Guarantees and limits

- A single Redis data-node failure can be recovered through automatic replica promotion.
- API processes discover the promoted primary through Sentinel and do not need to restart.
- Named volumes and AOF preserve non-expired bucket state across complete Redis process replacement.
- `appendfsync everysec` can lose approximately the latest second of writes during a severe crash.
- Replication is asynchronous, so the newest primary write might not reach the replica before promotion.
- Bucket TTL remains authoritative: a bucket that expires during downtime should not be restored.
- Every service runs on one computer. The experiment does not prove protection from host, Docker-engine, or availability-zone failure.

Production high availability requires data nodes and Sentinel voters in independent failure domains. This local topology demonstrates the mechanism without claiming that physical isolation.
