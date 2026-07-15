#!/usr/bin/env bash
#
# config.sh — single source of truth for the refresh-dev-db pipeline.
#
# Sourced by BOTH sides:
#   - scripts/dev-snapshot/build-snapshot.sh  (runs on the prod server, kungal-neo)
#   - scripts/refresh-dev-db.sh               (runs on the local dev box)
#
# Nothing here is a production secret. The dev credential derivations are
# deliberately public and reproducible (see 裁定 3 in the track charter):
# anyone can recompute them, which is the point of a dev fixture.
#
# shellcheck disable=SC2034  # every value here is consumed by a sourcing script.

# --- server / transport ------------------------------------------------------

# The Dokploy Postgres container that holds every production database.
PG_CONTAINER="kun-visual-novel-infra-vqvqbc-postgres-1"

# SSH alias for the production host (passwordless sudo docker).
SSH_HOST="kungal-neo"

# Where the server keeps desensitised artifacts. Rotated to RETAIN_SNAPSHOTS.
SNAPSHOT_ROOT="/var/dev-snapshots"
RETAIN_SNAPSHOTS=3

# --- database groups ---------------------------------------------------------

# core = the desensitised pipeline (download + restore). Charter 裁定 4.
CORE_DBS=(
  kun_galgame_infra
  kun_galgame_wiki
  kungalgame
  kungalgame_patch
  kun_community
  kun_catalog
  kun_images
  kun_artifacts
)

# Of the core DBs, these carry a scrub SQL file (scrub/<db>.sql) and are built
# via the server-side scratch database. Every other core DB is dumped straight
# from prod (read-only), because the PII survey confirmed zero sensitive data.
SCRUB_DBS=(
  kun_galgame_infra
  kungalgame
  kungalgame_patch
  kun_community
  kun_images
)

# sources = raw public scrape data (zero PII, zero desensitisation, no artifact).
# Streamed directly `pg_dump -Fc | pg_restore`. Charter 裁定 4.
SOURCE_DBS=(
  dlsite
  erogamespace
)

# Hard-refused everywhere: letmoe runs its own seed system, never the snapshot
# (裁定 1b). Matched as an exact name OR any name containing "letmoe".
REFUSED_DB_REGEX='letmoe'

# kun_trust is deliberately NOT in any group: local trust = `migrate-trust`
# re-seed (裁定 1b, most complete form). See docs/dev-environment.md.

# --- dev credential constants (public by design) -----------------------------

# Every user's password becomes the bcrypt of "kungal-dev" (one constant, so the
# hash is identical for all rows). Verified round-trip with golang.org/x/crypto
# bcrypt (cost 10). Login with password "kungal-dev".
DEV_BCRYPT='$2a$10$vSWo4K3J7j7t2ZKj9BTOnOlBUzgx1/onqF.n14rwMxgiprv5X.mAi'
DEV_PASSWORD_PLAINTEXT='kungal-dev'

# Emails become user<id>@<DEV_EMAIL_DOMAIN>.
DEV_EMAIL_DOMAIN='dev.local'

# OAuth client secrets become "sha256:" + hex(sha256(DEV_SECRET_PREFIX + client_id)).
# The server verifies by sha256-hashing the presented plaintext, so a client that
# presents the plaintext (DEV_SECRET_PREFIX + client_id) authenticates.
DEV_SECRET_PREFIX='dev-secret-'

# Synthetic free-text marker. Assertions look for it; the negative test proves a
# real value (e.g. an injected email) is rejected.
SCRUB_MARKER='[dev-scrubbed]'

# First-party OAuth clients whose localhost dev callback must be present in
# redirect_uris. "client_id=dev_callback_url". forum/patch/wiki already carry
# these in prod; the append is idempotent (dedup) and future-proofs a prod
# snapshot that ever lacks them. letmoe's dev client is not in prod at all — it
# is provisioned by letmoe's own seed, see docs/dev-environment.md.
DEV_REDIRECT_URIS=(
  "4ed9bc99ec0a789a4796b83e22bd84c5=http://127.0.0.1:2333/auth/callback"   # forum  (www.kungal.com)
  "df3ff6008d740bfacbe46aa8cf483cf2=http://127.0.0.1:6969/auth/callback"   # patch  (www.moyu.moe)
  "53e9b5ea70bfc4e4d0700a9f7b8818e8=http://127.0.0.1:9421/auth/callback"   # wiki   (wiki.kungal.com)
)

# --- local restore target ----------------------------------------------------

LOCAL_PG_HOST="localhost"
LOCAL_PG_PORT="5432"
LOCAL_PG_USER="postgres"
# The dev-box Postgres password is the documented dev constant (docs/dev-environment.md).
: "${PGPASSWORD:=191007}"
export PGPASSWORD

# Optional suffix appended to every target database name. Empty = the live local
# dev databases. Set DEVDB_SUFFIX=_devtest to restore into throwaway databases
# WITHOUT touching the live ones (used by the pipeline's own verification).
: "${DEVDB_SUFFIX:=}"
