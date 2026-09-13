# Rate Limiter Benchmarks

## Sliding-log state growth

The sliding log stores the timestamp and cost of every approved request that remains inside the rolling interval. This benchmark compares that representation with the fixed window's single counter.

Both algorithms receive the same client/resource key, one-minute policy, fixed clock, unit cost, and number of approved requests. Because the clock does not advance, no sliding-log entry expires during a batch.

Run the experiment with:

```bash
go test -run '^$' -bench=BenchmarkLimiterState -benchmem -benchtime=20x ./benchmarks
```

Representative Windows/AMD64 results:

| Requests | Fixed window | Sliding log | Token bucket |
|---:|---:|---:|---:|
| 100 | 688 B/op | 10,000 B/op | 688 B/op |
| 1,000 | 688 B/op | 70,928 B/op | 688 B/op |
| 10,000 | 688 B/op | 1,471,788 B/op | 688 B/op |

### Reading the output

Go prints one row for every request-count and algorithm combination:

```text
BenchmarkLimiterState/requests_1000/sliding_log-8  20  233560 ns/op  70928 B/op  15 allocs/op
```

The row means:

- `requests_1000/sliding_log` identifies a batch of 1,000 calls using the sliding-log limiter.
- `-8` is the `GOMAXPROCS` value used by the benchmark process, not a request count.
- `20` is the number of complete batches executed because the command uses `-benchtime=20x`.
- `233560 ns/op` is the average time for one complete 1,000-request batch, not one request.
- `70928 B/op` is the average number of bytes allocated by one complete batch.
- `15 allocs/op` is the number of heap-allocation events per batch. A Go slice grows in chunks, so this is not the number of request entries stored.

The command uses `-run '^$'` to skip ordinary tests, `-bench=BenchmarkLimiterState` to select this benchmark, and `-benchmem` to include memory measurements. A final `PASS` means the benchmark completed successfully; benchmark results are measurements and are not compared with a speed threshold.

Each benchmark operation constructs a fresh limiter and sends the selected number of requests through it. The fixed window retains one usage counter, the token bucket retains a balance and refill timestamp, and the sliding log retains every request entry because the fixed clock prevents entries from expiring. Timing increases for every algorithm because each still processes every request; the distinguishing result is that fixed-window and token-bucket allocation stay constant while sliding-log allocation grows with the batch size.

The exact numbers depend on the machine and Go version, so they are evidence rather than acceptance thresholds. The important result is the growth pattern: fixed-window and token-bucket state remain constant while sliding-log allocation increases with active request history.

The sliding log provides exact rolling-window enforcement, but its traffic-dependent state is expensive for high-volume client/resource policies. Token bucket removes that state growth by retaining only an available-token balance and a last-refill timestamp per policy. Its tradeoff is different semantics: it permits an initial burst up to capacity and then enforces a sustained refill rate rather than an exact rolling-window count.
