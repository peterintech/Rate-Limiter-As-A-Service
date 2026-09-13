package benchmarks

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/peterintech/global-rate-limiter/internal/ratelimiter"
)

func BenchmarkLimiterState(b *testing.B) {
	for _, requests := range []int{100, 1_000, 10_000} {
		b.Run(fmt.Sprintf("requests_%d", requests), func(b *testing.B) {
			b.Run("fixed_window", func(b *testing.B) {
				benchmarkFixedWindow(b, requests)
			})

			b.Run("sliding_log", func(b *testing.B) {
				benchmarkSlidingLog(b, requests)
			})
		})
	}
}

func benchmarkFixedWindow(b *testing.B, requests int) {
	key := ratelimiter.Key{ClientID: "client-a", Resource: "openai"}
	policies := ratelimiter.FixedWindowPolicies{
		key: {Limit: requests, Window: time.Minute},
	}
	now := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		limiter, err := ratelimiter.NewFixedWindowRateLimiter(policies, func() time.Time { return now })
		if err != nil {
			b.Fatal(err)
		}

		for range requests {
			if _, err := limiter.Allow(ctx, key, 1); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func benchmarkSlidingLog(b *testing.B, requests int) {
	key := ratelimiter.Key{ClientID: "client-a", Resource: "openai"}
	policies := ratelimiter.SlidingLogPolicies{
		key: {Limit: requests, Window: time.Minute},
	}
	now := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		limiter, err := ratelimiter.NewSlidingLogRateLimiter(policies, func() time.Time { return now })
		if err != nil {
			b.Fatal(err)
		}

		for range requests {
			if _, err := limiter.Allow(ctx, key, 1); err != nil {
				b.Fatal(err)
			}
		}
	}
}
