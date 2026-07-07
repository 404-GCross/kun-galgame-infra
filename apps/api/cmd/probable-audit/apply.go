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

	"api/internal/platform/catalog/repository"
	"api/internal/platform/catalog/service"
)

// receiptRow is one filled worklist line: the ref it addresses, the stratum it
// was drawn from (for the precision report), and the reviewer's verdict.
type receiptRow struct {
	Stratum    string
	SourceID   int16
	ExternalID string
	WorkID     int64
	Decision   string // ok | wrong | unsure | "" (skip)
	Notes      string
}

func (r receiptRow) refKey() service.RefKey {
	return service.RefKey{EntityType: entityTypeWork, EntityID: r.WorkID, SourceID: r.SourceID, ExternalID: r.ExternalID}
}

const entityTypeWork int16 = 5

// parseReceipt reads a filled worklist TSV, keyed by header name so column
// order is not load-bearing.
func parseReceipt(path string) ([]receiptRow, error) {
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
	for _, req := range []string{"source_id", "external_id", "work_id", "decision"} {
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
		src, _ := strconv.ParseInt(get(cols, "source_id"), 10, 16)
		wid, _ := strconv.ParseInt(get(cols, "work_id"), 10, 64)
		rows = append(rows, receiptRow{
			Stratum: get(cols, "stratum"), SourceID: int16(src),
			ExternalID: get(cols, "external_id"), WorkID: wid,
			Decision: strings.ToLower(get(cols, "decision")), Notes: get(cols, "notes"),
		})
	}
	return rows, sc.Err()
}

type applyStats struct {
	OKConfirmed int
	WrongReject int
	Unsure      int
	Skipped     int // blank decision
	AlreadyDone int // ref no longer probable / already gone (idempotent re-run)
	ExactTaken  int // ok lost the exact slot to another entity
	Unknown     int // unrecognized decision token
}

// runApply processes a filled receipt. ok → ConfirmRef (probable→exact); wrong
// → RejectRef (delete + permanent match_rejection); unsure/blank → nothing. The
// two verdicts reuse the admin review-queue service verbatim, so a receipt is
// exactly equivalent to clicking the probable-ref bucket. Dry-run (the default)
// previews the plan by reading each ref's current state without writing.
func (a *auditor) runApply(ctx context.Context, path string, run bool, actor int64) error {
	rows, err := parseReceipt(path)
	if err != nil {
		return err
	}
	queues := a.adminQueue()

	var st applyStats
	for _, r := range rows {
		switch r.Decision {
		case "":
			st.Skipped++
			continue
		case "unsure":
			st.Unsure++
			continue
		case "ok", "wrong":
		default:
			st.Unknown++
			continue
		}

		if !run {
			a.previewOne(ctx, r, &st)
			continue
		}
		if r.Decision == "ok" {
			err = queues.ConfirmRef(ctx, r.refKey(), actor)
		} else {
			err = queues.RejectRef(ctx, r.refKey(), rejectReason(r), actor)
		}
		a.classifyApplyErr(r, err, &st)
	}

	mode := "DRY-RUN (nothing written; pass --run to apply)"
	if run {
		mode = "APPLIED"
	}
	fmt.Fprintf(os.Stderr,
		"%s — ok/confirmed=%d wrong/rejected=%d unsure=%d skipped=%d already=%d exact-taken=%d unknown=%d\n",
		mode, st.OKConfirmed, st.WrongReject, st.Unsure, st.Skipped, st.AlreadyDone, st.ExactTaken, st.Unknown)
	return nil
}

// previewOne reads a ref's current state so a dry-run reports what --run would
// do: a still-probable ref would be confirmed/rejected; anything else is a
// no-op (idempotent).
func (a *auditor) previewOne(ctx context.Context, r receiptRow, st *applyStats) {
	var kind *int16
	_ = a.catalog.WithContext(ctx).Raw(
		`SELECT link_kind FROM catalog_external_ref WHERE entity_type=? AND entity_id=? AND source_id=? AND external_id=?`,
		entityTypeWork, r.WorkID, r.SourceID, r.ExternalID).Scan(&kind).Error
	if kind == nil || *kind != 1 { // 1 = probable; anything else is already settled/gone
		st.AlreadyDone++
		return
	}
	if r.Decision == "ok" {
		st.OKConfirmed++
	} else {
		st.WrongReject++
	}
}

