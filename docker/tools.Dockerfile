#
# Tools image: EVERY apps/api/cmd/* binary in one image, for the one-off
# migration / maintenance jobs the per-service images don't carry — e.g.
# migrate-users, migrate-galgame-data, migrate-moyu-galgame, dedup-galgame-alias,
# reindex-search, sync-vndb, migrate-moemoepoint (see docs/deploy/03-bootstrap.md §B).
#
# The per-service Dockerfiles build ONE binary (ARG CMD) so you can't override
# the entrypoint of infra-galgame to run migrate-users — that binary isn't in it.
# This image bundles them all and invokes a job by name:
#
#   docker run --rm --network kun-galgame-infra_default \
#     --env-file docker/galgame.env ghcr.io/kunmoe/infra-tools reindex-search
#
# Built CGO_ENABLED=1 + libwebp so the cgo cmds (image*, oauth) compile too; the
# rest are pure Go. Build context MUST be the repo root.
ARG GO_VERSION=1.25

# ---- build (libwebp headers for the cgo cmds; pure-Go cmds build the same) ----
FROM golang:${GO_VERSION}-trixie AS build
RUN apt-get update && apt-get install -y --no-install-recommends \
        libwebp-dev pkg-config \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY apps/api/go.mod apps/api/go.sum ./
RUN go mod download
COPY apps/api/ ./
# -o <dir>/ writes one binary per cmd package, named after its directory.
RUN mkdir -p /out && CGO_ENABLED=1 GOOS=linux go build -trimpath -ldflags="-s -w" \
        -o /out/ ./cmd/...

# ---- run (debian-slim + libwebp runtime; binaries on PATH, invoked by name) ----
FROM debian:trixie-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
        libwebp7 libsharpyuv0 ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --uid 10001 --create-home --shell /usr/sbin/nologin appuser
WORKDIR /app
COPY --from=build /out/ /usr/local/bin/
# image* cmds read image_presets.yaml from this absolute path.
COPY apps/api/configs /app/configs
ENV KUN_IMAGE_PRESETS_PATH=/app/configs/image_presets.yaml
# sync-vndb / sync-vndb-enrich parse tagMap.ts at runtime. It lives in the
# repo's docs/ (re-included in .dockerignore). Run them with -tagmap, e.g.
#   docker run ... infra-tools sync-vndb -tagmap docs/tagMap.ts
COPY docs/tagMap.ts /app/docs/tagMap.ts
USER appuser
# No ENTRYPOINT: run a job by name, e.g. `docker run ... infra-tools migrate-users`.
