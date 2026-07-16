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
#   --full   bring the WHOLE platform up from images (incl. the five hot
#            services), do NOT run air/frontends — for developing a PRODUCT repo
#            (letmoe / forum / …), not infra. `pnpm dev:full`.
set -euo pipefail
cd "$(dirname "$0")/.."

COMPOSE=(docker compose -f docker-compose.dev.yml)
PROFILE_ARGS=()
FULL=0
[[ "${1:-}" == "--full" ]] && { FULL=1; PROFILE_ARGS=(--profile full); }

# The box may already provide a backing service on its shared port (a native
# redis on :6379 is common). The compose copy uses host networking, so it would
# fail to bind — and the shared env points every service at 127.0.0.1:<port>
# regardless. So: if a port is already answering, scale that compose service to
# zero and use what's already there. (This is why the dev compose does not
# start-gate on redis.)
declare -A SHAREABLE=([redis]=6379 [meili]=7700 [mailpit]=1025)
port_busy() { (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null && exec 3>&-; }

SCALE_ARGS=()
for svc in "${!SHAREABLE[@]}"; do
  if port_busy "${SHAREABLE[$svc]}"; then
    echo "  · :${SHAREABLE[$svc]} already served — using it, not starting compose '$svc'"
    SCALE_ARGS+=(--scale "$svc=0")
  fi
done

# Phase 1 — start the base. NO --wait here: the run-once jobs (migrate* /
# minio-setup) exit 0 when done, and `up --wait` treats a top-level service that
# exits as a failure. So start everything first, then wait only on the
# long-running services below (there the run-once jobs are depends_on
# `service_completed_successfully`, which --wait handles correctly).
echo "▶ platform base: catalog/community/ai + storage + migrations…"
"${COMPOSE[@]}" "${PROFILE_ARGS[@]}" up -d "${SCALE_ARGS[@]}"

# Phase 2 — block until every long-running service (restart != "no") is healthy,
# minus the ones we scaled to zero. This is what gates air on a migrated DB.
mapfile -t WAIT_SVCS < <(
  "${COMPOSE[@]}" "${PROFILE_ARGS[@]}" config --format json \
    | jq -r '.services | to_entries[]
             | select((.value.restart // "") != "no")
             | select((.value.scale // 1) != 0)
             | .key'
)
# Drop the scaled-to-zero backing services from the wait set.
for svc in "${!SHAREABLE[@]}"; do
  port_busy "${SHAREABLE[$svc]}" && WAIT_SVCS=("${WAIT_SVCS[@]/$svc}")
done
# shellcheck disable=SC2206
WAIT_SVCS=(${WAIT_SVCS[@]})   # re-pack after removals
echo "  waiting for: ${WAIT_SVCS[*]}"
"${COMPOSE[@]}" "${PROFILE_ARGS[@]}" up -d --wait "${WAIT_SVCS[@]}"

if ((FULL)); then
  echo "✔ whole platform up from images. Now run your product repo's \`pnpm dev\`."
  exit 0
fi

echo "▶ hot-reload stack: air (oauth/galgame/image/artifact/trust) + frontends."
echo "  Ctrl-C stops these; the base above stays up (pnpm dev:down to stop it)."
exec pnpm -F "./apps/**" --parallel --stream run dev
