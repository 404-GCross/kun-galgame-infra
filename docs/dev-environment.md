# Local dev environment — the one-command platform

`docker-compose.dev.yml` (infra repo root) brings up the **whole nextmoe platform**
on your dev box with one command, so any product repo's `pnpm dev` / `air` can
depend on a single fact: **localhost has a platform**. Seven platform services
(from prebuilt GHCR images) + all local infrastructure, **zero cloud credentials**.

> This is the dev-environment track's step 01. Step 02 (`refresh-dev-db`) fills the
> databases with desensitised, real-shaped data; step 03 wires each product repo's
> `.env.example` to these ports. This file only stands up the services.

## What runs

| Service | Port (host) | Image / origin | Browser UI |
| --- | --- | --- | --- |
| oauth | 9277 | `ghcr.io/kunmoe/infra-oauth` | — |
| image | 9278 | `ghcr.io/kunmoe/infra-image` | — |
| artifact | 9279 | `ghcr.io/kunmoe/infra-artifact` | — |
| galgame | 9280 | `ghcr.io/kunmoe/infra-galgame` | — |
| catalog | 9281 | `ghcr.io/kunmoe/infra-catalog` | — |
| community | 9282 | `ghcr.io/kunmoe/infra-community` | — |
| trust | 9283 | `ghcr.io/kunmoe/infra-trust` | — |
| image-cdn-proxy (Caddy) | 9290 | `caddy:2-alpine` | — |
| MinIO (S3) | 9000 / 9001 | `minio/minio` | http://127.0.0.1:9001 (minioadmin/minioadmin) |
| Mailpit | 1025 / 8025 | `axllent/mailpit` | http://127.0.0.1:8025 |
| Meilisearch | 7700 | `getmeili/meilisearch` | http://127.0.0.1:7700 |
| Redis | 6379 | `redis:8-alpine` | — |
| **Postgres** | **5432** | **your host's own server — NOT in compose** | — |

The ports match production. Frontends (web/wiki/forum/moyu/letmoe) and out-of-band
jobs (image-gc, refping, cron, data-cutover tools) are **deliberately not here** —
frontends run their own `pnpm dev`, jobs run on demand.

### Network mode: `host`

Every service uses `network_mode: host`, so:

- inter-service URLs are all `127.0.0.1:<port>` (no service-name DNS), and
- a container's port is bound on the box directly — which is exactly what makes
  **Replace mode** (below) seamless.

## Prerequisites

1. **Host Postgres on `127.0.0.1:5432`, user `postgres`, password `191007`** — the
   dev source of truth (it is *not* a compose service). If your server is fresh,
   create the databases the services expect:

   ```sh
   psql -h 127.0.0.1 -U postgres -f docker/initdb.d/01-create-databases.sh   # or run the CREATE DATABASE lines
   ```

   (`kun_galgame_infra`, `kun_galgame_wiki`, `kun_images`, `kun_artifacts`,
   `kun_catalog`, `kun_community`, `kun_trust`.)

2. **GHCR access for the platform images** (they are private):

   ```sh
   gh auth token | docker login ghcr.io -u <your-gh-user> --password-stdin
   ```

## Bring up

```sh
# 1. Check nothing you care about already holds these ports — and NEVER kill a
#    process that does; stop the matching compose service instead (see below).
ss -tlnp | grep -E ':(9277|9278|9279|9280|9281|9282|9283|9000|9001|7700|1025|8025|9290|6379)\b'

# 2. Pull + start. migrate-* run first and gate the services.
docker compose -f docker-compose.dev.yml pull
docker compose -f docker-compose.dev.yml up -d

# 3. Watch health.
docker compose -f docker-compose.dev.yml ps
```

Pin an image tag with `INFRA_IMAGE_TAG` (a `sha-…` tag or `@sha256:` digest) when
you need reproducibility across a team:

```sh
INFRA_IMAGE_TAG=sha-abc1234 docker compose -f docker-compose.dev.yml up -d
```

### Port already in use?

`docker compose up` does **not** skip a busy port — it fails to bind. If a port is
held by *your own* native process (e.g. you already run `air` in `apps/api`, which
binds 9277-9283), that is the intended Replace-mode situation: just don't start
that container. Start a subset explicitly, e.g. only the infra + the two services
you need:

