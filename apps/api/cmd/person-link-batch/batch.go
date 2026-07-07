package main

import (
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"
	"api/internal/platform/catalog/service"

	"gorm.io/gorm"
)

// The two mechanical rules that make a shared-handle credit_name candidate
// auto-linkable (zero LLM). Both operate on the NFKC-normalized name (fetched
// via SQL, so fullwidth parens/comma are already ASCII — the same normalize()
// the Silver layer uses, no Go NFKC dependency):
//
//   - A1: the two names are literally identical after dropping any (...) segment
//     and collapsing whitespace/case — 緒方剛志 ↔ 緒方剛志(ぼうのうと).
//   - A2: one side's parenthetical alias list contains the other side's whole
//     folded name — ささきむつみ ↔ 藤宮博也(ささきむつみ). Whole-name, never a
//     substring, so 有限会社FAVORITE does NOT match FAVORITE (left for a human).
const (
	ruleA1 = "A1"
	ruleA2 = "A2"
	// A3/A4 clear the alias_declared candidates (step 25) under a second,
	// independent line of evidence beyond the alias declaration itself:
	//   - A3: the two names are co-credited on the SAME work (they collaborated).
	//   - A4: the declaration is bidirectional — each side's ingested aliases
	//     (catalog_name_alias, step 25) name the other whole.
	ruleA3 = "A3"
	ruleA4 = "A4"

	ruleSetShared = "shared" // A1/A2 over shared-handle candidates (default)
	ruleSetAlias  = "alias"  // A3/A4 over alias_declared candidates
)

// candidateRow is one pending shared-handle credit_name candidate with both
// names NFKC-normalized and their current person attachment (for the dry
// preview).
type candidateRow struct {
	AID     int64  `gorm:"column:a_id"`
	BID     int64  `gorm:"column:b_id"`
	AName   string `gorm:"column:an"`
	BName   string `gorm:"column:bn"`
	APerson *int64 `gorm:"column:ap"`
	BPerson *int64 `gorm:"column:bp"`
}

// loadCandidates reads the pending credit_name candidates of one reason in a
// deterministic order, with each name normalized by the exact Silver
// expression.
func loadCandidates(db *gorm.DB, reason int16) ([]candidateRow, error) {
	var rows []candidateRow
	err := db.Raw(`
		SELECT c.a_id, c.b_id,
		       normalize(a.name, NFKC) AS an, normalize(b.name, NFKC) AS bn,
		       a.person_id AS ap, b.person_id AS bp
		FROM catalog_match_candidate c
		JOIN catalog_credit_name a ON a.id = c.a_id
		JOIN catalog_credit_name b ON b.id = c.b_id
		WHERE c.entity_type = ? AND c.reason = ? AND c.status = ?
		ORDER BY c.a_id, c.b_id`,
		model.EntityTypeCreditName, reason, model.CandidateStatusPending,
	).Scan(&rows).Error
	return rows, err
}

// reasonForRuleSet maps a rule set to the candidate reason it clears.
func reasonForRuleSet(rs string) int16 {
	if rs == ruleSetAlias {
		return model.CandidateReasonAliasDeclared
	}
	return model.CandidateReasonSharedExternalID
}

// classify returns the rule that makes the pair auto-linkable ("" = neither).
func classify(anfkc, bnfkc string) string {
	af, bf := foldName(anfkc), foldName(bnfkc)
	if af != "" && af == bf {
		return ruleA1
	}
	if a2Match(af, bnfkc) || a2Match(bf, anfkc) {
		return ruleA2
	}
	return ""
}

// a2Match reports whether target (an already-folded base name) equals one of
// the folded alias items inside owner's parentheses.
func a2Match(target, ownerNFKC string) bool {
	return target != "" && slices.Contains(parenAliases(ownerNFKC), target)
}

// foldName is the comparison key: drop every (...) segment, remove whitespace,
// lowercase.
func foldName(nfkc string) string {
	return strings.ToLower(removeSpaces(stripParens(nfkc)))
}

