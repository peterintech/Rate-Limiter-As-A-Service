# Design journey

This project is meant to be read as a system-design story, not only as a finished codebase. Each phase begins with the smallest design that can teach us something. A test, benchmark, or failure experiment then shows where that design stops being sufficient. Only after that evidence do we add another algorithm or infrastructure component.

That order matters. If Redis, replication, circuit breaking, queues, and PostgreSQL appeared in the first commit, the repository would show a collection of technologies but not the reasoning that justified them.

Each phase below answers five questions:

1. What were we trying to learn or protect?
2. What evidence exposed the current limitation?
3. What reasonable options did we have?
4. Why did we choose this option?
5. What tradeoff or unanswered question did we carry forward?

## V0: define the decision contract

### Problem

Before choosing an algorithm, we needed to define what a rate-limit decision means. The limiter must identify a client and resource, support weighted cost, reject unknown policies, and explain allowed and denied decisions consistently.

### Options considered

- Start with infrastructure and allow the storage model to shape the API.
- Copy the response contract of a third-party gateway.
- Define a storage-independent domain contract first.

### Decision

We defined `Limiter`, `Key`, `Policy`, and `Decision` inside `internal/ratelimiter`, then placed HTTP translation in `cmd/api`.

### Why

The contract describes the business decision without mentioning maps, Redis, HTTP, or a particular algorithm. Later phases can replace implementations without rewriting handlers or callers.

### Tradeoff carried forward

The policies are static and compiled into the application. That is acceptable while the project is learning enforcement behavior. Dynamic policy management remains a separate problem.

## V1: use the simplest working limiter

### Problem

We needed a complete vertical slice that could accept an HTTP request and enforce a policy inside one process.

### Options considered

- Fixed window, one counter for each aligned time interval.
- Sliding log, one record for every approved request in the active interval.
- Token bucket, a continuously refilled balance.

### Decision

We chose an in-memory fixed window.

### Why

Fixed window is the easiest algorithm to read and verify. It gave us a small baseline before concurrency, boundary behavior, memory growth, or distribution were introduced. Choosing the final algorithm immediately would have hidden the reasons for choosing it.

### Tradeoff carried forward

The implementation was only correct for sequential use in one process. That limitation was intentional, but it still had to be demonstrated before synchronization was added.

## V2: make one process safe under concurrency

### Evidence

A concurrent HTTP test exposed `fatal error: concurrent map writes`. The map was not only a performance detail. It could crash the process and allow multiple goroutines to race through one capacity check.

### Options considered

- Use `sync.Map` to make individual map operations safe.
- Use atomic values for selected fields.
- Protect the complete read, reset, capacity check, and spend operation with a mutex.

### Decision

We used a mutex around the complete state transition.

### Why

The rate-limit decision is one logical transaction. Making individual map reads and writes safe would not make the combined check-and-spend sequence atomic. A normal map and mutex also keep the algorithm easier to read than a collection of unrelated atomic operations.

### Tradeoff carried forward

The mutex makes one process safe, but different processes still own different maps and different locks. It does not provide cluster-wide correctness.

## V3: measure the fixed-window boundary burst

### Evidence

A deterministic test spent the full quota immediately before an aligned minute boundary and spent it again immediately after the boundary. A policy of three requests per minute approved six requests within 200 milliseconds.

### Options considered

- Accept the burst and document that the policy means aligned calendar windows.
- Smooth the approximation with a sliding-window counter.
- Track exact recent approvals with a sliding log.
- Move directly to a token bucket.

### Decision

This phase changed no production code. It recorded the failure first.

### Why

Changing the algorithm in the same step that introduced the test would hide the original behavior. The failing model is part of the design evidence.

### Tradeoff carried forward

The project now had proof that fixed window was too bursty, but it had not yet measured the state cost of a more precise rolling algorithm.

## V4: use a sliding log for exact rolling-window behavior

### Problem

We wanted requests near a clock boundary to remain inside one rolling interval.

### Options considered

