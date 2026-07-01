# 03 — OIDC Standardization ("rebirth" of the OAuth server) — Design

> The definitive design for turning the self-built KUN Galgame OAuth 2.0 server
> (`cmd/oauth`) into a **standards-compliant, minimal OpenID Connect Provider (OP)**
> — WITHOUT replacing it. We keep our hardened Authorization Code + PKCE server and
> hand-roll only the missing standard pieces: an `id_token`, asymmetric signing
> (ES256 active + RS256 supported), a JWKS endpoint, discovery documents, and a
> spec-compliant wire format. Grounded in OIDC Core 1.0, OIDC Discovery 1.0,
> RFC 8414, RFC 7517 (JWK), RFC 9068 (JWT access tokens), RFC 9700 (BCP 240, 2025),
> OIDC RP-Initiated Logout, and OIDC Back-Channel Logout.
> See also [01-creator-role-design](./01-creator-role-design.md) and
> [02-account-switching-design](./02-account-switching-design.md).

> **Implementation status (2026-07-01)**: **Phase 0 implemented** — key infrastructure:
> `signing_keys` table (three-state, `kid`-tagged), `pkg/oidckeys` (ES256/RS256 keygen,
> RFC 7638 thumbprint kid, JWK encoding, AES-256-GCM at-rest), startup key bootstrap,
> and the public `GET /oauth/jwks` + `/.well-known/{openid-configuration,oauth-authorization-server}`
> endpoints — all gated on `KUN_OIDC_KEY_ENC_KEY`, world-readable (ACAO *), standard JSON.
> Verified end-to-end (crypto unit tests + live discovery/JWKS smoke test). **Phases 1–4
> not started** (asymmetric signing cutover, id_token, wire-format, deferred items).
> This is the keystone of the developer platform (todos #6), deliberately sequenced
> **before** any Huma-ification of the auth surface, because an IdP's canonical
> machine-readable contract is the **OIDC discovery document, not a hand-authored
> OpenAPI spec**. Governing premise: **every current consumer is first-party**
> (kungal / moyu / wiki, one team) → the ecosystem can afford a **coordinated clean
> cutover**; we optimize for the cleanest correct end state, not for preserving the
> legacy wire format forever.
> ⚠️ Phase 0 added a new `signing_keys` table on `kun_galgame_infra` → run
> `go run ./cmd/migrate` (deploy does NOT auto-run it — see the project migration rule).
> Also set a strong `KUN_OIDC_KEY_ENC_KEY` (the KEK that encrypts signing keys) on the
> oauth service, or key bootstrap + jwks/discovery stay disabled.

## 0. Locked decisions

| # | Decision | Consequence |
|---|----------|-------------|
| 1 | **Keep self-built; hand-roll only the missing standard bits.** NOT adopting an OIDC library or migrating to an off-the-shelf IdP. | The hard, easy-to-get-wrong security core is **already correct and already RFC 9700-compliant** (PKCE-everywhere, refresh rotation, reuse detection, CAS anti-race, `alg` allow-listing). The remaining surface (id_token, JWKS, discovery, key lifecycle) is **small and built on stable specs**. Self-built auth stays a first-party asset; no dependency introduced into the identity core. |
| 2 | **Asymmetric signing: ES256 active + RS256 supported.** | OIDC Core §15.1 and RFC 9068 make RS256 **mandatory-to-implement**; `alg=none` is forbidden. ES256 is the *active* signer (smaller keys/signatures, cheaper than RSA); RS256 keys are published too so any conformant verifier works. Verification switches from a shared symmetric secret to public keys. |
| 3 | **Three-state, `kid`-tagged signing keys published via JWKS.** | Keys move `pending` (pre-published) → single `active` signer → `retired` (still published for verification) → deleted after retention. Rotation is routine, not an incident. Private keys are encrypted at rest. |
| 4 | **Minimal `id_token`; roles stay OUT of it.** | `id_token` carries only `iss, sub, aud, exp, iat` (+ `nonce`/`auth_time` when applicable) — it authenticates the *user to the client*, nothing more. Authorization data (`roles`, `site_id`, `scope`) lives in the **access token + `/userinfo`**, never in the `id_token`. Our current server already puts roles there — this decision keeps it that way. |
| 5 | **Access tokens become RFC 9068 JWTs; `/userinfo` stays; revocation model unchanged.** | Access token = `typ=at+jwt`, audience-restricted. This ENABLES stateless local verification via JWKS, but we **do not drop `/userinfo` introspection**. Ban immediacy — a moat — is preserved: bans are enforced at `/authorize` + refresh (already), and access-token TTL stays short (15 min), so a banned user's local-verifiable token dies within one TTL. RFC 7662 introspection is deferred until a resource server needs sub-TTL revocation. |
| 6 | **Standardize the wire format on the OAuth/OIDC protocol endpoints only; coordinated first-party cutover.** | Drop the private `{code,message,data}` envelope on `/oauth/token`, `/oauth/userinfo`, `/oauth/revoke`, discovery, and JWKS → spec-compliant top-level JSON + standard OAuth error objects (`{"error","error_description"}`, RFC 6749 §5.2). The house envelope **stays** on `/auth/*` self-service and `/api` business endpoints. Migrate via expand→contract across repos (§7), then delete the old shape — no dual-shape-forever. |
| 7 | **Code + PKCE only. Everything else deferred.** | `response_type=code` + PKCE S256 is the whole flow surface. Implicit `SHOULD NOT`, ROPC `MUST NOT` (RFC 9700 / OAuth 2.1). Hybrid, dynamic client registration (DCR), request objects, pairwise `sub`, and sender-constraining (mTLS/DPoP) are **out of scope now** — added only when a real third party needs them. |

## 1. Mental model

Today `cmd/oauth` is a **complete, hardened OAuth 2.0 Authorization Server** wearing a
private wire format. OIDC is, by its own definition, *"a simple identity layer on top
of the OAuth 2.0 protocol"* — it **reuses** the same `/authorize` and `/token`
endpoints and adds only the `openid` scope + an `id_token`. So this is **not a rewrite**.
We are:

- **Adding** three genuinely-required OIDC pieces the server lacks: an `id_token`,
  asymmetric signing (with RS256 support), and a published JWKS.
- **Publishing** the machine-readable contract (discovery) so any OIDC library can
  integrate by configuration, not by reading our code.
- **Standardizing** the wire format so an off-the-shelf OIDC client can parse our
  responses (today it cannot — the envelope breaks it).
- **Changing nothing** about the security core (PKCE, refresh rotation, reuse
  detection, CAS, ban enforcement) — it already meets the current BCP.

The result is a **minimal but spec-correct OP**: everything a first-party SSO needs,
and exactly the subset a third party will later discover and consume.

## 2. Spec primitives (build on these, don't invent)

- **`id_token`** — a JWT authenticating the end-user; REQUIRED in the code-flow token
  response when `openid` scope is present. Required claims: `iss, sub, aud, exp, iat`
  (+ `nonce` if the auth request carried one). (OIDC Core §2, §3.1.3.6)
- **RS256 mandatory-to-implement** — an OP MUST support signing tokens with RS256;
  `alg=none` is forbidden. ES256 may be the active algorithm. (OIDC Core §15.1; RFC 9068 §2.1)
- **JWKS + `jwks_uri`** — the OP publishes its public keys as a JWK Set over HTTPS;
  `jwks_uri` is REQUIRED in OIDC discovery. Each key carries a `kid`. (RFC 7517; OIDC Discovery)
- **Discovery** — `/.well-known/openid-configuration` (identity specialization) and
  `/.well-known/oauth-authorization-server` (RFC 8414, the OAuth-native generalization).
  Serve both; they share a body. **This document is the IdP's canonical machine-readable
  contract.** (OIDC Discovery 1.0; RFC 8414)
- **RFC 9068 JWT access token** — `typ=at+jwt` header (keeps it distinct from `id_token`
  per RFC 8725), `iss/sub/aud/exp/iat/jti` claims, audience-restricted.
- **RP-Initiated Logout** — `end_session_endpoint` (support GET + POST); any
  `post_logout_redirect_uri` MUST be pre-registered and matched **exactly**; `id_token_hint`
  identifies the session. (OIDC RP-Initiated Logout 1.0)
- **Back-Channel Logout** — OP → RP server-to-server logout token; browser-independent,
  **the cross-TLD-reliable channel** (front-channel breaks under third-party-cookie
  blocking across different TLDs). Optional, for confidential RPs only. (OIDC Back-Channel Logout 1.0)
- **RFC 9700 (BCP 240)** — the security baseline we already satisfy (§7).

## 3. What we already have vs what we add (gap map)

| Concern | Today (verified in code) | Target |
|---|---|---|
| Flow | `/authorize` code + PKCE S256, `/token`, `/userinfo`, `/revoke` (RFC 7009), consent, custom logout (`cmd/oauth/main.go:196-219`) | Same flow, unchanged |
| Signing | **HS256, single `cfg.JWT.Secret`** (`pkg/utils/jwt.go` `GenerateAccessToken:32`) | **ES256 active + RS256** via `signing_keys` (§4) |
| Access token | HS256 JWT, claims `sub/id/email/name/roles/scope/site_id` (`TokenClaims`) | RFC 9068 `at+jwt`, same claims + proper `aud`, ES256-signed |
| Refresh token | HS256 JWT **but looked up by DB value, signature never verified** (`FindByRefreshTokenOrPrev`; `ParseRefreshToken` has **no callers**) | **Opaque random string** (drop the vestigial JWT signing) |
| `id_token` | **none** | Minimal, ES256-signed, issued on `openid` scope |
| JWKS | **none** | `GET /oauth/jwks` (`jwks_uri`) |
| Discovery | **none** | `/.well-known/{openid-configuration,oauth-authorization-server}` |
| Wire format | private `{code,message,data}` envelope on protocol endpoints (handler-added; DTOs `dto.TokenResponse`/`dto.UserInfoResponse` are already the standard shape) | standard top-level JSON + standard OAuth errors |
| Logout | custom `/oauth/logout` top-level bounce (`docs/integration/oauth/07`) | formalized `end_session_endpoint` semantics; back-channel optional |
| Scopes | `openid`/`profile`/`email` already recognized + enforced (`CreateAuthorizationCode:179`, `GetUserInfo:406`) | unchanged |

**Key blast-radius fact (why this is low-risk).** The access-token signature is verified in
exactly **four first-party services, all in the infra repo**: the OP itself (`middleware.Auth`
→ `internal/platform/auth/service/auth_service.go:807`), the galgame service
(`internal/middleware/jwt_auth.go:33,68`), image (`internal/platform/image/middleware/auth.go:138`),
and artifact (`internal/platform/artifact/middleware/auth.go:127`). **Downstream (forum/moyu/wiki)
verify nothing** — they call `GET /oauth/userinfo` to introspect (forum
`internal/user/oauth/client.go` `FetchUserInfo`). So switching to asymmetric signing is **purely
additive externally** and a **coordinated change across these four internal verifiers**. Bonus:
today all four services hold `cfg.JWT.Secret` (each can *forge* tokens); under asymmetric only the
OP holds the private key and galgame/image/artifact need only the **public key** — a real reduction
of the signing-secret footprint. (Note: `middleware.Auth` additionally does a per-request live ban
check — see §6.)

## 4. Data model — signing keys (`signing_keys`)

New table on `kun_galgame_infra`. Private keys **encrypted at rest**; JWKS publishes only
public material.

| Column | Meaning |
|--------|---------|
| `kid` (PK) | Key ID; appears in every JWS header and the JWK entry |
| `alg` | `ES256` (default) or `RS256` |
| `use` | `sig` |
| `public_jwk` (jsonb) | Public JWK — served verbatim in the JWKS |
| `private_key_enc` (bytea) | Private key, AES-256-GCM encrypted under a KEK from env (`KUN_OIDC_KEY_ENC_KEY`) — never served, never logged |
| `state` | `pending` → `active` → `retired` |
| `created_at` / `activated_at` / `retired_at` | Lifecycle timestamps |

**Key lifecycle (three states + overlap):**

1. **pending** — generated, its public JWK **already published** in the JWKS, but not yet
   signing. Lets verifiers pre-fetch the key before it's ever used.
2. **active** — the single signer. Activating a new key demotes the previous active to
   `retired` (there is always exactly one active per `alg`).
3. **retired** — no longer signs, but its public JWK **stays in the JWKS** until every
   token it signed has expired (≥ max access-token TTL), then it is deleted.

**Signer** picks the `active` key for the chosen `alg` and sets the JWS `kid`. **Verifiers**
read `kid` from the header and select the matching public key from the local key set
(cached from the DB/JWKS). Rotation cadence and overlap windows are config knobs (sensible
defaults: rotate ~90d, publish-before-activate ~a few min–hours, keep-retired ≥ access TTL).

**Key storage decision:** DB table with app-level AES-GCM encryption (KEK in env). Rationale:
no external KMS dependency, survives our single-Postgres deployment model, and the KEK is one
env var to protect — consistent with how the rest of infra handles secrets. (KMS is a future
option if/when the threat model warrants it.)

## 5. Token & endpoint design

### 5.1 Access token (RFC 9068)
- Header: `typ: at+jwt`, `alg: ES256`, `kid`.
- Claims (per RFC 9068 §2.2): `iss` (the OP issuer URL), `sub` (user UUID), `aud` (resource
  server identifier — map from the client's `site_id`; today `SiteID` already binds the token to
  a site for image_service's cross-site quota check), `client_id` (the requesting client),
  `exp` (15 min), `iat`, `jti` (already minted), plus `scope`, `roles`, `site_id`. Signed ES256.
- Verified by the four infra sites (§3) via the `active`/`retired` public keys.

### 5.2 id_token (minimal)
- Header: `typ: JWT`, `alg: ES256`, `kid`.
- Claims: `iss`, `sub`, `aud` = **client_id** (the RP, distinct from the access token's `aud`),
  `exp`, `iat`, `nonce` (only if the auth request carried one), `auth_time` (when `max_age`/
  the account-switch step-up applies — ties into [02](./02-account-switching-design.md) §6).
- **No `roles`, no `scope`, no `site_id`.** Issued in the `/token` response only when
  `openid` scope was granted.

### 5.3 Refresh token
- Becomes an **opaque, high-entropy random string** (it is already DB-looked-up and its JWT
  signature is never verified — see §3). Storage, rotation, reuse-detection, grace window, and
  CAS in `RefreshWithClient` / `AuthService.RefreshToken` are **unchanged**; only the token's
  *format* changes from a signed JWT to random bytes.

### 5.4 New / changed endpoints
| Endpoint | Change |
|---|---|
| `GET /.well-known/openid-configuration` | **new** — discovery (issuer, endpoints, `jwks_uri`, `id_token_signing_alg_values_supported: [ES256, RS256]`, `response_types_supported: [code]`, `grant_types_supported: [authorization_code, refresh_token]`, `scopes_supported`, `subject_types_supported: [public]`, `code_challenge_methods_supported: [S256]`, `end_session_endpoint`) |
| `GET /.well-known/oauth-authorization-server` | **new** — RFC 8414 body (same content) |
| `GET /oauth/jwks` | **new** — JWK Set of all `pending`/`active`/`retired` public keys |
| `POST /oauth/token` | issue `id_token` on `openid` scope; **standard top-level JSON**; standard OAuth error objects |
| `GET /oauth/userinfo` | **standard top-level JSON** (drop envelope) — DTO is already the right shape |
| `GET /oauth/logout` → `end_session_endpoint` | formalize to RP-Initiated Logout (GET+POST, `id_token_hint`, exact-match `post_logout_redirect_uri` — the allow-list already exists in `ValidatePostLogoutRedirect`) |
| (optional, deferred) back-channel logout | server-to-server logout token to confidential RPs (forum/moyu) — **complements**, does not replace, doc 02's revocation-based logout for pure SPAs |

## 6. Where `roles` live & the revocation trade-off (decision #4 + #5)

- `id_token` = identity only. `access token` + `/userinfo` = authorization (`roles`).
  This is already how the code behaves; we lock it in and must resist stuffing `roles` into
  the `id_token` when we add it.
- **Stateless vs call-home** for the access token: RFC 9068 JWTs let a resource server verify
  locally via JWKS (scalable, no per-request call-home). We **keep `/userinfo`** so callers who
  want live state still have it. The revocation moat — centralized IdP ban taking effect
  immediately — is **preserved, and stronger than pure TTL-bounding**: the OP's `middleware.Auth`
  already does a **per-request live ban check** (`GetCurrentUser` + `IsBanned()`, `middleware/auth.go:58-72`),
  so `/userinfo` and the OP's own protected endpoints reflect a ban **instantly**; bans are also
  enforced at `/authorize` and every refresh (`user.IsBanned()` at `ExchangeCode:321` and
  `RefreshWithClient:593`). Only the separate resource servers that verify the JWT locally
  (galgame / image / artifact) and downstream RPs are TTL-bounded — and access-token TTL is 15 min,
  so any locally-verified token is stale within one TTL. **This is already true today with HS256, so
  nothing regresses.** If a future resource server needs sub-TTL revocation, add RFC 7662
  introspection then — not now.

## 7. Wire-format cutover (decision #6) — expand → contract across repos

The DTOs are already standard; only the handler's `{code,message,data}` wrapper and the
error objects change. Because all clients are first-party, do a **coordinated expand→contract**,
never a dual-shape-forever:

1. **Expand consumers (tolerant readers).** Update each first-party OAuth client
   (forum `internal/user/oauth/client.go`, moyu, wiki) to parse **either** the standard
   top-level JSON **or** the legacy envelope. Ship + deploy these first. Nothing breaks —
   the OP still emits the old shape.
2. **Flip the producer.** Change the OP's protocol-endpoint handlers to emit standard
   top-level JSON + standard OAuth errors. Tolerant consumers keep working through the flip.
3. **Contract.** Once all RPs run tolerant readers against the new shape, remove the
   legacy-envelope tolerance from consumers and any old-shape code from the OP.

Sequencing note: because downstream *introspects* and mints its own server session at login,
the only consumers of these responses are the RP OAuth-callback code paths — a small, known,
first-party set. No content-negotiation or versioned endpoints needed; a coordinated cutover
is the right call here precisely because the fleet is mono-owned.

## 8. Security checklist (MUSTs — RFC 9700 / RFC 9068 / OIDC Core)

- [x] **Already met:** PKCE-everywhere (public MUST, confidential done), refresh rotation +
  reuse detection, `alg` allow-listing, atomic code consumption, open-redirect allow-list,
  refresh bound to `client_id`, short access TTL. (RFC 9700 §2.1/§2.2)
- [ ] **Asymmetric verify only:** verifiers accept **only** the OP's published `alg`s
  (ES256/RS256) and reject HS* and `none` — extend the current HMAC-only guard to an
  asymmetric allow-list. (RFC 9068 §4)
- [ ] **JWKS over HTTPS**, public keys only, `kid` on every key + every JWS header. (RFC 7517)
- [ ] **Audience-restrict** access tokens (`aud` = resource server) so a moyu token can't be
  replayed against kungal's API. (RFC 9700 §2.3)
- [ ] **`id_token` minimal** — no `roles`/authz data; validate `nonce` when present. (OIDC Core)
- [ ] **Private keys encrypted at rest**, KEK never logged, never in the JWKS.
- [ ] **Discovery leaks nothing** beyond endpoints/capabilities (no secrets, no internal hosts).
- [ ] **Issuer consistency** — the `iss` claim in every token == the discovery `issuer` == the base
  the `.well-known` documents are served from (a classic OIDC footgun; verifiers reject on mismatch).
- [ ] **Retired keys stay published** until their tokens expire (no premature deletion → no
  spurious verification failures).

## 9. Anti-patterns — do NOT do these

- ❌ Put `roles`/authz data in the `id_token` (it's for authenticating the user, not authorizing).
- ❌ Keep HS256 for signing once asymmetric is in — verifiers must reject HS* and `none`.
- ❌ Hand-roll JWS crypto — use `golang-jwt/jwt/v5` ES256/RS256 (already the dependency); only
  the *key lifecycle* is ours to build.
- ❌ Serve, log, or embed private key material anywhere near the JWKS.
- ❌ Maintain the old envelope and the standard shape forever — expand→contract, then delete (§7).
- ❌ Add implicit/hybrid/DCR/request-objects/pairwise `sub` "while we're in here" — deferred (§0.7).
- ❌ Delete a retired signing key before its longest-lived token has expired.
- ❌ Forget the `signing_keys` migration on `kun_galgame_infra` (deploy won't run it).

## 10. Migration & rollout (phases)

Each phase is independently deployable; earlier phases are additive.

0. **Key infrastructure.** ✅ **DONE** — `signing_keys` table + generation/encryption + `GET /oauth/jwks`
   + both discovery documents (advertising ES256/RS256, code+PKCE, endpoints). Nothing
   consumes them yet → **zero break**. → `go run ./cmd/migrate` on `kun_galgame_infra`.
   (Files: `pkg/oidckeys/`, `internal/platform/auth/{model/signing_key.go,repository/signing_key_repository.go,service/signing_key_service.go,handler/oidc_handler.go}`, wired in `cmd/oauth/main.go`, gated on `KUN_OIDC_KEY_ENC_KEY`.)
1. **Asymmetric signing.** Sign access tokens (and, next phase, id_tokens) with the `active`
   ES256 key; switch the **four** infra verifiers (§3) to public-key verification by `kid`
   (fetched from local JWKS / the key set); drop `cfg.JWT.Secret` from galgame/image/artifact
   verification (public key only). Make refresh tokens opaque random strings. **Externally
   additive** (downstream introspects, verifies nothing). Internally, run a **one-access-TTL
   accept-both verify window**: the four verifiers accept HS256 (old secret) OR ES256 (new keys)
   so in-flight ≤15-min HS256 tokens issued just before the flip don't 401; after the OP has
   signed ES256 for one access-TTL, drop HS256 acceptance. No dual-*sign* overlap is needed
   (nothing external verifies our signature) — only this brief dual-*verify* window for tokens
   already in the wild. In-flight refresh tokens are unaffected: they're matched by DB value, not
   signature, so old JWT and new opaque refresh tokens coexist naturally.
2. **id_token + logout.** Issue the minimal `id_token` on `openid` scope; formalize
   `end_session_endpoint` (RP-Initiated Logout) over the existing `/oauth/logout`.
3. **Wire-format cutover.** Expand→contract (§7): tolerant RP readers → flip OP to standard
   JSON + standard errors → remove legacy shape.
4. **Deferred (when a third party actually arrives).** DCR, mTLS/DPoP sender-constraining,
   RFC 8707 resource indicators, RFC 7662 introspection, back-channel logout for confidential RPs.

**Contract-doc impact (Tier-A):** implementing this changes the source contracts under
`docs/integration/oauth/` (`01-oauth-endpoints`, `04-tokens-and-errors`, `07-logout`) and adds
discovery/JWKS pages. Update the **source** docs in this repo in the same PR, then run
`pnpm docs:sync --write` + `docs:audit` from `kungal-docs` to regenerate the forum/moyu mirrors
(never hand-edit the downstream copies).

## 11. Open implementation points (resolve at build time)

- **KEK provisioning & rotation** (`KUN_OIDC_KEY_ENC_KEY`): where it lives, how it's rotated
  without re-encrypting all private keys at once (envelope-encrypt each private key under a
  per-key DEK wrapped by the KEK).
- **Exact `aud` values**: resource-server identifiers for the access token (per-site? per-API?)
  vs `client_id` for the `id_token`. Note the access token **keeps** its `site_id` claim (§5.1),
  so galgame/image/artifact need **no lockstep logic change** — `aud` is added for RFC 9068
  compliance; migrating those checks from `site_id` to `aud` is optional and can happen later.
- **Verifier key-fetch mechanism** for the separate `image`/`artifact` binaries: pull the JWK
  Set from `/oauth/jwks` (cache + refresh on unknown `kid`) vs read the `signing_keys` table
  directly (same DB is reachable). JWKS-over-HTTP is the more decoupled, standard choice.
- **Back-channel logout vs revocation-based (doc 02):** confirm whether forum/moyu want
  immediate server-to-server logout (they DO hold server-side Redis sessions, unlike pure SPAs);
  if so, back-channel is viable for them as an enhancement — reconcile with [02](./02-account-switching-design.md) §0 decision #2.
- **`/auth/refresh` self-service path** shares the refresh model — confirm the opaque-refresh
  change covers it (`AuthService.RefreshToken:579`) as well as the OAuth path.
- **Rotation cadence & overlap defaults** and whether rotation is a scheduled job (fits the
  existing in-process job registry / scheduler) or manual.
