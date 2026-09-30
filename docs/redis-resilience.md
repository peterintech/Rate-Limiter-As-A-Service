# Bounded Redis failure and readiness

V9 proved that a complete Redis outage could hold each rate-limit request for several seconds. V10 made a single Redis-node failure recoverable, but replication cannot help when the complete Redis deployment is unreachable. V11 controls how the API behaves in that situation.

## Terminology

A **timeout** is the longest time the application will wait for an operation. Redis has separate limits for connecting, writing a command, and reading its response. The decision timeout surrounds the complete rate-limit operation, so it remains the final upper bound.

A **circuit breaker** prevents a failing dependency from receiving the same request repeatedly. The circuit begins closed, which means requests may reach Redis. After three consecutive dependency failures it opens for five seconds. While open, decisions return immediately without contacting Redis. After five seconds, one request is allowed through as a recovery probe. A successful probe closes the circuit; a failed probe opens it again.

The circuit breaker does not provide fallback capacity. When Redis cannot make the authoritative decision, the API fails closed with HTTP 503. A valid HTTP 429 quota rejection is not a dependency failure and does not open the circuit.

**Liveness** answers whether the API process is running. **Readiness** answers whether that process can currently serve rate-limit decisions. `/v1/health` can therefore return HTTP 200 while `/v1/readiness` returns HTTP 503.

## Request flow

```text
POST /v1/check
      |
is the circuit open? -- yes --> 503 without calling Redis
      |
      no
      |
run the Redis decision within 300 ms
      |
success or 429 --> reset consecutive failures
      |
dependency failure --> record failure and return 503
```

Each API process owns its own breaker because the breaker protects that process's resources. Redis still owns the shared token state. With two API instances and a threshold of three, a complete outage can therefore produce up to six bounded Redis attempts before both local breakers are open.

## Measured complete-outage experiment

The experiment ran on 2026-09-29 through the two-instance gateway at `localhost:8084`:

1. Confirm health, readiness, and a normal decision returned HTTP 200.
2. Stop both Redis data nodes while leaving both APIs, all Sentinels, and Nginx running.
3. Send ten decisions through the distributed gateway.
4. Check liveness and readiness during the outage.
5. Restart both Redis nodes and check recovery without restarting the APIs.

The first six requests returned HTTP 503 in 303 to 305 milliseconds. This matches three failed attempts in each API process and the configured 300 millisecond decision deadline. The following four requests returned HTTP 503 in 2.6 to 3.3 milliseconds because both circuits were open and Redis was not called.

Every failure used the bounded public response:

```json
{"error":"rate-limit service temporarily unavailable"}
```

During the complete outage:

- `/v1/health` returned HTTP 200.
- `/v1/readiness` returned HTTP 503.
- No request received fallback capacity.

After both Redis nodes restarted, readiness returned HTTP 200 and the next rate-limit decision returned HTTP 200 in 18.5 milliseconds. Neither API needed to restart.

## What this protects

The decision deadline prevents slow Redis work from holding an API request indefinitely. The circuit breaker reduces repeated work after failure is established. HTTP 503 distinguishes dependency unavailability from an unexpected application defect. Readiness gives a deployment platform a signal for whether the instance should receive decision traffic.

The breaker does not repair Redis, preserve state, or coordinate between API instances. Sentinel and replication provide single-node recovery, AOF provides process-replacement durability, and the breaker protects API resources while those mechanisms recover or while the complete deployment remains unavailable.
