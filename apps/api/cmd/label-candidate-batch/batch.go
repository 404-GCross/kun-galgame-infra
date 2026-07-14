package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/service"

	"gorm.io/gorm"
)

const importNoteMark = "rule:galgame-official-import"

// pairRow is one pending label candidate with the classification evidence.
type pairRow struct {
	AID, BID       int64
	AName, BName   string
	AImport        bool `gorm:"column:a_import"`
	BImport        bool `gorm:"column:b_import"`
	SharedWorks    int  `gorm:"column:shared_works"`
	AWorks, BWorks int
}

// loadPending returns every still-pending entity_type=3 candidate with both
// names, the import-side marks and the shared-work count.
func loadPending(db *gorm.DB) ([]pairRow, error) {
	var rows []pairRow
	err := db.Raw(`SELECT c.a_id AS a_id, c.b_id AS b_id,
			la.display_name AS a_name, lb.display_name AS b_name,
			la.note LIKE '%` + importNoteMark + `%' AS a_import,
			lb.note LIKE '%` + importNoteMark + `%' AS b_import,
			(SELECT count(*) FROM catalog_work_label wa JOIN catalog_work_label wb ON wa.work_id = wb.work_id
			  WHERE wa.label_id = c.a_id AND wb.label_id = c.b_id) AS shared_works,
			(SELECT count(*) FROM catalog_work_label WHERE label_id = c.a_id) AS a_works,
			(SELECT count(*) FROM catalog_work_label WHERE label_id = c.b_id) AS b_works
		FROM catalog_match_candidate c
		JOIN catalog_label la ON la.id = c.a_id
		JOIN catalog_label lb ON lb.id = c.b_id
		WHERE c.status = 0 AND c.entity_type = 3
		ORDER BY c.a_id, c.b_id`).Scan(&rows).Error
	return rows, err
}

// direction fixes source/target by policy: the wiki-import label is absorbed
// into the pre-existing one. ok=false when the pair has no single import side
// (never seen in practice; such a pair goes to the judgment lane instead).
func direction(r pairRow) (source, target int64, ok bool) {
	switch {
	case r.BImport && !r.AImport:
		return r.BID, r.AID, true
	case r.AImport && !r.BImport:
		return r.AID, r.BID, true
	}
	return 0, 0, false
}

// acceptAndApprove drives the same two clicks an admin would make: accept the
// candidate (which opens the merge proposal) and approve the proposal (which
// starts the 48h cooling clock).
func acceptAndApprove(ctx context.Context, queue *service.AdminQueueService, merge *service.MergeService,
	r pairRow, source, target, actor int64, note string) error {
	outcome, err := queue.DecideCandidate(ctx, service.CandidateDecision{
		EntityType: model.EntityTypeLabel,
		AID:        r.AID, BID: r.BID,
		Action:   "accept",
		SourceID: source, TargetID: target,
		Note:      note,
		DecidedBy: actor,
	})
	if err != nil {
		return err
	}
	if outcome.Proposal == nil {
		return errors.New("accept produced no proposal")
	}
	return merge.ApproveMerge(ctx, outcome.Proposal.ID, actor)
}

// --- mechanical -------------------------------------------------------------

func runMechanical(ctx context.Context, db *gorm.DB, w io.Writer, actor int64, run bool) error {
	rows, err := loadPending(db)
	if err != nil {
		return err
	}
	queue, merge := services(db)
	var accepted, noDirection, judgment, errs int
	for _, r := range rows {
		if r.SharedWorks == 0 {
			judgment++
			continue
		}
		source, target, ok := direction(r)
		if !ok {
			fmt.Fprintf(w, "  (%d,%d) %q: shared=%d but no single import side — left for judgment\n", r.AID, r.BID, r.AName, r.SharedWorks)
			noDirection++
			continue
		}
		if !run {
			accepted++
			continue
		}
		note := fmt.Sprintf("%s shared_works=%d", noteTag, r.SharedWorks)
		if err := acceptAndApprove(ctx, queue, merge, r, source, target, actor, note); err != nil {
			fmt.Fprintf(w, "  (%d,%d) %q: ERROR %v\n", r.AID, r.BID, r.AName, err)
			errs++
			continue
		}
		accepted++
	}
	mode := "DRY-RUN (pass -run to accept+approve)"
	if run {
		mode = "APPLIED"
	}
	fmt.Fprintf(w, "%s [mechanical] pending=%d accepted+approved=%d no_direction=%d judgment_remainder=%d errors=%d\n",
		mode, len(rows), accepted, noDirection, judgment, errs)
	if errs > 0 {
		return fmt.Errorf("%d pairs failed", errs)
	}
	return nil
}

