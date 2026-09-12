# End Goal

Build a highly available global rate-limiting service that protects the company's use of external APIs whose pricing and availability depend on strict quotas.

Today, individual microservice instances enforce limits independently. When a service is replicated, every instance behaves as though it owns the full quota, causing third-party `429 Too Many Requests` responses and avoidable financial penalties. The completed system must provide one accurate view of usage across the cluster.

## Required capabilities

- Support different limits for different clients, providers, and API resources.
- Enforce the same quota regardless of which rate-limiter instance receives a request.
- Return rate-limit decisions within a few milliseconds.
- Continue serving controlled traffic during temporary database or cache outages instead of blocking every request.
- Record every approved request for analytics and billing without putting durable database writes on the decision path.
- Provide real-time usage and historical reporting, including average response time and trend views for 10, 15, and 30 days.

## Final architecture

The target system will separate fast enforcement from durable processing:

```text
Internal services
       |
Load balancer
   /   |   \
Go limiter instances
       |
Shared rate-limit state
       |
Durable usage events
       |
Background workers
       |
PostgreSQL
       |
Analytics dashboard
```

The exact components and algorithms will be selected through tests and measured failures. A component is introduced only after the preceding design demonstrates the problem that requires it.

## Final deliverable

The finished qualification submission will include:

- Complete source code packaged as a ZIP file.
- An architecture diagram in image format.
- Purposeful code comments.
- Unit, race-condition, load, and performance tests.
- A Dockerfile and `docker-compose.yml` that start the application and its dependencies with `docker compose up --build`.
- A README explaining startup, testing, architecture, and edge-case verification.