- Sliding-window counter, which combines neighboring fixed-window counts to estimate usage.
- Sliding log, which retains the timestamp and cost of every active approval.
- Token bucket, which controls a refill rate but does not reproduce an exact rolling-window count.

### Decision

We implemented a sliding log.

### Why

The sliding log is the clearest exact answer to the boundary problem. It allowed the repository to demonstrate correct rolling-window behavior before optimizing its storage cost.

### Tradeoff carried forward

Exactness requires retaining one entry per approved request until that entry expires. We needed evidence of how that state grows before replacing it.

## V5: benchmark sliding-log state growth

### Evidence

The benchmark froze time so no entry could expire, then compared 100, 1,000, and 10,000 approvals. Fixed-window state remained constant while sliding-log allocation grew with the number of active approvals.

### Options considered

- Keep the exact log and accept memory proportional to traffic.
- Compress requests into time buckets, accepting approximation.
- Move to a bounded-state token bucket.

### Decision

We retained the benchmark as evidence and selected token bucket for the next phase.

### Why

The project needs high traffic and many client/resource combinations. Retaining every approval on the synchronous decision path would make state depend on request volume rather than only on the number of active policies.

### Tradeoff carried forward

A token bucket provides bounded state and continuous refill, but it intentionally permits bursts up to its current token balance. It does not promise exact sliding-window semantics.

## V6: use a bounded-state token bucket

### Options considered

- Keep the sliding log for exactness.
- Use `golang.org/x/time/rate`, an established in-process token-bucket implementation.
- Implement the small project-specific token bucket behind the existing limiter contract.

### Decision

We implemented a local token bucket with one token balance and refill timestamp per client/resource pair.

### Why

The implementation makes weighted costs, refill mathematics, remaining capacity, and reset estimates visible to someone learning the algorithm. It also fits the existing policy and decision contract directly. An established library would be sensible when its API and behavior match production requirements, but using one here would hide the algorithm this phase is intended to teach.

### Tradeoff carried forward

State is bounded, but it still lives in one process. Replicating the API creates independent buckets and therefore independent copies of the quota.

## V7: test the reasons for distribution

### Problem

“Distributed systems scale better” is not sufficient justification. We tested three separate properties: capacity, availability, and global correctness.

### Evidence

- A corrected Vegeta experiment established a repeatable local capacity floor, but two API containers did not increase throughput on the same eight-core computer. The shared host was the binding resource.
- Killing one of two API instances did not stop the distributed route, while the single-instance route produced gateway failures. Distribution improved application availability.
- Two independent in-memory buckets approved exactly twice the intended quota. Distribution broke global correctness.

### Options considered

- Scale vertically by giving one process more CPU.
- Scale horizontally for availability and future independent hosts.
- Avoid distribution because the local throughput test did not improve.

### Decision

We kept the replicated API topology because it demonstrated availability benefits and exposed the shared-state requirement. We did not claim that two containers on one laptop increased capacity.

### Why

Capacity, availability, and correctness are different claims and require different experiments. Horizontal deployment is justified here by failure isolation and the eventual need for independent machines, not by a misleading local throughput number.

### Tradeoff carried forward

Once API instances are independent, one in-memory bucket cannot enforce a global quota. The next phase needs shared state and an atomic distributed decision.

## V8: move authoritative state to Redis

### Options considered

- Coordinate API instances directly with distributed locks or peer communication.
- Store every decision in PostgreSQL and use database transactions.
- Use Redis for low-latency shared state.

For the Redis update itself, we also considered:

- Separate read and write commands.
- Optimistic transactions with `WATCH` and `MULTI`.
- One Lua script executed atomically by Redis.

### Decision

We chose Redis and one Lua script for refill, capacity checking, spending, and expiry.

### Why

Redis matches the low-latency, short-lived state in the decision path. PostgreSQL is valuable for durable history later, but making every approval wait for a durable row would couple enforcement latency to analytics storage. One Lua script avoids the race where two instances read the same balance and both approve it. Redis server time also gives every instance one refill clock.

### Tradeoff carried forward

Redis fixed cluster-wide accuracy but became a required dependency. If Redis disappeared, every authoritative decision failed.

