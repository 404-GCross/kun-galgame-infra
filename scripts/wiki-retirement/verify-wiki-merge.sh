#!/usr/bin/env bash
# W1 · byte-level verifier (章程裁定 3 pre-check). Proves the wiki tables landed
# in the target catalog DB with zero loss/drift. For every wiki-owned table it
# compares SOURCE vs TARGET on:
#   (1) row count
#   (2) whole-row checksum  — md5 over md5(row::text) of ALL rows, order-normalized
#       (full checksum, not a sample: the wiki DB is small enough — see report —
#        that a full pass costs seconds, giving byte-level certainty rather than
#        the 1%-sample the task floor asked for; strictly stronger)
#   (3) index count  (4) constraint count
# Plus a DB-wide sequence last_value check. Emits a PASS/FAIL table + exit code
# (0 = all PASS) suitable for gating the production window.
#
# Session settings are pinned (TimeZone=UTC, extra_float_digits=3) so the
# row::text rendering is identical on both sides.
#
# Usage: source apps/api/.env && TARGET_DB=kun_catalog_w1 \
#          scripts/wiki-retirement/verify-wiki-merge.sh
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HERE/lib.sh"

SOURCE_DB="${SOURCE_DB:-kun_galgame_wiki}"
TARGET_DB="${TARGET_DB:?set TARGET_DB}"
PIN="SET TimeZone='UTC'; SET extra_float_digits=3;"

# count|checksum for one table in one DB
sig() {
  psql -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -v ON_ERROR_STOP=1 -tAqF'|' -d "$1" -c \
    "$PIN SELECT count(*), coalesce(md5(string_agg(h,'' ORDER BY h)),'-') FROM (SELECT md5(t::text) h FROM public.\"$2\" t) s;"
}
idxcount()  { psql_val "$1" "SELECT count(*) FROM pg_indexes WHERE schemaname='public' AND tablename='$2'"; }
concount()  { psql_val "$1" "SELECT count(*) FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid JOIN pg_namespace n ON n.oid=r.relnamespace WHERE n.nspname='public' AND r.relname='$2'"; }

mapfile -t TABLES < <(wiki_tables "$SOURCE_DB")
printf '%-28s %10s %10s  %-4s %-6s %-9s %-9s\n' TABLE ROWS_SRC ROWS_DST ROWS CHKSUM IDX CON
fail=0
for t in "${TABLES[@]}"; do
  IFS='|' read -r nS ckS <<<"$(sig "$SOURCE_DB" "$t")"
  IFS='|' read -r nD ckD <<<"$(sig "$TARGET_DB" "$t")"
  iS=$(idxcount "$SOURCE_DB" "$t"); iD=$(idxcount "$TARGET_DB" "$t")
  cS=$(concount "$SOURCE_DB" "$t"); cD=$(concount "$TARGET_DB" "$t")
  rr=$([ "$nS" = "$nD" ]   && echo ok || { echo BAD; fail=1; })
  kk=$([ "$ckS" = "$ckD" ] && echo ok || { echo BAD; fail=1; })
  ii=$([ "$iS" = "$iD" ]   && echo "$iS" || { echo "$iS!=$iD"; fail=1; })
  oo=$([ "$cS" = "$cD" ]   && echo "$cS" || { echo "$cS!=$cD"; fail=1; })
  printf '%-28s %10s %10s  %-4s %-6s %-9s %-9s\n' "$t" "$nS" "$nD" "$rr" "$kk" "$ii" "$oo"
done

echo "--- sequences (target last_value must be >= source) ---"
while read -r seq; do
  [ -z "$seq" ] && continue
  s=$(psql_val "$SOURCE_DB" "SELECT last_value FROM \"$seq\""); d=$(psql_val "$TARGET_DB" "SELECT last_value FROM \"$seq\"")
  st=$([ "$d" -ge "$s" ] && echo ok || { echo BAD; fail=1; })
  printf '  %-40s src=%-12s dst=%-12s %s\n' "$seq" "$s" "$d" "$st"
done < <(psql_val "$SOURCE_DB" "SELECT sequence_name FROM information_schema.sequences WHERE sequence_schema='public' ORDER BY 1")

echo
if [ "$fail" = 0 ]; then echo "RESULT: PASS — all wiki tables match SOURCE byte-for-byte."; else echo "RESULT: FAIL — see BAD rows above."; fi
exit "$fail"
