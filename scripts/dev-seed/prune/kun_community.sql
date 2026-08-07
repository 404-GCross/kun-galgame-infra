-- Prune kun_community down to the threads whose anchors survive in the forum
-- seed. Runs AFTER prune/kungalgame.sql, which exports the kept content ids.
--
-- Anchor model (site='kungal'): anchor_kind=1 anchors on a forum galgame id;
-- anchor_kind=2 anchors on '<prefix>:<id>' where prefix 'resource' points at
-- forum galgame_resource rows and quiz/toolset/website are site-global
-- features we keep wholesale. 'rating' threads are dropped: their targets are
-- per-user rating rows that do not survive sampling, so keeping them would
-- only produce dangling anchors in dev.

-- CSV paths are CWD-relative: \copy does not interpolate psql variables, so
-- build-seed.sh runs this file with CWD = the shared export directory.

BEGIN;

CREATE TEMP TABLE in_galgame (id bigint PRIMARY KEY);
\copy in_galgame FROM 'kungalgame_galgames.csv'
CREATE TEMP TABLE in_resource (id bigint PRIMARY KEY);
\copy in_resource FROM 'kungalgame_resources.csv'

-- Root keep-set, then close it over merged_into_id chains so the thread
-- self-FK stays satisfied after the delete.
CREATE TEMP TABLE keep_thread (id bigint PRIMARY KEY);
INSERT INTO keep_thread
WITH RECURSIVE roots AS (
  SELECT t.id
  FROM community_thread t
  WHERE t.site <> 'kungal'
     OR (t.anchor_kind = 1 AND t.anchor_id ~ '^[0-9]+$'
         AND t.anchor_id::bigint IN (SELECT id FROM in_galgame))
     OR (t.anchor_kind = 2
         AND split_part(t.anchor_id, ':', 1) IN ('quiz', 'toolset', 'website'))
     OR (t.anchor_kind = 2 AND split_part(t.anchor_id, ':', 1) = 'resource'
         AND split_part(t.anchor_id, ':', 2) ~ '^[0-9]+$'
         AND split_part(t.anchor_id, ':', 2)::bigint IN (SELECT id FROM in_resource))
), chain AS (
  SELECT t.id, t.merged_into_id
  FROM community_thread t JOIN roots r ON r.id = t.id
  UNION
  SELECT t.id, t.merged_into_id
  FROM community_thread t JOIN chain c ON t.id = c.merged_into_id
)
SELECT DISTINCT id FROM chain;

CREATE TEMP TABLE keep_post (id bigint PRIMARY KEY);
INSERT INTO keep_post
SELECT id FROM community_post WHERE thread_id IN (SELECT id FROM keep_thread);

-- Children of posts first (soft refs, but keep the discipline), then posts,
-- then threads.
DELETE FROM community_reaction   WHERE post_id NOT IN (SELECT id FROM keep_post);
DELETE FROM community_flag       WHERE post_id NOT IN (SELECT id FROM keep_post);
DELETE FROM community_review_item WHERE post_id NOT IN (SELECT id FROM keep_post);
DELETE FROM community_thread_user WHERE thread_id NOT IN (SELECT id FROM keep_thread);
DELETE FROM community_post       WHERE id NOT IN (SELECT id FROM keep_post);
DELETE FROM community_thread     WHERE id NOT IN (SELECT id FROM keep_thread);

-- Platform uids still referenced by the kept rows. Exported for the
-- kun_galgame_infra prune; trust rows shrink to the same set.
CREATE TEMP TABLE keep_user (id bigint PRIMARY KEY);
INSERT INTO keep_user
SELECT DISTINCT u FROM (
  SELECT author_id        FROM community_post
  UNION SELECT target_user_id FROM community_post
  UNION SELECT created_by     FROM community_thread
  UNION SELECT fb_responder_id FROM community_thread
  UNION SELECT user_id        FROM community_reaction
  UNION SELECT flagger_id     FROM community_flag
  UNION SELECT decided_by     FROM community_review_item
  UNION SELECT user_id        FROM community_thread_user
) s(u) WHERE u IS NOT NULL;

DELETE FROM community_trust WHERE user_id NOT IN (SELECT id FROM keep_user);

COMMIT;

\copy (SELECT id FROM keep_user ORDER BY id) TO 'kun_community_users.csv'
