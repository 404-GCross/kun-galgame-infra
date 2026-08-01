# Project Guidelines

## 铁律 (Iron Rules — non-negotiable; these override every other guideline in this file)

1. **No background gradients in any UI, ever.** Never use gradient backgrounds in UI design (`bg-gradient-*`, `from-*/via-*/to-*`, `linear-gradient()`, `radial-gradient()`, `conic-gradient()`, etc.); use solid colors from the project's palette.
2. **Prefer KunUI components; do not modify KunUI itself.** When adding or changing frontend UI, reach for a KunUI component (`@kungal/ui-*`) first — do not hand-roll a native/custom component unless there is genuinely no KunUI equivalent for what you need. If KunUI appears to have a bug or is missing a feature, **do not edit KunUI's code** (it is a shared upstream library) — report it to the user directly instead, and let them decide how to proceed.


## Core Engineering Principles

> Shared baseline across all KUN Galgame repositories. Defaults, not dogma — apply judgment.

1. All commit messages must be written entirely in English.
2. All code comments must be written entirely in English. (Exemption, ruled 2026-07-24: literal quotes of actual system output — error strings, UI text — may keep their original language when identifying the exact symptom is the point of the comment.)
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
13. **One task = one Codex session; one assigned target repo = one branch = one worktree.** Never let two sessions write the same checkout. Prefer the user-level `codex-session new <repo> <session>` launcher; it exposes source-repo reference material through `$CODEX_SESSION_REFS` when present. Read it in place and never copy this repo's ignored multi-gigabyte `refs/` tree into any worktree. A single-repo session may write only its own worktree; an explicitly coordinated cross-repo operation may write only separately assigned target worktrees. Launcher source checkouts and refs are always read-only.
14. **Every DB-backed session gets its own test database.** Use only the launcher-provided `TEST_DATABASE_DSN`; never discover or fall back to a DSN from `.env`, and never print the DSN. Keep `GOMAXPROCS=8` and run DB integration suites with `-count=1 -p 1`. `kun_catalog` is always read-only; `kun_catalog_rehearsal` belongs only to the explicitly assigned rehearsal/aggregation track and is never a general test target.

## Current migration state (read before starting a task)

- Git state is not production state. First inspect the current branch/worktree and then read the read-only ledger at `$CODEX_SESSION_REFS/proj/161-n5-grand-window.md`; use `162`, `163`, and `165` for the immediately following infra/catalog work. The Wave 161 ledger currently records prepared window branches and acceptance evidence, not blanket proof that every downstream or production step ran.
- The checked-out infra code has the Wave 161 P5 retirement shape: the galgame write/staff/workflow faces and the two legacy feeds are gone, `galgame.game` is not registered, and only the `/v1/galgame` 410 tombstone remains. Do not reintroduce a galgame HTTP client or treat the retired Tier-A route table as a live contract.
- Before cross-repo work, verify the paired forum/moyu branch and deployment state explicitly. Never infer it from infra `origin/main`, and never mix pre-window and window-only commits in a shared checkout.

## Local development (one command)

`pnpm dev` starts **everything**: it brings up the platform base from
`docker-compose.dev.yml` (redis / minio / meili / mailpit / all migrations +
the two rarely-edited platform services **community / ai** from prebuilt GHCR
images) and then runs `air` for the five frequently-edited Go services
(**oauth / catalog / image / artifact / trust**, hot-reloaded from source)
plus the Nuxt frontends. catalog (:9281) hosts the catalog faces and the
`/v1/galgame` **410 tombstone only** (`galgameapp.MountRetiredPublic`); the
standalone galgame service (:9280) and every live galgame face are retired.
Ctrl-C stops only the hot stack; the base
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

`docs/integration/oauth`, `docs/image_service`, `docs/artifact`, and `docs/catalog` are the **single sources for the four active cross-service contracts** OAuth / image hosting / artifact (large files) / catalog. `docs/integration/galgame_wiki` is the retained source location for the retired contract and is being reduced to a 410/successor tombstone; its former route tables are not implementation guidance. Forum/patch only vendor the generated `docs/{oauth,image_service,artifact}` mirrors now. Catalog and retired galgame-wiki are portal + `docs:verify` sources without downstream mirrors.

- **To change a contract**: edit only these source files in the assigned infra worktree. Run `pnpm docs:sync --write` only from a separately assigned kungal-docs worktree in an explicitly coordinated sibling-layout workspace whose infra/forum/patch/docs targets are all separately assigned worktrees; then `pnpm docs:audit` (`docs:check` verifies mirror consistency + `docs:verify` verifies source == code) should report 0 error. If that topology is unavailable, run only feasible read-only checks and hand off the sync requirement; never write a launcher source checkout.
- The **source of truth for active contracts is in the code** (`cmd/oauth`, `cmd/image`, `cmd/artifact`, `cmd/catalog`, and their handlers). `internal/galgameapp` now implements only the public 410 tombstone. When code changes, update the owning source docs in the same PR; `docs:verify` catches code/doc drift.
- Unified docs portal: `docs-kungal.nextmoe.dev`; read the full ownership model (Tier A/B/C) from the read-only source-workspace copy at `${CODEX_SESSION_REPO%/*}/kungal-docs/docs/_meta/ownership.md`.

## Database schema changes → you must remind about migrations

**Whenever this change touches the database schema (a GORM model adding/changing a field or table, or raw SQL/constraints/indexes in `cmd/migrate*`), you must explicitly tell the user at the end of the task: whether a migration needs to run, which command to run, and against which database.** Deployment (push → CI → Dokploy redeploy) **does not run migrations automatically** — skipping one makes the live code read a column that doesn't exist (GORM `SELECT *` silently reads it as a zero value) → **silent failure**.

- Main database `kun_galgame_infra` (oauth + the various site models) → `go run ./cmd/migrate` (**not run automatically by deployment**).
- Catalog models → `go run ./cmd/migrate-catalog` against `KUN_CATALOG_PG_DATABASE`. Wave 161 removed the galgame family from this binary; it must not be recreated after the retirement DROP. Prod runs the catalog migration on deploy (`compose depends_on`), but outage-class changes still follow the manual migrate-first order.
- `cmd/image` / `cmd/artifact` → ship with `AutoMigrate` at service startup (runs automatically with deployment, no manual step needed).
- Production execution: the `infra-tools` image + an env-file dumped from the corresponding container's `.Config.Env` (see the prod ops notes).
- Lesson learned: in 2026-06 the `oauth_clients.moemoepoint_awarder` column was not migrated → the entire site could not award moemoepoints for ~29h.
