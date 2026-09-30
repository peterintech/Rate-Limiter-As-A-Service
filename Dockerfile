FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /bin/api ./cmd/api
RUN CGO_ENABLED=0 go build -o /bin/worker ./cmd/worker

FROM golang:1.26-alpine AS goose-build

RUN CGO_ENABLED=0 go install -tags="no_azuresql no_clickhouse no_libsql no_mssql no_mysql no_sqlite3 no_vertica no_ydb" github.com/pressly/goose/v3/cmd/goose@v3.28.0

FROM alpine:3.21 AS api

RUN adduser -D -H -u 10001 appuser
USER appuser

COPY --from=build /bin/api /bin/api

EXPOSE 8080

ENTRYPOINT ["/bin/api"]

FROM alpine:3.21 AS worker

RUN adduser -D -H -u 10001 appuser
USER appuser

COPY --from=build /bin/worker /bin/worker

ENTRYPOINT ["/bin/worker"]

FROM alpine:3.21 AS migrate

COPY --from=goose-build /go/bin/goose /bin/goose
COPY sql/schema /migrations

ENTRYPOINT ["/bin/goose"]
