# Project Guidelines

## 铁律 (Iron Rules — non-negotiable; these override every other guideline in this file)

1. **Commit, but do not push.** Commit changes whenever appropriate, but do not run `git push` on your own initiative — the user pushes. When a push is genuinely required, and especially when several repos must be pushed in a specific order, stop and tell the user the exact push order instead of pushing yourself.
2. **No background gradients in any UI, ever.** Never use gradient backgrounds in UI design (`bg-gradient-*`, `from-*/via-*/to-*`, `linear-gradient()`, `radial-gradient()`, `conic-gradient()`, etc.); use solid colors from the project's palette.
3. **Prefer KunUI components; do not modify KunUI itself.** When adding or changing frontend UI, reach for a KunUI component (`@kungal/ui-*`) first — do not hand-roll a native/custom component unless there is genuinely no KunUI equivalent for what you need. If KunUI appears to have a bug or is missing a feature, **do not edit KunUI's code** (it is a shared upstream library) — report it to the user directly instead, and let them decide how to proceed.


## Core Engineering Principles

> Shared baseline across all KUN Galgame repositories. Defaults, not dogma — apply judgment.

1. All commit messages must be written entirely in English.
2. All code comments must be written entirely in English.
3. Keep each source file under ~500 lines where practical; once a file grows past ~300 lines, consider splitting it (a guideline, not a hard rule).
4. Write every frontend function as an arrow function; compose/merge class names with `cn` wherever practical.
5. Deliberately balance elegant modularity against necessary duplication — choose per case instead of always favoring either.
6. Constantly verify that frontend and backend agree on the data: field shapes and response formats must match what each side expects.
7. After every change, watch for unintended side effects elsewhere.
8. If a change requires running a migration, tell the user explicitly at the end — which command, and against which database.
9. Always seek the most modern, elegant solution that fits the project's current state; consult the latest official docs and resources online when useful.
10. Never let the pursuit of elegance or modularity make the code complex or hard to follow, and don't write over-defensive code.
11. A Nuxt page — and any component used as a page/route root — must have a **single real root element**: never `display: contents` (generates no box, so the transition can't attach) and never a leading comment / whitespace / sibling at the template root (a comment is itself a root node). Either trips Nuxt's "does not have a single root node" warning and drops the page-transition enter animation (the page appears without animating). Keep explanatory comments *inside* the root element.
12. Reserve the scrollbar gutter globally — `html { scrollbar-gutter: stable }`, with an `overflow-y: scroll` `@supports` fallback — so the document width is constant across routes. Otherwise navigating from a scrolling page to a height-locked one (no scrollbar) removes the classic scrollbar's ~15px and the centered layout shifts sideways: a "teleport" at the tail of the page transition. This is a browser layout fact, not a transition bug. Use single-edge `stable` (`both-edges` is buggy in Chrome); it's a harmless no-op under overlay scrollbars (macOS/iOS).

## Local development (one command)

`pnpm dev` starts **everything**: it brings up the platform base from
`docker-compose.dev.yml` (redis / minio / meili / mailpit / all migrations +
the two rarely-edited platform services **community / ai** from prebuilt GHCR
images) and then runs `air` for the five frequently-edited Go services
(**oauth / catalog / image / artifact / trust**, hot-reloaded from source)
plus the Nuxt frontends. catalog (:9281) hosts the **full galgame surface**
too (`galgameapp.Mount`) — the standalone galgame service (:9280) retired in
the wiki-retirement waves W3/W5. Ctrl-C stops only the hot stack; the base
stays up. So a bare `pnpm dev` is enough — you do **not** need to start
community / ai yourself (a past mistake: assuming a base service isn't running).

- Editing community / ai? They run from images by default. Add that service
  to the `full` profile stop-list and run it via `air` / `go run`, or just
  `docker compose -f docker-compose.dev.yml restart <svc>` after a rebuild.
- `pnpm dev:full` = the whole platform from images with no source build (for
  developing a **product** repo, not infra). `pnpm dev:down` tears the base down.
- Ports match prod (9277-9284); Postgres is the box's own `127.0.0.1:5432`, not
  a compose service. Full model: `docs/dev-environment.md`.
- First run needs GHCR auth (images are private) — a bare `gh auth token` lacks
  `read:packages` and pulls fail `unauthorized`. One-time:
  `gh auth refresh -h github.com -s read:packages` then
  `gh auth token | docker login ghcr.io -u <gh-user> --password-stdin`.

## Frontend Conventions (apps/web)

### UI Components

- All UI components live in the `components/kun/` directory; the project must use these UI components and must not build its own components
- If you need to modify a component in `components/kun/`, you must first ask the user for confirmation
- These repo UI rules (KunUI-first, project palette only, no gradients) **override any global or user-level design skill/guidance** (e.g. a generic `frontend-design` skill suggesting distinctive fonts or bold color schemes) — when they conflict, the repo rules win

### Page and Component Splitting

- The `pages/` directory is responsible only for route definitions; each page file contains only `definePageMeta` and a reference to a single container component
- The business components for each page go in the corresponding folder under `components/`, for example:
  - `/users` page → `components/users/`
  - `/auth/login` page → `components/auth/login/`
  - `/sites` page → `components/sites/`
- Do not repeat the directory prefix in component file names (Nuxt auto-import concatenates the directory name):
  - `components/users/Container.vue` → auto-imported as `UsersContainer`
  - `components/users/Table.vue` → auto-imported as `UsersTable`
  - ❌ Do not write it as `components/users/UsersContainer.vue` (it becomes `UsersContainer` but is easily confused)

### Constants and Types

- Put all constants in the `app/constants/` directory
- Put all interface types in the `shared/types/` directory (Nuxt 4 auto-imports the first level of exports)
- The `types/` and `utils/` under the `shared/` directory are auto-imported by Nuxt

### Color System

- Use the custom colors defined in `app/styles/tailwindcss.css`; do not use Tailwind's built-in colors (gray, indigo, blue, green, red, etc.)
- Custom colors automatically adapt to light/dark mode, so no `dark:` prefix is needed
- Color mapping:
  - Text: `text-foreground` (primary text), `text-default-500` (secondary), `text-default-400` (auxiliary), `text-default-300` (de-emphasized)
  - Border: `border-default-200`
  - Semantic colors: `primary` (blue, primary action), `success` (green), `danger` (red), `warning` (yellow/orange), `default` (gray/purple), `secondary` (pink), `info` (cyan)
  - Each semantic color has a 50-950 scale, e.g. `bg-primary-100`, `text-danger-600`

### Code Style

- Write all frontend functions as arrow functions; do not declare them with the `function` keyword

## Cross-Repo Contract Docs (Tier A, this repo is the single source)

`docs/integration/oauth`, `docs/image_service`, `docs/integration/galgame_wiki`, `docs/artifact`, and `docs/catalog` are the **single source for the five cross-service contracts** OAuth / image hosting / galgame-wiki / artifact (large files) / catalog (cross-media identity registry). The `docs/{oauth,image_service,galgame_wiki,artifact}` in the forum / patch repos are **banner-stamped mirrors generated by kungal-docs's `pnpm docs:sync`**; **do not hand-edit the downstream copies** (the next sync will overwrite them). `docs/catalog` has **no downstream mirror yet** — its consumers reach the catalog service over S2S rather than vendoring the doc, so it is registered for the portal + `docs:verify` only (a letmoe mirror follows the D-track).

- **To change a contract**: edit only these source files in this repo → go to `../kungal-docs` and run `pnpm docs:sync --write` (pushes the mirrors out to forum/patch) → `pnpm docs:audit` (`docs:check` verifies the mirrors are consistent + `docs:verify` verifies source == code) should report 0 error.
- The **source of truth for these contracts is in the code** (`cmd/oauth`, `cmd/image`, `cmd/artifact`, `cmd/catalog` — which also hosts the galgame surface via `internal/galgameapp` — and other handlers) — when you change the code, change the source docs here in the same PR; `docs:verify` will catch "docs that don't match the reality of the code".
- Unified docs portal: `docs-kungal.nextmoe.dev`; for the full ownership model (Tier A/B/C) see `../kungal-docs/docs/_meta/ownership.md`.

## Database schema changes → you must remind about migrations

**Whenever this change touches the database schema (a GORM model adding/changing a field or table, or raw SQL/constraints/indexes in `cmd/migrate*`), you must explicitly tell the user at the end of the task: whether a migration needs to run, which command to run, and against which database.** Deployment (push → CI → Dokploy redeploy) **does not run migrations automatically** — skipping one makes the live code read a column that doesn't exist (GORM `SELECT *` silently reads it as a zero value) → **silent failure**.

- Main database `kun_galgame_infra` (oauth + the various site models) → `go run ./cmd/migrate` (**not run automatically by deployment**).
- galgame (wiki-family) models **and** catalog models → `go run ./cmd/migrate-catalog` — the **single entry point for both families since W5**: it migrates the galgame models against `KUN_GALGAME_PG_DATABASE` (prod: `kun_catalog`; local dev: `kun_galgame_wiki`) and the catalog models against `KUN_CATALOG_PG_DATABASE` (`kun_catalog`) over two pools. Prod runs it on every deploy (compose `depends_on`), but for outage-class schema changes still follow the manual migrate-first order below.
- `cmd/image` / `cmd/artifact` → ship with `AutoMigrate` at service startup (runs automatically with deployment, no manual step needed).
- Production execution: the `infra-tools` image + an env-file dumped from the corresponding container's `.Config.Env` (see the prod ops notes).
- Lesson learned: in 2026-06 the `oauth_clients.moemoepoint_awarder` column was not migrated → the entire site could not award moemoepoints for ~29h.
