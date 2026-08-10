package main

import (
	"bufio"
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/service"

	"gorm.io/gorm"
)

type receiptStats struct {
	Linked      int
	NeedsManual int
	Rejected    int
	Skipped     int
	Already     int
	NotFound    int
	Errors      int
	Unknown     int
}

type receiptRow struct {
	AID, BID int64
	Decision string
}

func runReceipts(ctx context.Context, db *gorm.DB, w io.Writer, path string, actor int64, apply bool) (receiptStats, error) {
	rows, err := parseReceipts(path)
	if err != nil {
		return receiptStats{}, err
	}
	queues := adminQueue(db)

	var st receiptStats
	for _, r := range rows {
		switch r.Decision {
		case "", "skip":
			st.Skipped++
			continue
		case "link", "reject":
		default:
			st.Unknown++
			continue
		}

		if !apply {
			st.tallyDryReceipt(db, ctx, r)
			continue
		}
		action := "accept"
		if r.Decision == "reject" {
			action = "reject"
		}
		outcome, err := queues.DecideCandidate(ctx, service.CandidateDecision{
			EntityType: model.EntityTypeCreditName, AID: r.AID, BID: r.BID,
			Action: action, DecidedBy: actor,
		})
		st.tallyReceipt(w, r, outcome, err)
	}

	mode := "DRY-RUN (nothing written; pass --run to apply)"
	if apply {
		mode = "APPLIED"
	}
	fmt.Fprintf(w, "%s — linked=%d needs_manual=%d rejected=%d skipped=%d already=%d not_found=%d errors=%d unknown=%d\n",
		mode, st.Linked, st.NeedsManual, st.Rejected, st.Skipped, st.Already, st.NotFound, st.Errors, st.Unknown)
	return st, nil
}

func (st *receiptStats) tallyReceipt(w io.Writer, r receiptRow, outcome *service.CandidateOutcome, err error) {
	switch {
	case err == nil && r.Decision == "reject":
		st.Rejected++
	case err == nil && outcome != nil && outcome.Link != nil && outcome.Link.NeedsManual:
		st.NeedsManual++
	case err == nil:
		st.Linked++
	case stderrors.Is(err, service.ErrProposalState):
		st.Already++
	case stderrors.Is(err, service.ErrNotFound):
		st.NotFound++
	default:
		st.Errors++
		fmt.Fprintf(w, "  ! %s (%d,%d): %v\n", r.Decision, r.AID, r.BID, err)
	}
}

func (st *receiptStats) tallyDryReceipt(db *gorm.DB, ctx context.Context, r receiptRow) {
	var row struct {
		Status  int16  `gorm:"column:status"`
		APerson *int64 `gorm:"column:ap"`
		BPerson *int64 `gorm:"column:bp"`
	}
	err := db.WithContext(ctx).Raw(`SELECT c.status, a.person_id AS ap, b.person_id AS bp
		FROM catalog_match_candidate c
		JOIN catalog_credit_name a ON a.id = c.a_id
		JOIN catalog_credit_name b ON b.id = c.b_id
		WHERE c.entity_type = ? AND c.a_id = ? AND c.b_id = ?`,
		model.EntityTypeCreditName, r.AID, r.BID).Scan(&row).Error
	if err != nil || (row.Status == 0 && row.APerson == nil && row.BPerson == nil && !candidateExists(db, ctx, r)) {
		st.NotFound++
		return
	}
	if row.Status != model.CandidateStatusPending {
		st.Already++
		return
	}
	if r.Decision == "reject" {
		st.Rejected++
		return
	}
	if predictOutcome(row.APerson, row.BPerson) == "needs_manual" {
		st.NeedsManual++
		return
	}
	st.Linked++
}

func candidateExists(db *gorm.DB, ctx context.Context, r receiptRow) bool {
	var n int64
	_ = db.WithContext(ctx).Raw(`SELECT count(*) FROM catalog_match_candidate
		WHERE entity_type = ? AND a_id = ? AND b_id = ?`,
		model.EntityTypeCreditName, r.AID, r.BID).Scan(&n).Error
	return n > 0
}

func parseReceipts(path string) ([]receiptRow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	if !sc.Scan() {
		return nil, fmt.Errorf("empty receipt file")
	}
	idx := map[string]int{}
	for i, h := range strings.Split(sc.Text(), "\t") {
		idx[strings.TrimSpace(h)] = i
	}
	for _, req := range []string{"a_id", "b_id", "decision"} {
		if _, ok := idx[req]; !ok {
			return nil, fmt.Errorf("receipt missing required column %q", req)
		}
	}
	get := func(cols []string, name string) string {
		i, ok := idx[name]
		if !ok || i >= len(cols) {
			return ""
		}
		return strings.TrimSpace(cols[i])
	}

	var rows []receiptRow
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		cols := strings.Split(line, "\t")
		a, _ := strconv.ParseInt(get(cols, "a_id"), 10, 64)
		b, _ := strconv.ParseInt(get(cols, "b_id"), 10, 64)
		rows = append(rows, receiptRow{AID: a, BID: b, Decision: strings.ToLower(get(cols, "decision"))})
	}
	return rows, sc.Err()
}
