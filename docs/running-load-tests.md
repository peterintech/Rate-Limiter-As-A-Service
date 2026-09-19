# Running the V8 traffic comparison

Run commands from the repository root in a terminal that can reach the API addresses. Keep the API terminal open during testing. Install Vegeta if needed:

```text
go install github.com/tsenart/vegeta/v12@v12.13.0
```

Ensure your Go binary directory is on PATH so `vegeta` is available.

## Start and check the services

```text
docker compose up -d --build
docker compose restart single-gateway distributed-gateway
docker compose ps
docker inspect rate-limiter-api-1-1 rate-limiter-api-2-1 --format "{{.Name}}:{{.HostConfig.NanoCpus}}"
```

Each API should report `4000000000`: four logical CPUs' worth of execution time. This is a maximum CPU-time allowance, not dedicated cores. The two replicas can consume eight CPUs in total, but Redis, Nginx and the load generator still compete for the host's resources. Container names above assume the default `rate-limiter` Compose project name; use the names from `docker compose ps` if yours differs.

For the direct API, configure your local `.env` with these values (preserve any unrelated settings):

```dotenv
PORT=8080
ENV=test
REDIS_ADDR=localhost:6381
REDIS_PASSWORD=
REDIS_DB=0
```

Stop an existing Air or API process using port 8080 from its terminal before starting another. In a separate terminal run:

```text
go run ./cmd/api
```

Process environment variables take precedence over `.env`; ensure an existing `REDIS_ADDR` or `PORT` does not override these values. The direct API has no Docker CPU quota. Both containers use `redis:6379`, which is the same Redis server exposed to the host as `localhost:6381`.

Check all three endpoints. Each must return HTTP 200 and the health JSON before you test:

```text
curl -i http://localhost:8080/v1/health
curl -i http://localhost:8083/v1/health
curl -i http://localhost:8084/v1/health
```

Repeat the last command several times. `X-Upstream-Addr` should show both API addresses. Port 8083 should show only one. Port 8082 is Redis Commander and is not an API load-test target.

## Select a route and save a run

| Target file | Port | Request path | API CPU allowance |
| --- | --- | --- | --- |
| `benchmarks/load-test-target.txt` | 8080 | Direct Go process | No container quota |
| `benchmarks/load-test-single-target.txt` | 8083 | Nginx to api-1 | 4 CPUs |
| `benchmarks/load-test-distributed-target.txt` | 8084 | Nginx to api-1 and api-2 | 4 CPUs each, 8 combined |

The following example tests the single-container route at 1,000 RPS for 30 seconds. Keep the JSON body file; the real limiter stays enabled.

```text
vegeta attack -name=single-1000-trial1 -rate=1000/s -duration=30s -timeout=5s -dns-ttl=-1 -connections=1000 -max-connections=1000 -max-workers=2000 -targets=benchmarks/load-test-single-target.txt -body=benchmarks/load-test-body.json -header="Content-Type: application/json" -output=single-1000-trial1.bin
vegeta report single-1000-trial1.bin
vegeta report -output=single-1000-trial1.txt single-1000-trial1.bin
vegeta report -type=json -output=single-1000-trial1.json single-1000-trial1.bin
```

Change the target file, attack name, and output filenames for the other routes. Use a new filename for every run so failures and earlier trials remain available. The binary file contains individual request results; text and JSON files contain aggregate reports. These commands use Vegeta's output flags, avoiding shell-specific binary redirection.

## Run a comparison

Run only one attack at a time. Test 1,000, 2,000, 4,000, 6,000, 8,000 and 10,000 RPS by changing `-rate`. Keep every other attack setting fixed. Rotate route order between stages:

1. Direct, single, distributed.
2. Single, distributed, direct.
3. Distributed, direct, single.

Repeat this order cycle for the remaining stages. Repeat each route's highest passing rate twice more, giving three trials at that candidate boundary. A candidate is verified only if all three trials pass. If repeats fail, report the instability rather than labeling the initial result reliable.

## Read the report

- `Requests total`: requests actually issued, not merely the requested rate multiplied by duration.
- `Requests rate`: achieved sending rate. Falling below 99% of target fails the rate criterion.
- `95` and `99`: p95 and p99 request latencies. The criteria require p95 below 5 ms and p99 below 10 ms.
- `Status Codes 0:N`: N requests received no HTTP status. If code 0 is absent, this count is zero.
- `200` and `429`: completed responses. A 429 is an expected quota rejection.
- `500`: also a completed response for this traffic metric, but an application error that must be reported separately. It is not a successful limiter decision.
- `Error Set`: distinct errors, not counts of each error. EOF, connection errors, and timeouts require inspection; `429 Too Many Requests` alone is not a missing response.

Ignore the report's `Success` percentage and successful-response `throughput` when measuring completed HTTP traffic: both exclude valid 429 responses. Passing the traffic gate requires every issued request to receive a response, no connection/timeout failures, and the rate and latency criteria above.

The same client/resource bucket is shared across runs and refills continuously, so 200/429 proportions can change with run order and idle time. Do not interpret those proportions as serving capacity. This experiment compares local deployment configurations; CPU quotas and network paths differ, and all services share one machine.