func (a *auditor) classifyApplyErr(r receiptRow, err error, st *applyStats) {
	if err == nil {
		if r.Decision == "ok" {
			st.OKConfirmed++
		} else {
			st.WrongReject++
		}
		return
	}
	switch {
	case stderrors.Is(err, service.ErrExactTaken):
		st.ExactTaken++
	case stderrors.Is(err, service.ErrNotFound), stderrors.Is(err, service.ErrProposalState):
		// ref already promoted / already gone → idempotent no-op.
		st.AlreadyDone++
	default:
		fmt.Fprintf(os.Stderr, "  ! %s ref (%d,%d,%q): %v\n", r.Decision, r.SourceID, r.WorkID, r.ExternalID, err)
		st.Unknown++
	}
}

// rejectReason keeps the reviewer's note (RejectRef requires a non-empty
// reason) and always tags the sample-audit provenance.
func rejectReason(r receiptRow) string {
	if r.Notes != "" {
		return "sample-audit: " + r.Notes
	}
	return "sample-audit: rejected as a wrong probable match"
}

// adminQueue builds the review-queue service exactly as cmd/catalog does, so
// receipt actions are byte-identical to admin clicks.
func (a *auditor) adminQueue() *service.AdminQueueService {
	resolve := service.NewResolveService(repository.NewRedirectRepository(a.catalog))
	merge := service.NewMergeService(a.catalog, resolve,
		repository.NewProposalRepository(a.catalog), repository.NewRevisionRepository(a.catalog))
	return service.NewAdminQueueService(a.catalog, merge)
}

// runReport summarizes a receipt: measured precision per stratum plus the
// mechanical consequences of each candidate batch policy. It states numbers,
// never a recommendation — the policy is the user's named decision.
func (a *auditor) runReport(w io.Writer, path string) error {
	rows, err := parseReceipt(path)
	if err != nil {
		return err
	}
	if err := a.loadAll(); err != nil {
		return err
	}

	// Per-stratum tallies from the receipt.
	type tally struct{ n, ok, wrong, unsure int }
	tallies := map[string]*tally{}
	order := []string{"rosetta", "ts-rosetta-corrob", "ts-bangumi-only", "contradiction"}
	for _, s := range order {
		tallies[s] = &tally{}
	}
	for _, r := range rows {
		t := tallies[r.Stratum]
		if t == nil {
			t = &tally{}
			tallies[r.Stratum] = t
			order = append(order, r.Stratum)
		}
		t.n++
		switch r.Decision {
		case "ok":
			t.ok++
		case "wrong":
			t.wrong++
		case "unsure":
			t.unsure++
		}
	}

	fmt.Fprintln(w, "## sample precision by stratum")
	fmt.Fprintf(w, "%-20s%8s%8s%8s%8s%12s\n", "stratum", "n", "ok", "wrong", "unsure", "precision")
	for _, s := range order {
		t := tallies[s]
		if t.n == 0 {
			continue
		}
		fmt.Fprintf(w, "%-20s%8d%8d%8d%8d%12s\n", s, t.n, t.ok, t.wrong, t.unsure, precision(t.ok, t.wrong))
	}

	// Population strata (the mechanical policy consequences).
	pop := map[string]int{}
	for _, r := range a.refs {
		pop[r.stratum()]++
	}
	fmt.Fprintln(w, "\n## policy consequence estimates (numbers only — the decision is yours)")
	fmt.Fprintf(w, "  promote ALL rosetta                → +%d exact\n", pop["rosetta"])
	fmt.Fprintf(w, "  promote title-strict (also-rosetta) → +%d exact\n", pop["ts-rosetta-corrob"])
	fmt.Fprintf(w, "  promote title-strict (bangumi-only) → +%d exact\n", pop["ts-bangumi-only"])
	fmt.Fprintf(w, "  reject ALL contradictions           → -%d refs, +%d rejections\n",
		pop["contradiction"], pop["contradiction"])
	fmt.Fprintln(w, "  (apply a promotion only after its stratum's measured precision clears your bar.)")
	return nil
}

func precision(ok, wrong int) string {
	d := ok + wrong
	if d == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.3f", float64(ok)/float64(d))
}
