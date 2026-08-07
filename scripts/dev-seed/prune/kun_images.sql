-- Prune kun_images to a small metadata sample. Image BYTES live in prod
-- MinIO/CDN and are not part of any seed, so content-side hash references
-- dangle in dev no matter what we keep — the sample exists to exercise the
-- image admin console and the usage bookkeeping, not to resolve every hash.

BEGIN;

TRUNCATE image_moderation_queue;

CREATE TEMP TABLE keep_image (id bigint PRIMARY KEY, hash text);
INSERT INTO keep_image
SELECT id, hash FROM images ORDER BY id DESC LIMIT 2000;

DELETE FROM image_site_usage WHERE hash NOT IN (SELECT hash FROM keep_image);
DELETE FROM images           WHERE id   NOT IN (SELECT id   FROM keep_image);

COMMIT;
