# Distribute the API for availability

Status: Accepted

Date: 2026-09-27

## Summary

Run two API instances behind one Nginx entry point to preserve rate limit decisions when one API instance stops. Do not claim that this topology raises capacity from the current local experiment. The private in memory token buckets remain intentionally unchanged in this phase so their cluster correctness failure is visible and measurable.

## Context

The project goal is a highly available global rate limiter with one accurate quota across all application instances. A single API process cannot meet the availability part of that goal because stopping the process stops every decision.

Capacity, availability, and global correctness are different properties. They require separate evidence.

The local capacity sweep measured the load generator, proxy, APIs, and unrelated containers on one eight logical CPU host. The API containers remained below their CPU limits. The result cannot isolate application capacity or prove that adding an instance raises it.

The outage experiment does isolate availability. The distributed route returned a successful response for all 15,000 requests while each API instance was stopped for 30 seconds. The single route returned 850 HTTP errors when its only upstream was unavailable.

The quota experiment isolates global correctness. Two independent instances approved 16,664 requests where one approved 8,332 under the same demand. Private state therefore doubled the intended cluster allowance.

## Requirements

1. AC-1: The comparison exposes one route backed by one API instance and one route backed by two API instances through the same proxy configuration.
2. AC-2: The distributed route continues returning rate limit decisions when either one API instance is unavailable.
3. AC-3: The single route records client visible failures when its only API instance is unavailable.
4. AC-4: Both API instances keep private token bucket state during this phase so quota multiplication remains measurable.
5. AC-5: Capacity numbers are described as complete local harness observations until the load generator and system under test use isolated resources without unrelated workloads.
6. AC-6: No Redis or other shared state is introduced until the private state failure is demonstrated.

## Options considered

### Keep one API instance

This preserves the simplest topology and one in memory quota. It fails the availability goal because one process outage stops all decisions.

### Distribute the API to claim more capacity

This is not accepted. The current experiment cannot isolate API capacity, and the distributed route did not produce a higher passing boundary in the shared local harness.

### Distribute the API for availability

This is accepted. The outage experiment directly shows that one surviving instance can continue making decisions. It also exposes the private state correctness failure that the next phase must solve.

## Decision

Use two API replicas behind Nginx as the project topology for availability. Treat Nginx and the host as remaining single points of failure in this measurement scaffold. Make no production capacity claim from the current results.

Preserve the in memory token bucket during this phase. The measured 2.00 quota multiplication establishes the requirement for one shared atomic decision across all replicas in the next phase.

## Rationale

The project needs distribution because availability is part of the final goal. The outage experiment directly demonstrates that a surviving API replica keeps decisions available. Capacity is not used as justification because the current test setup cannot isolate application capacity.

Keeping private state for this phase makes the next problem visible. The exact 2.00 multiplication shows that adding replicas without shared atomic state breaks the meaning of a global quota.

## Proposed stack

| Layer | Choice | Purpose |
| --- | --- | --- |
| Entry point | Nginx | Route requests and retry another API peer when one peer is unavailable. |
| Application | Two identical Go API processes | Remove one API process as a single point of failure. |
| Rate limit state | Private in memory token bucket for this phase | Preserve and measure the cluster correctness failure before shared state is added. |
| Measurement | Vegeta plus container resource samples | Measure availability, quota multiplication, and the limits of the local harness. |
| Deployment scope | Local Compose measurement scaffold | Reproduce the experiment without presenting it as production orchestration. |

## Consequences

The service can continue making decisions when one API instance stops and Nginx can reach the survivor.

The topology adds proxy and deployment complexity.

Nginx and the single host remain outside the availability guarantee.

Private token bucket state makes the cluster quota incorrect. The effective allowance grows with the number of instances.

Application capacity remains unknown. A valid capacity decision requires an independent load generator, isolated deployment resources, and no unrelated workloads.

## Verification

AC-1 is verified by the two Nginx routes and the preflight that confirms their upstream membership.

AC-2 and AC-3 are verified by the 150 second outage experiment.

AC-4 is verified by the 600 RPS quota experiment and `TestIndependentInstancesMultiplyQuota`.

AC-5 is enforced by the interpretation and limits recorded in `docs/load-testing.md`.

AC-6 is verified by the absence of shared state from the application wiring in this phase.

## Next decision

Introduce shared atomic token bucket state so every replica enforces one cluster quota. Redis is justified by the measured correctness failure, not by an unproven capacity claim.

Redis outage behavior and degraded mode remain separate future decisions. They should be introduced only after shared enforcement works and its failure is demonstrated.

## References

sources

`docs/load-testing.md`

`docs/requirements.md`

`cmd/api/check_test.go`

`compose.yaml`

`deploy/nginx/nginx.conf`
