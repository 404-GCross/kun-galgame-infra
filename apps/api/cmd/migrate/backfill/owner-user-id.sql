-- catalog_work.owner_user_id backfill (wave 178)
--
-- WHAT THIS IS FOR
-- ----------------
-- Wave 178 made per-user entity ownership a fact the CATALOG holds:
-- catalog_work.owner_user_id, written once at submission mint
-- (service/work_submit.go) or at claim birth (service/claim_lifecycle.go's
-- `claim` action), and read by the editing engine to derive
-- PolicyContext.IsEntityOwner — which is what admits an entry's creator to the
-- kungal overlay's owner-review lane WITHOUT any product backend asserting it.
--
-- Every row that predates the wave therefore has owner_user_id IS NULL, meaning
-- "unknown", not "nobody". Until this backfill runs, a kungal creator loses the
-- owner-review capability on the user-token face (they keep it over S2S, where
-- the forum still asserts is_entity_owner). Nothing else regresses: derivation
-- can only ever turn the flag ON.
--
-- The migration itself (`go run ./cmd/migrate-catalog` against
-- KUN_CATALOG_PG_DATABASE) only ADDS the nullable column + its index. This file
-- is the DATA step, run MANUALLY by the track lead, and it is idempotent: every
-- statement below is guarded by `owner_user_id IS NULL`, so re-running it never
-- re-attributes a work whose owner is already known. That guard is the SQL half
-- of the write-once rule the Go write paths enforce.
--
-- Run order: step 1 (forum DB) → step 2 (catalog DB) → step 3 (catalog DB) →
-- verification. Steps 2 and 3 are independent of each other; step 3 alone is
-- valid on a database with no kungal export to hand.

-- ===========================================================================
-- STEP 1 — export the forum's creator column (run against the FORUM database,
--          kungalgame, with psql; \copy writes client-side, so no server-side
--          file permission is needed)
-- ===========================================================================
--
--   \copy (SELECT id AS gid, creator_user_id FROM galgame WHERE creator_user_id IS NOT NULL) TO 'galgame_creators.csv' CSV HEADER
--
-- `galgame.id` is the product-side work id — exactly what the catalog stores as
-- catalog_work.product_work_id for site='kungal'. Rows with a NULL creator are
-- skipped rather than exported as NULLs: they carry no information, and letting
-- them through would only widen the join for nothing.

-- ===========================================================================
-- STEP 2 — load and apply (run against the CATALOG database,
--          KUN_CATALOG_PG_DATABASE, from the directory holding the CSV)
-- ===========================================================================

BEGIN;

-- A temp table, not a real one: this staging data has no life beyond the
-- session, and ON COMMIT DROP would fire before the UPDATE in an interactive
-- run, so the table is dropped explicitly at the end of the transaction.
CREATE TEMP TABLE tmp_creators (
  gid             bigint PRIMARY KEY,
  creator_user_id bigint NOT NULL
);

-- \copy tmp_creators (gid, creator_user_id) FROM 'galgame_creators.csv' CSV HEADER

-- The join is (site, product_work_id) — the claim identity — and the write is
-- fenced by `owner_user_id IS NULL` so an owner already stamped by the Go write
-- paths (a submission minted after the wave shipped) is never overwritten by
-- the older forum value.
UPDATE catalog_work w
   SET owner_user_id = t.creator_user_id
  FROM tmp_creators t
 WHERE w.site = 'kungal'
   AND w.product_work_id = t.gid
   AND w.owner_user_id IS NULL;

DROP TABLE tmp_creators;

COMMIT;

-- ===========================================================================
-- STEP 3 — fall back to the birth claim event for rows still NULL (CATALOG
--          database; idempotent, safe to run on its own and repeatedly)
-- ===========================================================================
--
-- Every claim born through the lifecycle service or the submission mint appended
-- ONE event with from_state IS NULL — the birth of the claim, the transition
-- with no prior state (model/claimevent.go). Its actor is the person who brought
-- the work into the registry, which is the same fact owner_user_id records. The
-- earliest such event wins (id order) so a re-claimed row keeps its FIRST
-- claimant, matching the write-once rule; actor_uid > 0 excludes machine-driven
-- claims, which have no user to attribute.

UPDATE catalog_work w
   SET owner_user_id = e.actor_uid
  FROM (
        SELECT DISTINCT ON (work_id) work_id, actor_uid
          FROM catalog_claim_event
         WHERE from_state IS NULL
           AND actor_uid > 0
         ORDER BY work_id, id
       ) e
 WHERE w.id = e.work_id
   AND w.owner_user_id IS NULL;

-- ===========================================================================
-- VERIFICATION (CATALOG database)
-- ===========================================================================

-- How far the backfill got, per tenant: `owned` should be ~the number of kungal
-- works whose forum row carries a creator; `unknown` is the honest remainder
-- (imported / machine-created works that never had a human creator).
SELECT COALESCE(site, '(unclaimed)') AS site,
       count(*)                                        AS works,
       count(owner_user_id)                            AS owned,
       count(*) - count(owner_user_id)                 AS unknown
  FROM catalog_work
 WHERE deleted_at IS NULL
 GROUP BY 1
 ORDER BY works DESC;

-- Spot check: no owner may be 0 or negative (the write paths refuse to stamp
-- one, and this must stay true after a manual load). Expect 0 rows.
SELECT id, site, product_work_id, owner_user_id
  FROM catalog_work
 WHERE owner_user_id IS NOT NULL
   AND owner_user_id <= 0;
