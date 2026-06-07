# Docker deployment — kun-galgame-infra

This repo is the ecosystem **hub** (identity / image / galgame-wiki). Its
compose file owns the shared backing services (Postgres / Redis / MinIO /
Meilisearch); kungal + moyu connect to these.

## Layout

| File | Builds | Base image | Why |
|---|---|---|---|
| `docker/go.Dockerfile` | galgame + every `migrate-*` / worker (pure Go) | `distroless/static` (~25–45 MB) | `CGO_ENABLED=0` static binary |
| `docker/cgo.Dockerfile` | **oauth + image** | `debian:trixie-slim` (~180 MB) | both transitively import `kolesa-team/go-webp` → cgo → **libwebp** at build + runtime |
| `docker/nuxt.Dockerfile` | web + wiki (Nitro `node-server`) | `node:24-trixie-slim` (~390 MB) | self-contained `.output`; sharp comes via `@kungal/ui-nuxt`'s `@nuxt/image` |

Both Go Dockerfiles and the Nuxt one are **parametric** (`--build-arg CMD=…` /
`APP=…`) and require the **repo root** as build context (the pnpm workspace
install needs the lockfile + every workspace manifest).

> Why oauth needs cgo: it embeds the image-admin endpoints, and
> `image/service` imports the WebP `processor`. Extract that (or swap go-webp
> for a pure-Go encoder) and oauth could return to distroless.

## Quick start (single host)

```bash
docker compose build
docker compose up -d postgres redis minio meili   # shared infra
docker compose run --rm migrate                    # oauth schema + seed (sites/roles)
docker compose run --rm migrate-galgame            # galgame wiki schema
docker compose up -d oauth image galgame web wiki
```

Then (browser-facing, host ports are the **consecutive 15000–15013** range so
the stack coexists with a running `air` dev server; all Go services share a
root `/healthz`):

| Service | URL |
|---|---|
| oauth API | http://localhost:15005/healthz |
| image API | http://localhost:15006/healthz |
| galgame API | http://localhost:15007/healthz |
| web (admin) | http://localhost:15008 |
| wiki (galgame-wiki) | http://localhost:15009 |
| MinIO console | http://localhost:15003 |

Service-to-service traffic uses container ports via service names
(`postgres:5432`, `minio:9000`, `http://oauth:9277`, …) regardless of the host
mapping.

## Configuration

- Backend: 12-factor env via `docker/*.env` (loaded with `env_file`). **TEST
  secrets** — rotate `JWT_SECRET`, `POSTGRES_PASSWORD`, MinIO + Meili keys for a
  real deploy. `config.validate()` requires `KUN_PG_PASSWORD` + `JWT_SECRET` on
  every service.
- Frontends: public config (`apiBase`, oauth client, image CDN) is **baked at
  build** from the `PUBLIC_*` build args (mapped to the `KUN_*_NUXT_PUBLIC_*`
  names `nuxt.config.ts` reads). To build once and configure at runtime
  instead, set the canonical `NUXT_PUBLIC_*` env on the container — but note
  `oauthClientID`/`oauthRedirectURI` have awkward env-name mappings, which is
  why baking is the default here.

## Health checks

Distroless ships no shell/curl, so each Go service binary self-probes via a
`healthcheck` subcommand (`pkg/health`): the compose healthcheck runs
`/app healthcheck` (or `/app/app healthcheck` for the cgo images), which GETs
its own root `/healthz` and exits 0/1. Frontends use a Node TCP liveness probe.

## Notes / gotchas hit while building

- **No BuildKit/buildx** on this host → Dockerfiles avoid `--mount=type=cache`
  (plain layer caching only). Install buildx to re-enable cache mounts.
- **Meilisearch ≥ v1.13**: the `meilisearch-go` client sends `disableOnNumbers`
  (rejected by older servers). Pinned to `v1.20`. Bumping a *populated* Meili
  volume across major versions needs a dump/migrate — wipe the volume in dev.
- **sharp arch**: the Nuxt build bundles `sharp` for `linux-x64`; build + run
  both happen in linux-x64 containers, so they match. Don't copy host-built
  `.output` into the image.
- **Migrations** are one-off jobs (profile `jobs`), never auto-run on boot. The
  full cross-repo migration pipeline (migrate-users, migrate-galgame-data, …)
  is a separate ordered runbook — containerize each step the same way.

## Three-repo orchestration

Put an umbrella `website/compose.yaml` one level up that `include:`s each repo's
compose, but define `postgres`/`redis`/`minio`/`meili` **only here** (the hub).
kungal + moyu services then connect to `postgres:5432`, `http://oauth:9277`,
`http://galgame:9280`, etc. Front the lot with Caddy/Traefik by domain.

## Production hardening (not done here)

- Rotate all secrets; use `docker secret`/a vault, not `env_file`.
- Optional: build the cgo binaries fully static (musl + static libwebp) to put
  oauth/image back on `distroless/static`.
- Pin image digests; add resource limits; ship logs to a collector.