```sh
docker compose -f docker-compose.dev.yml up -d minio minio-setup mailpit meili image-cdn-proxy catalog community
```

## Verify (healthz checklist)

Every platform service answers `GET /healthz` → `{"status":"ok"}`:

```sh
for p in 9277 9278 9279 9280 9281 9282 9283; do
  printf '%s ' "$p"; curl -fsS "http://127.0.0.1:$p/healthz" && echo || echo DOWN
done
curl -fsS http://127.0.0.1:9000/minio/health/live && echo minio-ok   # MinIO
curl -fsS http://127.0.0.1:7700/health && echo                        # Meili
curl -fsS http://127.0.0.1:8025/api/v1/messages?limit=1 >/dev/null && echo mailpit-ok
```

- **MinIO console**: http://127.0.0.1:9001 (minioadmin / minioadmin)
- **Mailpit** captures every outbound mail (registration codes, etc.) — read them
  at http://127.0.0.1:8025. Nothing is ever delivered to a real inbox.

## Replace mode (stop a container, `go run` in its place)

Because host mode binds the prod port on the box, you can develop one service
against the live containerised rest of the platform:

```sh
docker compose -f docker-compose.dev.yml stop galgame     # free port 9280
cd apps/api && go run ./cmd/galgame                        # your code now IS the platform's galgame
```

Your local process reads the same host Postgres / MinIO / Meili as the containers,
so the rest of the stack talks to it transparently. Restart the container when done:

```sh
docker compose -f docker-compose.dev.yml start galgame
```

The same works for any of oauth / image / artifact / catalog / community / trust —
each is `go run ./cmd/<svc>` (see `apps/api/dev.sh` for the env each expects; the
container env in `docker-compose.dev.yml` is the canonical list).

## Image reads: the read-through CDN proxy

The image service emits content-addressed CDN URLs of the shape
`/<h1>/<h2>/<hash>.<ext>`. In dev its `KUN_IMAGE_PUBLIC_BASE_URL` points at the
Caddy proxy on **:9290**, which:

1. tries the **local MinIO** `kun-images` bucket first (a hit means you uploaded /
   seeded it locally), and
2. on a miss transparently **falls back to the prod public CDN**
   (`https://image.kungal.iloveren.link`), so images that were never seeded locally
   still render.

Uploads (write path) always go to local MinIO — there are **no cloud credentials**
on the dev box. Artifact downloads have **no** such proxy by design: a prod
artifact simply misses locally (upload a fresh one to exercise the flow).

## Data & credentials

- **Client secrets are not this file's concern.** The platform services only need
  DB / S3 / SMTP to boot; OAuth `client_secret`s live in the product repos'
  `.env.example`. Avatar/banner image-upload creds (`KUN_IMAGE_CLIENT_*`) are left
  empty here — set them only if you exercise those upload paths locally.
- **trust/community forwarding envs are empty = fail-closed off**, mirroring prod
  until a real cut-over wires them.
- Realistic data comes from **step 02** (`refresh-dev-db`); a bare bring-up gives
  you empty (freshly migrated) databases.

## Refreshing the databases (step 02 — `refresh-dev-db`)

`docker-compose.dev.yml` gives you empty, freshly-migrated databases. To fill
them with **desensitised, real-shaped production data**, use one command:

```sh
./scripts/refresh-dev-db.sh                    # all core DBs, latest artifact
./scripts/refresh-dev-db.sh --fresh            # rebuild the artifact on the server first
./scripts/refresh-dev-db.sh --db kun_community # just one core DB
./scripts/refresh-dev-db.sh --group sources    # stream the raw scrape DBs (dlsite/erogamespace)
```

Desensitisation happens **at the source** (裁定 1a): the prod host produces
already-clean artifacts; **raw production PII never reaches your dev box.** The
local script only downloads + restores (and deletes the download afterwards).

### Groups

