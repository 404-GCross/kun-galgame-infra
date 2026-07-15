-- kun_images scrub — server-side scratch DB only.
-- The PII survey found exactly one sensitive column in this otherwise
-- content-addressed database: images.first_uploader_ip. Everything else (hash,
-- storage_key, thumbhash) is public content addressing.
--
-- No psql variables required.

\set ON_ERROR_STOP on

BEGIN;

UPDATE images
  SET first_uploader_ip = ''
  WHERE first_uploader_ip IS NOT NULL AND first_uploader_ip <> '';

COMMIT;
