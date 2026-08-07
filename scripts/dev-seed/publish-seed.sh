#!/usr/bin/env bash
#
# publish-seed.sh — upload the latest dev-seed build as a rolling GitHub
# Release (fixed tag, assets replaced in place). Consumers always fetch the
# same tag; see scripts/restore-dev-seed.sh.
#
# Usage: ./publish-seed.sh [build-dir]   (default: the `latest` symlink)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=config.sh
source "${SCRIPT_DIR}/config.sh"

DIR="$(readlink -f "${1:-${SEED_OUT_ROOT}/latest}")"
[[ -f "${DIR}/SHA256SUMS" ]] || { echo "no build at ${DIR} — run build-seed.sh first" >&2; exit 1; }
STAMP="$(basename "${DIR}")"

if ! gh release view "${SEED_TAG}" -R "${SEED_REPO}" >/dev/null 2>&1; then
  gh release create "${SEED_TAG}" -R "${SEED_REPO}" --latest=false \
    --title "Dev seed data (rolling)" \
    --notes "placeholder — replaced on first publish"
fi

gh release upload "${SEED_TAG}" -R "${SEED_REPO}" --clobber \
  "${DIR}"/*.dump "${DIR}/MANIFEST.txt" "${DIR}/SHA256SUMS"

gh release edit "${SEED_TAG}" -R "${SEED_REPO}" \
  --title "Dev seed data (rolling, ${STAMP})" \
  --notes "$(printf 'Lightweight desensitised dev fixture: a few hundred entities per database, full FK closure, sampled from the desensitised snapshot pipeline.\n\nRestore: `scripts/restore-dev-seed.sh` (or `pg_restore --no-owner -d <db> <db>.dump`).\nEvery seeded user logs in with password `kungal-dev`.\n\nBuilt: %s\n\n%s' "${STAMP}" "$(cat "${DIR}/MANIFEST.txt")")"

echo "published ${STAMP} → https://github.com/${SEED_REPO}/releases/tag/${SEED_TAG}"
