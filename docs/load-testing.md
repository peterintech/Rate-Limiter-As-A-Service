# Single-instance traffic capacity

Horizontal scaling should answer a measured capacity problem. This experiment sends fixed request rates through the real `POST /v1/check` endpoint to establish a local traffic envelope for one API instance.

The test measures completed HTTP traffic, not rate-limit outcomes. A `200`, `429`, or any other HTTP response means the server received the request and returned a response. Requests with status code `0`, connection errors, and timeouts did not receive an HTTP response.

## Tool

[Vegeta](https://github.com/tsenart/vegeta) is a Go-based command-line load generator designed to send traffic at a constant request rate. Install the pinned version with the Go toolchain on Windows, Linux, or macOS:

```text
go install github.com/tsenart/vegeta/v12@v12.13.0
```

Prebuilt binaries are also available from the Vegeta releases page. No Vegeta package is added to this application's `go.mod`.

## Test environment

| Component | Value |
| --- | --- |
| Date | 2026-09-16 |
| Operating system | Microsoft Windows 11 Pro 10.0.26200 |
| CPU | Intel Core i5-8365U at 1.60 GHz, 8 logical processors |
| Memory | 15.8 GB |
| Go | 1.26.4 windows/amd64 |
| Vegeta | 12.13.0 |

The API and load generator run on the same machine. They therefore compete for CPU, memory, and network resources. These results are a reproducible local comparison rather than a production capacity guarantee.

## Running the experiment

Start the API:

```text
go run ./cmd/api
```

For the measurement, set `ENV=test` in `.env` so per-request console logging does not become the bottleneck. This changes no routing, JSON handling, token-bucket behavior, or response handling.

The request target and body are stored in `benchmarks/load-test-target.txt` and `benchmarks/load-test-body.json`. Run one 30-second attack for each rate:

```text
vegeta attack -rate=1000/s -duration=30s -timeout=5s -dns-ttl=-1 -connections=1000 -max-connections=1000 -max-workers=2000 -targets=benchmarks/load-test-target.txt -body=benchmarks/load-test-body.json -header="Content-Type: application/json" -output=direct-1000-trial1.bin
vegeta report direct-1000-trial1.bin
```

Repeat the command with rates of `2000/s`, `4000/s`, `6000/s`, `8000/s`, and `10000/s`.

Run Vegeta in an environment that can reach the API address in the target file. Restart the server before each rate so every report starts from the same process and limiter state. Repeat boundary rates to distinguish a reproducible limit from a favorable individual run.

## Reading the report

For this experiment, use:

- `Requests total` for the number of requests Vegeta issued.
- `Requests rate` for the actual request rate Vegeta sustained.
- Latency `95th` and `99th` percentiles for response time.
- Status code `0` and the error set for requests that received no HTTP response.

Do not use Vegeta's `Success` ratio or successful-response `Throughput`. Vegeta treats non-2xx responses as unsuccessful, while this experiment deliberately counts all HTTP responses—including valid `429` decisions—as handled traffic.

The local traffic envelope is the highest offered rate where the actual request rate reaches at least 99 percent of the target, p95 stays below 5 milliseconds, p99 stays below 10 milliseconds, and every issued request receives an HTTP response without connection errors or timeouts.

## Results

The 1,000-RPS stage was run once. The 2,000-RPS stage and every higher boundary rate were run three times after the initial progression showed non-monotonic latency. Each trial used a fresh API process.

| Target RPS | Trial | Requests | Actual RPS | p95 | p99 | No HTTP response |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1,000 | 1 | 30,000 | 1,000.04 | 0.58 ms | 0.73 ms | 0 |
| 2,000 | 1 | 60,000 | 2,000.07 | 0.29 ms | 0.64 ms | 0 |
| 2,000 | 2 | 59,999 | 2,000.03 | 0.43 ms | 0.64 ms | 0 |
| 2,000 | 3 | 59,999 | 2,000.03 | 0.52 ms | 0.65 ms | 0 |
| 4,000 | 1 | 120,000 | 4,000.10 | 0.59 ms | 4.43 ms | 1 |
| 4,000 | 2 | 119,997 | 4,000.04 | 0.54 ms | 2.12 ms | 0 |
| 4,000 | 3 | 120,000 | 4,000.06 | 0.78 ms | 9.67 ms | 0 |
| 6,000 | 1 | 179,999 | 6,000.14 | 36.39 ms | 253.69 ms | 387 |
| 6,000 | 2 | 179,999 | 6,000.02 | 0.63 ms | 31.41 ms | 0 |
| 6,000 | 3 | 179,998 | 6,000.12 | 0.71 ms | 1.65 ms | 0 |
| 8,000 | 1 | 239,997 | 7,999.98 | 1.32 ms | 3.20 ms | 0 |
| 8,000 | 2 | 239,997 | 8,000.00 | 1.14 ms | 2.75 ms | 0 |
| 8,000 | 3 | 240,000 | 8,000.02 | 81.24 ms | 204.10 ms | 362 |
| 10,000 | 1 | 299,997 | 9,999.99 | 3.04 ms | 104.99 ms | 68 |
| 10,000 | 2 | 299,995 | 10,000.00 | 5.31 ms | 110.85 ms | 183 |
| 10,000 | 3 | 299,997 | 10,000.20 | 1.85 ms | 3.94 ms | 0 |

All three 2,000-RPS trials satisfy the request-rate, latency, and response criteria. At 4,000 RPS, one trial contains a request with no HTTP response, so that rate does not satisfy the strict zero-loss requirement across repeated runs. Higher rates show larger and non-monotonic latency and response-loss spikes.

The defensible result is therefore a **verified local capacity floor of 2,000 requests per second**, not an exact maximum. The server may process more in favorable runs, but this same-machine experiment cannot claim those rates as reliable under the selected criteria.

## Interpretation

Request rate, concurrent connections, in-flight requests, and users are different measurements. Vegeta controls requests per second and creates enough workers to sustain that rate. It does not claim that the request rate equals a number of users or a maximum connection count.

Production-equivalent testing requires the same build, resource limits, network topology, and dependencies as production, with Vegeta running from separate load-generator infrastructure. That separation also removes the load generator's competition with the server and is required before claiming a production capacity ceiling.

Once a workload exceeds the measured single-instance envelope, adding application instances and resources can provide more serving capacity. Replication then exposes the next failure: every instance owns a separate in-memory token bucket and grants another full copy of the quota. The multi-instance test demonstrates why horizontal scaling requires shared, atomic rate-limit state.

## Replicated topology comparison

Redis makes it safe for multiple API instances to enforce one shared quota. This experiment compares a direct host process, one container behind Nginx, and two containers behind Nginx to determine whether replication also improves completed HTTP traffic on the local test machine.

Each API container has a four-CPU execution-time ceiling. The single route can therefore consume up to four CPUs, while the distributed route can consume up to eight CPUs across two processes. These are ceilings, not reserved physical cores. The direct process has no container CPU ceiling.

The test does not assume that replication must be faster. The API containers, Redis, Nginx, and Vegeta still compete for the same physical host, so the result remains local evidence rather than a production scaling coefficient.

The three routes are:

```text
localhost:8080 -> direct API process -> Redis
localhost:8083 -> Nginx -> api-1 -> Redis
localhost:8084 -> Nginx -> api-1 or api-2 -> Redis
```

Start the complete stack:

```text
docker compose up -d --build
```

Use the existing JSON body with the route-specific target files. The copyable, single-line commands and complete procedure are in [Running the V8 traffic comparison](running-load-tests.md).

Apply the same acceptance criteria used by the original experiment: actual rate at least 99 percent of target, p95 below 5 milliseconds, p99 below 10 milliseconds, and no request without an HTTP response. Count every HTTP status as handled traffic. The useful comparison is the highest repeatable accepted rate for each topology, not Vegeta's success percentage.

### Results

The comparison was rerun on 2026-09-18 on the test machine documented above. The direct process and containers used the exact same API binary and Redis database. Each route was tested sequentially for 30 seconds at every offered rate. Route order rotated between stages. All raw text and JSON reports, including failed runs, are retained in [`benchmarks/results/v8-four-cpu-2026-09-18`](../benchmarks/results/v8-four-cpu-2026-09-18/README.md).

| Target RPS | Route | Requests | Actual RPS | p95 | p99 | No HTTP response | HTTP 500 | Transport error kinds | Pass |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 1,000 | direct | 30,000 | 1,000.04 | 5.28 ms | 18.51 ms | 0 | 0 | 0 | no |
| 1,000 | one instance | 30,000 | 1,000.05 | 4.46 ms | 17.31 ms | 0 | 0 | 0 | no |
| 1,000 | two instances | 30,000 | 1,000.02 | 4.65 ms | 11.64 ms | 0 | 0 | 0 | no |
| 2,000 | direct | 20,416 | 680.52 | 102.14 ms | 319.16 ms | 0 | 0 | 0 | no |
| 2,000 | one instance | 38,256 | 1,275.22 | 7.56 ms | 14.45 ms | 0 | 0 | 0 | no |
| 2,000 | two instances | 51,276 | 1,709.24 | 5.66 ms | 41.24 ms | 0 | 0 | 0 | no |
| 4,000 | direct | 9,371 | 312.17 | 87.37 ms | 117.30 ms | 0 | 0 | 0 | no |
| 4,000 | one instance | 11,782 | 392.73 | 306.13 ms | 503.31 ms | 681 | 0 | 2 | no |
| 4,000 | two instances | 17,388 | 579.53 | 321.94 ms | 1,299.60 ms | 0 | 0 | 0 | no |
| 6,000 | direct | 20,321 | 677.34 | 214.06 ms | 288.06 ms | 0 | 0 | 0 | no |
| 6,000 | one instance | 29,324 | 977.48 | 70.67 ms | 197.20 ms | 702 | 0 | 2 | no |
| 6,000 | two instances | 19,840 | 661.29 | 237.09 ms | 453.36 ms | 172 | 0 | 2 | no |
| 8,000 | direct | 17,085 | 569.49 | 283.93 ms | 463.70 ms | 0 | 0 | 0 | no |
| 8,000 | one instance | 6,652 | 220.80 | 241.63 ms | 506.11 ms | 704 | 0 | 2 | no |
| 8,000 | two instances | 5,338 | 177.92 | 333.15 ms | 422.01 ms | 320 | 0 | 2 | no |
| 10,000 | direct | 40,076 | 1,335.87 | 22.19 ms | 68.68 ms | 0 | 0 | 0 | no |
| 10,000 | one instance | 45,039 | 1,501.31 | 26.67 ms | 213.06 ms | 746 | 0 | 2 | no |
| 10,000 | two instances | 6,071 | 202.37 | 734.77 ms | 840.94 ms | 105 | 0 | 2 | no |

`Transport error kinds` counts distinct non-429 messages in Vegeta's error set; it is not a request count. `No HTTP response` is the request count represented by status code `0`. No run returned HTTP 500.

### Conclusion

No route passed the complete acceptance gate at 1,000 RPS: all three sustained the offered rate and returned a response for every request, but all exceeded the 10 ms p99 requirement. Therefore there was no passing boundary to repeat three times and this run establishes **no verified capacity floor under the selected thresholds**.

At 2,000 RPS the two-instance route issued more requests than the one-instance route, and both proxied routes issued more than the direct route. That isolated ordering is not enough to claim a scaling factor. Results became strongly non-monotonic at higher offered rates, and some proxied runs recorded status-zero responses and EOF or closed-idle-connection errors. Increasing an offered rate cannot make the same server intrinsically more capable, so the varying achieved rates show that this colocated setup is measuring combined load-generator, host scheduler, Nginx, Redis, WSL2, and API behavior—not a clean API-only ceiling.

The experiment does demonstrate an important reliability lesson: allocating two four-CPU containers raises the available application CPU ceiling, but it does not guarantee better end-to-end capacity when every component competes on one eight-logical-processor laptop. Redis remains justified because it preserves one atomic quota across replicas. A defensible production scaling claim requires equivalent deployment resources, independent application hosts, an independent load generator, and repeated trials at a candidate passing boundary.
