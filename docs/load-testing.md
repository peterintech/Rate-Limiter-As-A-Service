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

```bash
vegeta attack \
  -rate=1000/s \
  -duration=30s \
  -timeout=5s \
  -dns-ttl=-1 \
  -connections=1000 \
  -max-connections=1000 \
  -max-workers=2000 \
  -targets=benchmarks/load-test-target.txt \
  -body=benchmarks/load-test-body.json \
  -header='Content-Type: application/json' |
vegeta report
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

Once a workload exceeds the measured single-instance envelope, running multiple API instances provides more serving capacity. Replication then exposes the next failure: every instance owns a separate in-memory token bucket and grants another full copy of the quota. The multi-instance test demonstrates why horizontal scaling requires shared, atomic rate-limit state.