| Group | Databases | How |
| --- | --- | --- |
| `core` (default) | kun_galgame_infra, kun_galgame_wiki, kungalgame, kungalgame_patch, kun_community, kun_catalog, kun_images, kun_artifacts | download desensitised `*.dump`, `terminate → drop → create → pg_restore -j4` |
| `sources` | dlsite, erogamespace | raw `pg_dump -Fc | pg_restore` stream — **zero PII, zero desensitisation**, no artifact |
| — | **kun_trust** | *not in any group.* Local trust = `go run ./cmd/migrate-trust` (re-seed). |
| — | **letmoe (any `*letmoe*`)** | **hard-refused** by the script — letmoe runs its own seed system. |

After a restore the script runs **PII assertions** (no real emails, empty
sessions, every OAuth secret = the dev derivation, …) and exits non-zero if any
fail — the pipeline proves it desensitised. Re-check any time without restoring:
`./scripts/refresh-dev-db.sh --assert-only --db kun_galgame_infra`.

### Desensitisation contract (what changes)

Server-side pipeline: `scripts/dev-snapshot/build-snapshot.sh` +
`scripts/dev-snapshot/scrub/<db>.sql` (one auditable SQL file per scrubbed DB).
It copies each prod DB **read-only** into a throwaway `dev_snapshot_scratch_<db>`
on the server, scrubs *that*, dumps it, and drops it — production is never
mutated.

| Data | Becomes |
| --- | --- |
| `users.email` / `original_email`, `user_migrations.source_email` | `user<id>@dev.local` |
| `users.password` / `kungal_password` / `moyu_password` | bcrypt of **`kungal-dev`** (one constant — log in as any user with password `kungal-dev`) |
| `users.ip`, `kungalgame_patch."user".ip`, `images.first_uploader_ip` | emptied |
| `oauth_clients.secret` | `sha256:` + hex(sha256(**`dev-secret-<client_id>`**)) — a client presenting the plaintext `dev-secret-<client_id>` authenticates |
| `oauth_clients.redirect_uris` | first-party localhost dev callbacks ensured present (forum :2333, patch :6969, wiki :9421) |
| `sessions`, `authorization_codes`, `password_resets`, `signing_keys`, `oauth_accounts` tokens | emptied (signing_keys: dev runs HS256 / self-bootstraps a fresh KEK) |
| private chat + DM content (`chat_message`, `message`, `user_message`, edit history) | `[dev-scrubbed] …` synthetic text |
| `kun_community` **held** posts (`status=1`) body + `community_flag.note` | `[dev-scrubbed] …` synthetic text |
| public content (topics, replies, comments, resources, catalog, wiki, images) | **preserved verbatim** |

The dev credentials above are **public by design** (裁定 3) — hard-code them in
each product repo's `.env.example` (step 03 consumes this).

### trust: re-seed, don't restore

`kun_trust` is never in a snapshot. Bring it up locally with the migration:

```sh
cd apps/api && go run ./cmd/migrate-trust      # creates + seeds kun_trust
```

If you need a dev subject-kind registration (a **dev** callback secret, unrelated
to the prod registry), insert one by hand — e.g.:

```sql
-- kun_trust: register a dev subject kind for local S2S callbacks
INSERT INTO trust_subject_kind (site, key, callback_url, callback_secret, is_deprecated, notify_on_dismiss, created_at)
VALUES ('kungalgame', 'forum_topic', 'http://127.0.0.1:9282/internal/trust/callback', 'dev-trust-callback-secret', false, false, now())
ON CONFLICT DO NOTHING;
```

### ⚠️ Schema truth lives in the migrations, never in a snapshot (裁定 1c)

A snapshot carries whatever schema production had **when it was taken**. If your
local code has a newer migration than the snapshot, **run that repo's migration**
— do not wait for the next snapshot and do not hand-patch columns:

```sh
cd apps/api
go run ./cmd/migrate           # kun_galgame_infra (oauth + site models)
go run ./cmd/migrate-galgame   # kun_galgame_wiki
# cmd/image, cmd/artifact AutoMigrate on boot
```

The snapshot is a **data** fixture and at most a drift *detector* — the
migrations are the single source of truth for structure.

## Tear down

```sh
docker compose -f docker-compose.dev.yml down       # keep data volumes
docker compose -f docker-compose.dev.yml down -v    # also drop redis/minio/meili volumes
```
