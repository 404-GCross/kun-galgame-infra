-- kun_news scrub — server-side scratch DB only.
-- The news corpus itself is upstream public reporting and stays verbatim; the
-- only operator-authored free text is the moderation reason, which gets the
-- same fidelity downgrade as kun_community's flag notes (裁定 1b).
--
-- Expected psql variables: marker  (e.g. -v marker="[dev-scrubbed]")

\set ON_ERROR_STOP on

BEGIN;

UPDATE news_moderation_decision
  SET reason = :'marker' || ' synthetic moderation reason.'
  WHERE reason <> '';

COMMIT;
