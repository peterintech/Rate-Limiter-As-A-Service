# Why we tested distribution

The goal of this project is a highly available rate limiter that enforces one quota across every API instance. Before adding a distributed component, we needed evidence that a single API process had reached a real limitation.

We tested three different questions. They sound similar, but they are not the same thing.

| Question | What it means |
| --- | --- |
| Capacity | Can the system handle more HTTP traffic? |
| Availability | Can requests still be served when one API process stops? |
| Global correctness | Does every API instance enforce one shared quota? |

The answers led to one clear decision: use multiple API instances for availability, then introduce shared atomic state so those instances still enforce one global quota.

## The setup

The test topology uses one Nginx entry point and two routes:

| Route | Request path | Purpose |
| --- | --- | --- |
| `8083` | Vegeta → Nginx → `api-1` | Single API instance control |
| `8084` | Vegeta → Nginx → `api-1` or `api-2` | Two API instance comparison |

Each API process uses the same in-memory token bucket. There is intentionally no Redis or other shared state in this phase.

Vegeta sends a fixed request rate to the real `POST /v1/check` endpoint. A `200`, `429`, or other HTTP status means the server returned an HTTP response. Status `0`, connection errors, and timeouts mean no HTTP response arrived.

Vegeta stores latency in nanoseconds in its JSON output. Divide by `1,000,000` to display milliseconds.

## What the tests showed

### Capacity was inconclusive

The single route sustained 7,000 requests per second under the selected local criteria. The distributed route sustained 6,000 requests per second.

That does **not** prove that one API instance has more capacity than two. The load generator, Nginx, the API containers, Redis, and Redis Commander were all competing for the same eight logical CPU host. The API containers stayed below their configured CPU limits, so the test found a limit in the local harness before it found a limit in the API.

The honest result is: this experiment cannot justify distribution for capacity. A real capacity decision needs an independent load generator, isolated resources, and no unrelated workloads.

### Availability was proven

We held traffic at 100 requests per second for 150 seconds, then stopped each API instance for 30 seconds. This rate stays below the configured quota refill rate, so a failure cannot be confused with a valid `429` rate-limit decision.

| Route | Requests | Successful responses | Client-visible errors |
| --- | ---: | ---: | ---: |
| One instance | 15,000 | 14,150 | 850 (`502` or `504`) |
| Two instances | 15,000 | 15,000 | 0 |

When one API process stopped, Nginx retried the other process. Six requests were slower because the first upstream attempt timed out, but every client still received `200`.

This is the reason we keep the distributed topology: one API process can fail without stopping rate-limit decisions.

### Global correctness was broken

We sent 600 requests per second against a policy with a 5,000-request-per-minute allowance. Both routes began with fresh token buckets.

| Route | Duration | Approved requests | Rejected requests |
| --- | ---: | ---: | ---: |
| One instance | 40 seconds | 8,332 | 15,668 |
| Two instances | 40 seconds | 16,664 | 7,336 |

Two instances approved exactly twice as many requests because each process owned its own token bucket. The deterministic application test, `TestIndependentInstancesMultiplyQuota`, shows the same failure with a fixed clock.

This is why the next phase introduces shared atomic state. Multiple API instances are useful only if they make one decision against one shared quota.

## The decision

We distribute for availability, not for an unproven capacity claim.

The next phase adds Redis so every API instance reads and updates the same token bucket atomically. Redis outage behaviour and degraded mode stay deferred until shared enforcement works and its own failure can be demonstrated.

## Repeating the experiment

Start the local measurement topology:

```bash
docker compose up -d
```

Run a 30-second distributed-route check at 2,000 requests per second:

```bash
docker compose --profile loadtest run --rm vegeta sh -c "vegeta attack -rate=2000/s -duration=30s -timeout=5s -max-workers=16 -connections=256 -targets=/in/load-test-target-distributed.txt -body=/in/load-test-body.json -header='Content-Type: application/json' | vegeta report"
```

Replace `load-test-target-distributed.txt` with `load-test-target-single.txt` to use the single-instance route. Treat the result as a local learning experiment, not a production capacity number.
