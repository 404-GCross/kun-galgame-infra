---
name: nextmoe-draft-navigator
description: Topic index for the 24 NextMoe design docs in refs/docs/nextmoe-draft/. Use when a task touches NextMoe architecture, platform services, catalog/entity layer, community, moderation, media pipeline, search, open API, AI gateway, or editing engine — read only the docs relevant to the topic instead of the whole 452 KB corpus.
---

# NextMoe Draft Navigator

The corpus at `refs/docs/nextmoe-draft/` is 24 numbered docs (~452 KB, draft v2, revised 2026-07). Never read all of them; jump by topic. Later docs revise earlier ones — when they conflict, the later, more specific doc wins.

## Topic → doc index

| Topic | Docs |
|---|---|
| Vision, product matrix, what NextMoe is | 00, 08 |
| Overall architecture, five layers, hard constraints | 01 |
| Platform services (IdP/image/artifact/search/notify/wallet/moderation) | 02 |
| Contracts, OpenAPI, SDK, CI gates | 03 (revised by 15 §6, 19, 22) |
| Unified content platform, lazy extraction strategy | 04 |
| Frontend, kun-ui multi-brand, native apps | 05 |
| Infra reuse (reuse vs iterate vs build-new) | 06 |
| External references/prior art | 07 |
| Roadmap stages and triggers | 09 (entry point for "what's next") |
| Entity layer / catalog core design (P0-1) | 10 (hardened by 17 R1–R9) |
| Community primitives, thread model, trust levels (P0-3) | 11 |
| Manga media pipeline, bundle GC, delivery (P0-2) | 12 |
| Cross-media search, Meilisearch federation, CJK (P0-4) | 13 |
| Content rating, age-verification legislation risk | 14 (advisory notes, quarterly review) |
| Engineering assets: .github repo, kungal-kit, site-shell, copier template | 15 |
| Repository blueprint (all repos, six classes A–F) | 16 |
| Multi-source catalog upgrade, Bronze/Silver/Gold, R1–R9 | 17 |
| Trust & safety service (cmd/trust), reports, AI cascade | 18 |
| Open API program, wiki retirement W0–W5 | 19 |
| AI gateway (cmd/ai), semantic routes | 20 |
| Unified editing engine (proposals, revisions, field registry) | 21 |
| Open API phase 2: downstream migration to /internal | 22 |
| Write-face platformization (user writes via devapi) | 23 |

## Reading protocol

1. Identify the topic(s); read the matching doc(s) fully — they are decision records with explicit verdicts ("拍板"), trade-offs, and named triggers.
2. Check doc 09 if you need current stage/priority context.
3. Distinguish decided ("拍板稿") from draft ("DRAFT/待拍板"): docs 19, 20 contain sections still marked draft.
4. For long multi-doc research, delegate to a scout subagent (English prompt, file-based handoff) instead of reading in the main context.