// --- receipts ---------------------------------------------------------------

type receiptRow struct {
	aID, bID, source, target int64
	verdict, reason          string
}

func runReceipts(ctx context.Context, db *gorm.DB, w io.Writer, path string, actor int64, run bool) error {
	rows, err := parseReceipts(path)
	if err != nil {
		return err
	}
	queue, merge := services(db)
	var merged, kept, unsure, errs int
	for _, r := range rows {
		switch r.verdict {
		case "merge":
			if !run {
				merged++
				continue
			}
			note := noteTag + " agent-adjudicated"
			if r.reason != "" {
				note += ": " + r.reason
			}
			if err := acceptAndApprove(ctx, queue, merge, pairRow{AID: r.aID, BID: r.bID}, r.source, r.target, actor, note); err != nil {
				fmt.Fprintf(w, "  (%d,%d) merge: ERROR %v\n", r.aID, r.bID, err)
				errs++
				continue
			}
			merged++
		case "keep":
			if !run {
				kept++
				continue
			}
			if _, err := queue.DecideCandidate(ctx, service.CandidateDecision{
				EntityType: model.EntityTypeLabel,
				AID:        r.aID, BID: r.bID,
				Action:    "reject",
				Note:      noteTag + " agent: " + r.reason,
				DecidedBy: actor,
			}); err != nil {
				fmt.Fprintf(w, "  (%d,%d) keep: ERROR %v\n", r.aID, r.bID, err)
				errs++
				continue
			}
			kept++
		case "unsure":
			unsure++ // stays pending for a human
		default:
			fmt.Fprintf(w, "  (%d,%d): unknown verdict %q — skipped\n", r.aID, r.bID, r.verdict)
			errs++
		}
	}
	mode := "DRY-RUN (pass -run to apply)"
	if run {
		mode = "APPLIED"
	}
	fmt.Fprintf(w, "%s [receipts] rows=%d merged=%d kept_separate=%d unsure_pending=%d errors=%d\n",
		mode, len(rows), merged, kept, unsure, errs)
	if errs > 0 {
		return fmt.Errorf("%d receipt rows failed", errs)
	}
	return nil
}

// parseReceipts reads the adjudicated TSV: a_id, b_id, source_id, target_id,
// verdict (merge/keep/unsure), reason.
func parseReceipts(path string) ([]receiptRow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var out []receiptRow
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first {
			first = false
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) < 5 {
			continue
		}
		a, err1 := strconv.ParseInt(cols[0], 10, 64)
		b, err2 := strconv.ParseInt(cols[1], 10, 64)
		s, err3 := strconv.ParseInt(cols[2], 10, 64)
		t, err4 := strconv.ParseInt(cols[3], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
			return nil, fmt.Errorf("bad receipt row: %q", line)
		}
		reason := ""
		if len(cols) > 5 {
			reason = strings.TrimSpace(cols[5])
		}
		out = append(out, receiptRow{aID: a, bID: b, source: s, target: t, verdict: strings.TrimSpace(cols[4]), reason: reason})
	}
	return out, sc.Err()
}

// --- execute ----------------------------------------------------------------

func runExecute(ctx context.Context, db *gorm.DB, w io.Writer, actor int64, limit int, run bool) error {
	var ids []int64
	q := `SELECT id FROM catalog_merge_proposal
		WHERE entity_type = ? AND status = ? AND execute_after <= now() AND note LIKE ?
		ORDER BY id`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	if err := db.Raw(q, model.EntityTypeLabel, model.ProposalStatusApproved, "%"+noteTag+"%").Scan(&ids).Error; err != nil {
		return err
	}
	_, merge := services(db)
	var executed, errs int
	for _, id := range ids {
		if !run {
			executed++
			continue
		}
		if err := merge.ExecuteMerge(ctx, id, &actor); err != nil {
			fmt.Fprintf(w, "  proposal %d: ERROR %v\n", id, err)
			errs++
			continue
		}
		executed++
	}
	mode := "DRY-RUN (pass -run to execute)"
	if run {
		mode = "APPLIED"
	}
	fmt.Fprintf(w, "%s [execute] cooled=%d executed=%d errors=%d\n", mode, len(ids), executed, errs)
	if errs > 0 {
		return fmt.Errorf("%d executions failed", errs)
	}
	return nil
}
