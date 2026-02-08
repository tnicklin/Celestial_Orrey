# syntax=docker/dockerfile:1.7

FROM golang:1.24-bookworm AS build

WORKDIR /src
ENV CGO_ENABLED=1

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build \
    go build -o /out/celestial-orrey ./cmd/bot

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/*

ENV TZ=America/Los_Angeles

RUN useradd -r -u 10001 -g users appuser \
    && mkdir -p /app/data /app/config /app/store/schema/migrations \
    && chown -R appuser:users /app

WORKDIR /app

COPY --from=build /out/celestial-orrey /app/celestial-orrey
COPY store/schema/migrations /app/store/schema/migrations
COPY config/config.yaml /app/config/config.yaml
COPY secrets.yaml /app/config/secrets.yaml

USER appuser

VOLUME ["/app/data"]

STOPSIGNAL SIGTERM

ENTRYPOINT ["/app/celestial-orrey"]