## V9: demonstrate the Redis outage before hiding it

### Evidence

Stopping Redis caused rate-limit decisions to return HTTP 500 after 2.360 to 4.435 seconds. The API processes remained alive and reconnected after Redis returned.

### Options considered

- Fail open and approve requests without authoritative state.
- Give each API an emergency local reserve.
- Fail closed and reject decisions while shared state is unavailable.
- Improve Redis availability so total dependency loss is less likely.

### Decision

V9 did not add a fallback. It documented the outage and deferred the policy choice until its correctness consequences were understood.

### Why

Fail open protects caller availability but can violate the external provider’s quota. Independent emergency reserves reintroduce quota multiplication and require a reconciliation model. Since this service exists specifically to protect a strict shared quota, silently approving without authority is dangerous.

### Tradeoff carried forward

Failing closed preserves quota safety but reduces decision availability. We first reduced the chance of complete Redis loss in V10, then bounded the remaining failure in V11.

## V10: add Redis failover and durable state

### Availability options considered

- One Redis process with application retries.
- Redis primary and replica with Sentinel failover.
- Redis Cluster.
- A managed Redis service with provider-controlled failover.

### Decision

For the local qualification environment, we chose one primary, one replica, and three Sentinels with quorum two.

### Why

The project needs one logical writable dataset, not sharding, so Redis Cluster would introduce routing and operational complexity without solving a measured capacity problem. Sentinel demonstrates primary discovery and automatic promotion locally. A managed service would be a strong production choice, but it would hide the failover mechanics this repository is meant to teach and would make local reproduction dependent on a cloud account.

Three Sentinels avoid allowing one monitor to promote a node by itself. Quorum two means a majority must agree that the primary is unavailable.

### Durability options considered

- No persistence, accepting complete state loss after process replacement.
- RDB snapshots, which periodically capture the dataset.
- AOF with `appendfsync always`, syncing every change.
- AOF with `appendfsync everysec`, syncing approximately once per second.

### Decision

We enabled AOF with `appendfsync everysec` on both data nodes and gave each one a named volume.

### Why

RDB can lose all writes since the latest snapshot. `appendfsync always` reduces the loss window but puts a disk synchronization cost on every rate-limit mutation. `everysec` is the chosen performance and durability balance for ephemeral quota state.

### Evidence and tradeoffs

Sentinel promoted the replica and clients recovered without API restarts. AOF restored a non-expired bucket after every Redis process was replaced. Replication remains asynchronous and `everysec` can lose approximately the latest second of writes. All containers still share one computer, so this proves software mechanics rather than host-level high availability.

## V11: bound complete Redis failure

### Problem

Sentinel can recover one failed data node, but it cannot help while both data nodes are unavailable or while failover is still in progress. Slow failures can consume API connections and goroutines even when the API process itself is healthy.

### Options considered

- Continue relying on Redis client retries.
- Fail open during dependency failure.
- Allocate local emergency quota.
- Use short deadlines, a circuit breaker, and fail-closed HTTP 503 responses.

### Decision

We chose explicit Redis timeouts, a 300 millisecond complete-decision deadline, a per-process circuit breaker, and fail-closed HTTP 503 responses. We also separated liveness from readiness.

### Why

Retries can amplify an outage and make latency unpredictable. Failing open can exceed the protected quota. Local emergency capacity would require reserving and reconciling quota across instances, adding significant correctness complexity before the brief demonstrates a need for it.

The breaker is local because it protects each API process from spending its own resources on a known failing dependency. Redis remains global because it protects the shared quota.

The project uses a small implementation behind the existing `Limiter` interface so the three states and recovery behavior remain visible. If breaker requirements grow to include richer telemetry, rolling failure ratios, distributed configuration, or more complex concurrency rules, replacing it with a mature maintained library would reduce maintenance risk.

### Evidence

With both Redis data nodes stopped, each of the two APIs made three bounded attempts. Those first six responses completed in approximately 304 milliseconds. Once both breakers opened, later responses completed in approximately 3 milliseconds without contacting Redis. Liveness remained HTTP 200, readiness became HTTP 503, and normal decisions returned after Redis recovered.

