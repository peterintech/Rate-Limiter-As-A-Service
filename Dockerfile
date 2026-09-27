# Minimal two-stage build for the V7 capacity and availability experiments.
# Only cmd/ and internal/ are copied, so the image never carries the repository.
FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/api ./cmd/api

FROM alpine:3.20

RUN adduser -D -H -u 10001 api

COPY --from=build /out/api /usr/local/bin/api

USER api

EXPOSE 8080

# The measurement runs must not pay for per-request access logging, so the
# image defaults to the environment that disables the Chi logger middleware.
ENV PORT=8080
ENV ENV=test

ENTRYPOINT ["/usr/local/bin/api"]
