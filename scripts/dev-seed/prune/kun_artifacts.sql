-- Prune kun_artifacts to the newest few hundred artifact records. Like the
-- image seed, the underlying files stay in prod object storage; the rows are
-- enough to exercise listings and the manifest model.

BEGIN;

CREATE TEMP TABLE keep_artifact (id bigint PRIMARY KEY);
INSERT INTO keep_artifact
SELECT id FROM artifacts ORDER BY id DESC LIMIT :seed_works;

DELETE FROM manifests WHERE artifact_id NOT IN (SELECT id FROM keep_artifact);
DELETE FROM artifacts WHERE id NOT IN (SELECT id FROM keep_artifact);

COMMIT;
