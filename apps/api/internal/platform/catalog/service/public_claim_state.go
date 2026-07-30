// public_claim_state.go — the SQL side of the PUBLIC claim vocabulary
// (none|live|draft|hidden), A2-R4.
//
// model.ClaimStateKey is the single semantic source for that vocabulary: the
// read face renders it as claimed_by.state and the reindexer bakes it into the
// works index as the filterable claim_state field. A Go function cannot run
// inside a WHERE clause, though, so a face that filters IN THE DATABASE — the
// works LIST — needs the same projection expressed as a predicate. This file is
// that one expression, kept alone so there is a single place to audit, and
// pinned row-for-row against model.ClaimStateKey by
// TestClaimStateWhereMatchesProjection: a future edit to either side that
// forgets the other fails the suite instead of silently publishing drafts.
package service

import (
	"strings"

	"api/internal/platform/catalog/model"
)

// claimedSQL is the registry's "a product site owns this row" predicate — the
// first half of model.ClaimStateKey. The product_work_id half is not pedantry:
// claimed_by is null without one (there is nothing to point at), so such a row
// reads as unclaimed on the wire and must filter as unclaimed too.
const claimedSQL = "(w.site IS NOT NULL AND w.site <> '' AND w.product_work_id IS NOT NULL)"

// claimStateWhere compiles a claim_state= filter into ONE SQL predicate plus its
// bind args; an empty state list returns an empty predicate, i.e. no gate at all
// (absent means every state, so pre-existing callers stay byte-identical).
//
// The four branches mirror model.ClaimStateKey exactly:
//
//	none   → NOT claimed
//	live   → claimed AND (claim_state IS NULL OR claim_state = 0)
//	draft  → claimed AND claim_state = 1
//	hidden → claimed AND claim_state IS NOT NULL AND claim_state NOT IN (0, 1)
//
// The NULL column reads as `live` because that is how every consumer treated a
// claim before the column existed (zero-regression semantics), and `hidden`
// takes EVERYTHING else rather than just the literal 2 — the Go default branch's
// conservative choice, so an unrecognized state is never published. The four are
// therefore a partition of the table: each row satisfies exactly one, and a
// caller naming all four gets the ungated set back.
//
// Several states OR into one parenthesized group that ANDs with every other
// filter — the same one-door shape the search face's Meilisearch expression has,
// so total-style aggregates and rows can never end up behind different gates.
func claimStateWhere(states []string) (string, []any) {
	if len(states) == 0 {
		return "", nil
	}
	ors := make([]string, 0, len(states))
	args := make([]any, 0, len(states)+1)
	for _, st := range states {
		switch st {
		case model.ClaimStateKeyNone:
			ors = append(ors, "NOT "+claimedSQL)
		case model.ClaimStateKeyLive:
			ors = append(ors, claimedSQL+" AND (w.claim_state IS NULL OR w.claim_state = ?)")
			args = append(args, model.ClaimStateLive)
		case model.ClaimStateKeyDraft:
			ors = append(ors, claimedSQL+" AND w.claim_state = ?")
			args = append(args, model.ClaimStateDraft)
		case model.ClaimStateKeyHidden:
			ors = append(ors, claimedSQL+" AND w.claim_state IS NOT NULL AND w.claim_state NOT IN (?, ?)")
			args = append(args, model.ClaimStateLive, model.ClaimStateDraft)
		default:
			// Unreachable — the handler 400s every token outside the vocabulary
			// before it gets here. Match nothing rather than widen the gate: a
			// state nobody understands must not be the one that leaks drafts.
			ors = append(ors, "false")
		}
	}
	return "((" + strings.Join(ors, ") OR (") + "))", args
}