### Tradeoff carried forward

The system protects quota correctness by refusing decisions during complete Redis loss. It does not provide uninterrupted approvals. The next major requirement is durable approved-request history without placing durable database writes on this synchronous decision path.

## V12: capture every reported approval

### Problem

The token bucket stores only current enforcement state. It cannot reconstruct individual approvals for billing or analytics, and its key disappears after inactivity.

### Options considered

- Insert directly into PostgreSQL and make durable storage part of every decision.
- Buffer events in a Go channel and accept process-loss risk.
- Publish to a separate broker after Redis and accept a dual-write gap.
- Append to a Redis Stream inside the existing Lua decision.

### Decision

We append one Redis Stream entry for every allowed decision inside the token-bucket Lua script. Rejected requests append nothing, and the stream remains untrimmed until a durable consumer exists.

### Why

The stream reuses the dependency already on the decision path and requires no second network round trip. A reported HTTP 200 is not returned until `XADD` completes. Direct PostgreSQL writes would couple enforcement to analytics storage, an in-process channel could lose billing events, and a separate broker would introduce a Redis-to-broker dual write before the project has a mechanism to reconcile it.

### Evidence

Two API instances wrote weighted approvals into one stream while a rejected request added no entry. The stream continued after Sentinel promotion and AOF restored all observed entries after Redis process replacement. Three controlled 100 RPS comparisons found no measurable low-load regression. Higher-rate tail latency was noisy on the shared laptop and was not presented as an isolated `XADD` cost.

### Tradeoff carried forward

The untrimmed stream grows with every approval and retains Redis's asynchronous-replication and AOF durability limits. V13 now has a concrete reason to add a background consumer and permanent PostgreSQL storage.

## V13: move approval history out of Redis

### Problem

V12 retained every approval indefinitely. At 1,000 approvals per second, that is 86.4 million new stream entries per day. Redis is appropriate for active rate-limit state and short-lived delivery, but not for unbounded billing history.

### Options considered

- Write PostgreSQL synchronously before returning HTTP 200.
- Trim Redis by a maximum length or age.
- Use a separate worker, persist idempotently, and delete only committed event IDs.

### Decision

We added a separate approval worker, PostgreSQL, Goose migrations, and sqlc-generated queries. The worker reads through one Redis consumer group, commits each batch to PostgreSQL, and only then acknowledges and deletes those exact Redis entries.

### Why

Synchronous database writes would make PostgreSQL latency and availability part of every decision. Blind trimming could delete the only copy of an event during a database outage. The worker keeps durable storage off the HTTP path while the PostgreSQL primary key on `stream_id` makes redelivery idempotent.

The worker is a separate process because it has a different scaling and failure lifecycle from the API. Goose keeps schema changes explicit and reversible; sqlc preserves visible SQL while generating compile-time-checked Go calls. An ORM and a generic event framework would add abstraction without solving another demonstrated problem.

### Evidence

With the worker stopped, three HTTP 200 approvals accumulated as three Redis entries and PostgreSQL did not change. Restarting the worker added exactly three rows and drained the stream. With PostgreSQL stopped, two approvals still returned HTTP 200 and remained in Redis. After PostgreSQL returned, both were persisted; the entry already pending was recovered through `XAUTOCLAIM` after the configured 30-second idle period. Redis then reported zero stream entries and zero pending entries.

### Tradeoff carried forward

Normal processing now bounds stream growth, but a prolonged PostgreSQL or worker outage still creates an unbounded backlog. V14 must add lag and memory observability and choose an explicit backpressure policy. It must not disguise the problem with unsafe blind trimming.

## How to evaluate future phases

Future phases should keep the same evidence trail:

```text
current design
      ↓
measured failure or missing requirement
      ↓
reasonable alternatives
      ↓
chosen design and explicit tradeoff
      ↓
test or experiment that proves the claim
```

This discipline prevents the repository from becoming a technology checklist. Every component should have a reason to exist, a limitation it solves, and a cost the design knowingly accepts.
