# syntax=docker/dockerfile:1.7

# --- build simc from source --------------------------------------------------
FROM debian:bookworm-slim AS simc-build
ARG SIMC_TAG=thewarwithin
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        git build-essential cmake ca-certificates \
    && rm -rf /var/lib/apt/lists/*
RUN git clone --depth 1 --branch ${SIMC_TAG} \
        https://github.com/simulationcraft/simc.git /src/simc
WORKDIR /src/simc
RUN cmake -DCMAKE_BUILD_TYPE=Release -DSC_NO_NETWORKING=ON -B build \
    && cmake --build build -j"$(nproc)" --target simc

# --- build the bot -----------------------------------------------------------
FROM golang:1.25-bookworm AS build

WORKDIR /src
ENV CGO_ENABLED=1

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build \
    go build -o /out/celestial-orrey ./cmd/bot

# --- runtime image -----------------------------------------------------------
FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata sqlite3 \
    && rm -rf /var/lib/apt/lists/*

ENV TZ=America/Los_Angeles

RUN useradd -r -u 1000 -g users appuser \
    && mkdir -p /app/data /app/data/simc /app/config /app/store/schema/migrations \
    && chown -R appuser:users /app

WORKDIR /app

COPY --from=simc-build /src/simc/build/simc /usr/local/bin/simc
COPY --from=build /out/celestial-orrey /app/celestial-orrey
COPY store/schema/migrations /app/store/schema/migrations
COPY config/config.yaml /app/config/config.yaml
COPY config/secrets.yaml /app/config/secrets.yaml

USER appuser

VOLUME ["/app/data"]

STOPSIGNAL SIGTERM

ENTRYPOINT ["/app/celestial-orrey"]
