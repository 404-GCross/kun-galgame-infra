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

## Tear down

```sh
docker compose -f docker-compose.dev.yml down       # keep data volumes
docker compose -f docker-compose.dev.yml down -v    # also drop redis/minio/meili volumes
```
