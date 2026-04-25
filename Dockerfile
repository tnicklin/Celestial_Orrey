# syntax=docker/dockerfile:1.7

# --- build simc from source --------------------------------------------------
# We build only the engine via its legacy Makefile. The top-level CMakeLists
# pulls in the Qt GUI subdir unconditionally, which fails without Qt5/Qt6
# installed; the make path skips the GUI entirely and is what the official
# simc Docker images use.
FROM debian:bookworm-slim AS simc-build
ARG SIMC_TAG=midnight
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        git build-essential ca-certificates \
    && rm -rf /var/lib/apt/lists/*
RUN git clone --depth 1 --branch ${SIMC_TAG} \
        https://github.com/simulationcraft/simc.git /src/simc
WORKDIR /src/simc/engine
RUN make optimized OS=UNIX SC_NO_NETWORKING=1 -j"$(nproc)"

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
    && apt-get install -y --no-install-recommends ca-certificates tzdata sqlite3 locales \
    && sed -i 's/^# *\(C.UTF-8\)/\1/' /etc/locale.gen \
    && locale-gen \
    && rm -rf /var/lib/apt/lists/*

ENV TZ=America/Los_Angeles
ENV LANG=C.UTF-8
ENV LC_ALL=C.UTF-8

RUN useradd -r -u 1000 -g users appuser \
    && mkdir -p /app/data /app/data/simc /app/config /app/store/schema/migrations \
    && chown -R appuser:users /app

WORKDIR /app

COPY --from=simc-build /src/simc/engine/simc /usr/local/bin/simc
COPY --from=build /out/celestial-orrey /app/celestial-orrey
COPY store/schema/migrations /app/store/schema/migrations
COPY config/config.yaml /app/config/config.yaml
COPY config/secrets.yaml /app/config/secrets.yaml

USER appuser

VOLUME ["/app/data"]

STOPSIGNAL SIGTERM

ENTRYPOINT ["/app/celestial-orrey"]
