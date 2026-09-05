# ============================================================================
# kamado-api build
#
# Stage 1 (ui):   Build the Svelte dashboard to ui/dist.
# Stage 2 (go):   Build the Go binary, embedding the UI via //go:embed.
# Stage 3 (run):  Minimal debian with tini and the static binary.
#
# The build context for this Dockerfile is the ./api directory by
# default, which is fine for stage 2. But stage 1 needs the ui/
# directory from the repo root. docker-compose sets `context: .` at
# the repo root and `dockerfile: api/Dockerfile` to make both visible;
# for plain `docker build ./api`, set --build-context repo=..
# ============================================================================

# ----------------------------------------------------------------------------
# Stage 1: build the Svelte dashboard
# ----------------------------------------------------------------------------
FROM node:22-bookworm-slim AS ui

WORKDIR /ui
# Copy just the manifest first so npm install is cached across code
# edits. The build context must be the repo root, docker-compose
# uses `context: .`, and `make api` invokes docker build with the
# repo root as context.
COPY ui/package.json ./
RUN npm install --no-audit --no-fund

COPY ui/ ./
RUN npm run build

# ----------------------------------------------------------------------------
# Stage 2: build the Go binary with the UI embedded
# ----------------------------------------------------------------------------
FROM golang:1.22-bookworm AS build

WORKDIR /src
COPY api/go.mod ./
RUN go mod download 2>/dev/null || true
COPY api/ .

# Drop the built dashboard into the embed target before `go build` so
# //go:embed picks it up. We delete the committed placeholder first.
RUN rm -rf internal/webui/dist && mkdir -p internal/webui/dist
COPY --from=ui /ui/dist/ internal/webui/dist/

RUN CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -ldflags="-s -w" \
        -o /out/kamado-api \
        ./cmd/kamado-api

# ----------------------------------------------------------------------------
# Stage 3: runtime
# ----------------------------------------------------------------------------
FROM debian:bookworm-slim AS runtime

RUN apt-get update && apt-get install --no-install-recommends -y \
        ca-certificates \
        tini \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /var/lib/kamado /var/log/ckpool /run/ckpool

COPY --from=build /out/kamado-api /usr/local/bin/kamado-api

EXPOSE 8080

ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/kamado-api"]
