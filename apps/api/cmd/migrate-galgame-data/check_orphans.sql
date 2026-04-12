-- Run against the kungal SOURCE database to find orphan records
-- Usage: psql -U postgres kungalgame -f check_orphans.sql

\echo '=== Orphan contributors (galgame_id not in galgame) ==='
SELECT c.id, c.galgame_id, c.user_id
FROM galgame_contributor c
WHERE NOT EXISTS (SELECT 1 FROM galgame g WHERE g.id = c.galgame_id)
ORDER BY c.id;

\echo ''
\echo '=== Orphan contributors (user_id not in user) ==='
SELECT c.id, c.galgame_id, c.user_id
FROM galgame_contributor c
WHERE NOT EXISTS (SELECT 1 FROM "user" u WHERE u.id = c.user_id)
ORDER BY c.id;

\echo ''
\echo '=== Orphan aliases ==='
SELECT a.id, a.galgame_id, a.name
FROM galgame_alias a
WHERE NOT EXISTS (SELECT 1 FROM galgame g WHERE g.id = a.galgame_id);

\echo ''
\echo '=== Orphan links ==='
SELECT l.id, l.galgame_id, l.name
FROM galgame_link l
WHERE NOT EXISTS (SELECT 1 FROM galgame g WHERE g.id = l.galgame_id);

\echo ''
\echo '=== Orphan tag relations ==='
SELECT r.galgame_id, r.tag_id
FROM galgame_tag_relation r
WHERE NOT EXISTS (SELECT 1 FROM galgame g WHERE g.id = r.galgame_id)
   OR NOT EXISTS (SELECT 1 FROM galgame_tag t WHERE t.id = r.tag_id);

\echo ''
\echo '=== Orphan official relations ==='
SELECT r.galgame_id, r.official_id
FROM galgame_official_relation r
WHERE NOT EXISTS (SELECT 1 FROM galgame g WHERE g.id = r.galgame_id)
   OR NOT EXISTS (SELECT 1 FROM galgame_official o WHERE o.id = r.official_id);

\echo ''
\echo '=== Orphan engine relations ==='
SELECT r.galgame_id, r.engine_id
FROM galgame_engine_relation r
WHERE NOT EXISTS (SELECT 1 FROM galgame g WHERE g.id = r.galgame_id)
   OR NOT EXISTS (SELECT 1 FROM galgame_engine e WHERE e.id = r.engine_id);

\echo ''
\echo '=== Summary ==='
SELECT 'contributors (no galgame)' AS type, COUNT(*) FROM galgame_contributor c WHERE NOT EXISTS (SELECT 1 FROM galgame g WHERE g.id = c.galgame_id)
UNION ALL
SELECT 'contributors (no user)', COUNT(*) FROM galgame_contributor c WHERE NOT EXISTS (SELECT 1 FROM "user" u WHERE u.id = c.user_id)
UNION ALL
SELECT 'aliases', COUNT(*) FROM galgame_alias a WHERE NOT EXISTS (SELECT 1 FROM galgame g WHERE g.id = a.galgame_id)
UNION ALL
SELECT 'links', COUNT(*) FROM galgame_link l WHERE NOT EXISTS (SELECT 1 FROM galgame g WHERE g.id = l.galgame_id)
UNION ALL
SELECT 'tag relations', COUNT(*) FROM galgame_tag_relation r WHERE NOT EXISTS (SELECT 1 FROM galgame g WHERE g.id = r.galgame_id) OR NOT EXISTS (SELECT 1 FROM galgame_tag t WHERE t.id = r.tag_id)
UNION ALL
SELECT 'official relations', COUNT(*) FROM galgame_official_relation r WHERE NOT EXISTS (SELECT 1 FROM galgame g WHERE g.id = r.galgame_id) OR NOT EXISTS (SELECT 1 FROM galgame_official o WHERE o.id = r.official_id)
UNION ALL
SELECT 'engine relations', COUNT(*) FROM galgame_engine_relation r WHERE NOT EXISTS (SELECT 1 FROM galgame g WHERE g.id = r.galgame_id) OR NOT EXISTS (SELECT 1 FROM galgame_engine e WHERE e.id = r.engine_id);
