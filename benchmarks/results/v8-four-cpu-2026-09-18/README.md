# V8 four-CPU comparison — 2026-09-18

Each `.txt` file is Vegeta's original text report. Its paired `.json` file retains exact request counts, status counts, latencies in nanoseconds, achieved rate, timestamps, duration and distinct errors. Failures are retained.

All attacks run sequentially for 30 seconds with a five-second timeout, 1,000 idle/max connections, 2,000 maximum workers, the existing client-b/stripe cost-one body, and the real Redis limiter. Route order rotates between stages: direct/single/distributed, single/distributed/direct, distributed/direct/single.

The direct process and both containers run identical API binaries (SHA-256 `e47578b1c1bc2f34c601b427c7c2d51e44b36989f495f52fb2fd0ab5acd4f61a`, Go 1.26.8). The direct process uses host port 6381 for the same Redis instance and DB 0 used by the containers at redis:6379. Both containers have Docker NanoCpus=4000000000. The direct process has no container quota. The two Nginx configs are unchanged apart from gateway restarts to resolve recreated API addresses.

Host: Intel Core i5-8365U, eight logical processors, approximately 15.8 GB physical RAM, Windows 11 with WSL2 kernel 6.6.87.2-microsoft-standard-WSL2. Vegeta and all server components run on this machine. The generator was built with Go 1.26.6; per-request API logging is disabled by the user's existing edit. No unrelated host workloads were stopped. No Redis bucket reset was performed between attacks.

Vegeta writes per-request binary output to the ignored `bin/load-test-results.bin` before generating these reports; that temporary file is reused between attacks. Output I/O and the colocated generator consume host resources and can affect achieved rate. These reports cannot isolate API capacity from load-generator, host, proxy, or Redis constraints.

Pass criteria: achieved RPS >= 99% of target, p95 < 5 ms, p99 < 10 ms, zero status-code-zero results, and no connection/timeout errors. Every HTTP status counts as a returned response; 500s are additionally reported as application failures. A passing stage requires two additional passing repetitions before being called a verified boundary.

## Summary

| Target RPS | Direct actual RPS | One instance actual RPS | Two instances actual RPS | Direct p95/p99 | One instance p95/p99 | Two instances p95/p99 |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1,000 | 1,000.04 | 1,000.05 | 1,000.02 | 5.28/18.51 ms | 4.46/17.31 ms | 4.65/11.64 ms |
| 2,000 | 680.52 | 1,275.22 | 1,709.24 | 102.14/319.16 ms | 7.56/14.45 ms | 5.66/41.24 ms |
| 4,000 | 312.17 | 392.73 | 579.53 | 87.37/117.30 ms | 306.13/503.31 ms | 321.94/1,299.60 ms |
| 6,000 | 677.34 | 977.48 | 661.29 | 214.06/288.06 ms | 70.67/197.20 ms | 237.09/453.36 ms |
| 8,000 | 569.49 | 220.80 | 177.92 | 283.93/463.70 ms | 241.63/506.11 ms | 333.15/422.01 ms |
| 10,000 | 1,335.87 | 1,501.31 | 202.37 | 22.19/68.68 ms | 26.67/213.06 ms | 734.77/840.94 ms |

All routes returned a response for every request at 1,000 RPS, but all failed the p99 threshold. There was therefore no passing boundary to repeat. At higher offered rates, achieved rates were non-monotonic and some proxied runs recorded missing responses. The reports preserve those failures; they do not support a production capacity or linear-scaling claim.

The full table, including missing responses, HTTP 500s and distinct transport-error kinds, is in [`docs/load-testing.md`](../../../docs/load-testing.md).