// stripParens removes ASCII parenthetical segments (NFKC already folded
// fullwidth（）to ASCII). Unbalanced parens degrade gracefully.
func stripParens(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

func removeSpaces(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

// parenAliases returns the folded alias items inside every top-level (...)
// segment, split on the ideographic comma 、 (the only in-paren separator in
// the data; ASCII , is also honored defensively).
func parenAliases(nfkc string) []string {
	var items []string
	depth := 0
	var cur strings.Builder
	flush := func() {
		for _, part := range strings.FieldsFunc(cur.String(), func(r rune) bool { return r == '、' || r == ',' }) {
			if f := strings.ToLower(removeSpaces(part)); f != "" {
				items = append(items, f)
			}
		}
		cur.Reset()
	}
	for _, r := range nfkc {
		switch r {
		case '(':
			if depth == 0 {
				cur.Reset()
			}
			depth++
		case ')':
			if depth > 0 {
				depth--
				if depth == 0 {
					flush()
				}
			}
		default:
			if depth > 0 {
				cur.WriteRune(r)
			}
		}
	}
	return items
}

// batchStats is the four-way outcome tally (plus the rule split and misses).
type batchStats struct {
	A1Hits, A2Hits, A3Hits, A4Hits int
	LinkedCreated                  int
	LinkedAttached                 int
	NeedsManual                    int
	Already                        int
	Errors                         int
	Unmatched                      int
}

func (st *batchStats) hit(rule string) {
	switch rule {
	case ruleA1:
		st.A1Hits++
	case ruleA2:
		st.A2Hits++
	case ruleA3:
		st.A3Hits++
	case ruleA4:
		st.A4Hits++
	}
}

// run classifies every pending candidate of the rule set's reason and, for the
// mechanically-decidable hits, links it through the SAME path the admin bucket
// uses (DecideCandidate accept → step-22 three-state person link + candidate
// flip, one transaction). Unmatched candidates are left untouched — they are
// the human-review backlog (--export drafts a worklist for them). Dry-run
// predicts the outcome without writing.
func run(ctx context.Context, db *gorm.DB, w io.Writer, actor int64, apply bool, ruleSet string) (batchStats, error) {
	rows, err := loadCandidates(db, reasonForRuleSet(ruleSet))
	if err != nil {
		return batchStats{}, err
	}
	classify, err := buildClassifier(db, ruleSet, rows)
	if err != nil {
		return batchStats{}, err
	}
	queues := adminQueue(db)

	var st batchStats
	for _, r := range rows {
		rule := classify(r)
		if rule == "" {
			st.Unmatched++
			continue
		}
		st.hit(rule)

		if !apply {
			action := predictOutcome(r.APerson, r.BPerson)
			tallyPredicted(&st, action)
			fmt.Fprintf(w, "[%s] %-3s  %q ↔ %q\n", action, rule, r.AName, r.BName)
			continue
		}

		outcome, err := queues.DecideCandidate(ctx, service.CandidateDecision{
			EntityType: model.EntityTypeCreditName, AID: r.AID, BID: r.BID,
			Action: "accept", DecidedBy: actor,
		})
		switch {
		case err == nil && outcome.Link != nil && outcome.Link.NeedsManual:
			st.NeedsManual++
		case err == nil && outcome.Link != nil && outcome.Link.Created:
			st.LinkedCreated++
		case err == nil && outcome.Link != nil:
			st.LinkedAttached++
		case stderrors.Is(err, service.ErrProposalState):
			st.Already++ // already decided (idempotent re-run / raced)
		default:
			st.Errors++
			fmt.Fprintf(w, "  ! candidate (%d,%d): %v\n", r.AID, r.BID, err)
		}
	}

	mode := "DRY-RUN (nothing written; pass --run to link)"
	if apply {
		mode = "APPLIED"
	}
	fmt.Fprintf(w, "%s [%s] — A1=%d A2=%d A3=%d A4=%d | created=%d attached=%d needs_manual=%d already=%d errors=%d | unmatched=%d of %d\n",
		mode, ruleSet, st.A1Hits, st.A2Hits, st.A3Hits, st.A4Hits, st.LinkedCreated, st.LinkedAttached, st.NeedsManual, st.Already, st.Errors,
		st.Unmatched, len(rows))
	return st, nil
}

// buildClassifier returns the rule classifier for the rule set. The shared set
// judges the two names purely (A1/A2); the alias set consults the DB for a
// second line of evidence (A3 co-credit, A4 bidirectional declaration).
func buildClassifier(db *gorm.DB, ruleSet string, rows []candidateRow) (func(candidateRow) string, error) {
	if ruleSet != ruleSetAlias {
		return func(r candidateRow) string { return classify(r.AName, r.BName) }, nil
	}
	return aliasClassifier(db, rows)
}

// predictOutcome mirrors the three-state rule for the dry preview, from the
// two names' current person attachment.
func predictOutcome(a, b *int64) string {
	switch {
	case a != nil && b != nil:
		if *a == *b {
			return "already"
		}
		return "needs_manual"
	case a != nil || b != nil:
		return "attach"
	default:
		return "create"
	}
}

func tallyPredicted(st *batchStats, action string) {
	switch action {
	case "create":
		st.LinkedCreated++
	case "attach":
		st.LinkedAttached++
	case "needs_manual":
		st.NeedsManual++
	case "already":
		st.Already++
	}
}

// adminQueue builds the review-queue service exactly as cmd/catalog does, so
// the batch link is byte-identical to an admin "confirm same person" click.
func adminQueue(db *gorm.DB) *service.AdminQueueService {
	resolve := service.NewResolveService(repository.NewRedirectRepository(db))
	merge := service.NewMergeService(db, resolve,
		repository.NewProposalRepository(db), repository.NewRevisionRepository(db))
	return service.NewAdminQueueService(db, merge)
}
