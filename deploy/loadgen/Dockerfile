# Pinned Vegeta build used as the load generator for the V7 experiments.
# The version matches the one already documented for the host-installed tool.
FROM golang:1.26-alpine

ARG VEGETA_VERSION=12.13.0

RUN go install github.com/tsenart/vegeta/v12@v${VEGETA_VERSION}

# No ENTRYPOINT: every attack is invoked explicitly, for example
#   docker compose run --rm vegeta sh -c "vegeta attack ... | vegeta report"
