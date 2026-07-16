#!/usr/bin/env bash
# One-command local dev for this repo. Two halves, one command:
#
#   1. PLATFORM BASE (docker-compose.dev.yml, default profile) — redis / minio /
#      meili / mailpit / the read-through image proxy / all migrations / and the
#      three rarely-edited platform services (catalog / community / ai) from
#      prebuilt GHCR images. Brought up once, then LEFT RUNNING.
#   2. HOT-RELOAD STACK — `air` rebuilds the five frequently-edited Go services
#      (oauth / galgame / image / artifact / trust) from source on every save,
#      plus the Nuxt frontends (web / wiki / developer) via their own dev servers.
#
# The base and the hot stack never collide: the five hot services carry the
# `full` compose profile, so the default `up` below deliberately does NOT start
# them — their host ports (9277-9283) stay free for air.
#
# Ctrl-C stops ONLY the hot stack; the base keeps running (that's the point —
# "localhost has a platform"). Tear the base down with `pnpm dev:down`.
#
# Want the ENTIRE platform from images with no source build (e.g. you are
# developing a PRODUCT repo, not infra)? Use `pnpm dev:full` instead.
set -euo pipefail
cd "$(dirname "$0")/.."

COMPOSE="docker compose -f docker-compose.dev.yml"

echo "▶ platform base: redis/minio/meili + catalog/community/ai + migrations…"
# --wait blocks until every started service is healthy and every migration has
# completed, so air never races an un-migrated database.
$COMPOSE up -d --wait

echo "▶ hot-reload stack: air (oauth/galgame/image/artifact/trust) + frontends."
echo "  Ctrl-C stops these; the base above stays up (pnpm dev:down to stop it)."
exec pnpm -F "./apps/**" --parallel --stream run dev
