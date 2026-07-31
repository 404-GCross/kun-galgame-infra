// Package creditsplit undoes import-era identity contamination on
// catalog_credit_name (wave 156, refs/proj/156-p2b-adjudication.md).
//
// The 18,133 multi-source credit names were merged at import by EXACT NAME
// STRING, never by an identity judgement (wave 152 §5). Most of that is
// correct, but a measured slice is not: a bare surname (井上, 原田) or a
// two-character handle collects several different people's source ids under one
// row. Wave 152 shortlisted 593 such rows; wave 156 adjudicates them and hands
// the confirmed ones here.
//
// What a split does, and deliberately nothing else:
//
//   - DELETE the et=1 external_ref rows of the sources that do not belong on
//     this name (the false identity claim), recording every deleted row in a
//     receipts file so the operation is reversible by hand;
//   - NULL catalog_credit_name.person_id if the row carried one — the person
//     link was inferred from an anchor set now known to be mixed;
//   - write one action=split revision per changed row, so the entity history
//     carries the pre-split snapshot.
//
// What it does NOT touch: catalog_credit (a credit says "this NAME had this
// role on this work", which stays true whichever person the name is), aliases,
// redirects, and every anchor of the sources that keep the name.
//
// Existing machinery was surveyed first: MergeService.Unmerge only reverses a
// RECORDED merge proposal, and no proposal exists for import-era merges, so it
// cannot be used here.
//
// Idempotent: a second --apply finds the anchors already gone and person_id
// already NULL, so it writes nothing (and adds no revision).
package creditsplit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"

	"gorm.io/datatypes"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Opts configures a run. Apply=false is the dry forecast; DSN is REQUIRED and
// never defaulted (the rehearsal copy locally, the live catalog only in the
// acceptance run).
type Opts struct {
	Apply        bool
	DSN          string
	WorklistPath string
	ReceiptsPath string
	ActorID      *int64
	Limit        int
}

// Stats reports the run. The decided counters are identical in dry and apply.
type Stats struct {
	Rows            int
	Skipped         int // nothing left to do (already split, or never anchored)
	Refused         int // guard tripped — reported, never forced
	WouldDropAnchor int
	WouldUnlink     int
	AnchorsDropped  int
	PersonsUnlinked int
	Revisions       int
	Refusals        []Refusal
	Receipts        []Receipt
}

// Refusal is a worklist row the guards rejected.
type Refusal struct {
	CreditNameID int64  `json:"credit_name_id"`
	Reason       string `json:"reason"`
}

// Receipt records exactly what one split removed, so it can be put back by
// hand. It is written even in a dry run (as the forecast).
type Receipt struct {
	CreditNameID   int64    `json:"credit_name_id"`
	Name           string   `json:"name"`
	DetachSources  []string `json:"detach_sources"`
	DroppedAnchors []Anchor `json:"dropped_anchors"`
	PersonUnlinked *int64   `json:"person_unlinked,omitempty"`
}

// Anchor is one deleted external_ref row.
type Anchor struct {
	SourceID   int16  `json:"source_id"`
	SourceKey  string `json:"source_key"`
	ExternalID string `json:"external_id"`
	LinkKind   int16  `json:"link_kind"`
	MatchedBy  string `json:"matched_by"`
}

// Run executes the split worklist against the catalog.
func Run(ctx context.Context, opts Opts) (*Stats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn); refusing to guess")
	}
	rows, err := LoadWorklist(opts.WorklistPath)
	if err != nil {
		return nil, err
	}
	if opts.Limit > 0 && opts.Limit < len(rows) {
		rows = rows[:opts.Limit]
	}
	db, err := gorm.Open(postgres.Open(opts.DSN), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		return nil, fmt.Errorf("connect catalog db: %w", err)
	}
	if sqlDB, e := db.DB(); e == nil {
		defer sqlDB.Close()
	}
	sources, err := loadSources(db)
	if err != nil {
		return nil, err
	}

	st := &Stats{Rows: len(rows)}
	for _, r := range rows {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err := splitOne(ctx, db, sources, r, opts, st); err != nil {
			return nil, err
		}
	}
	slog.Info("credit-split done", "apply", opts.Apply, "rows", st.Rows,
		"skipped", st.Skipped, "refused", st.Refused,
		"would_drop_anchor", st.WouldDropAnchor, "would_unlink", st.WouldUnlink,
		"anchors_dropped", st.AnchorsDropped, "persons_unlinked", st.PersonsUnlinked,
		"revisions", st.Revisions)
	return st, nil
}

// splitOne decides and (in apply) performs one row, in a single transaction.
func splitOne(ctx context.Context, db *gorm.DB, sources map[string]int16, r Row, opts Opts, st *Stats) error {
	wanted := make([]int16, 0, len(r.DetachSources))
	for _, key := range r.DetachSources {
		id, ok := sources[key]
		if !ok {
			st.Refused++
			st.Refusals = append(st.Refusals, Refusal{r.CreditNameID, fmt.Sprintf("unknown source key %q", key)})
			return nil
		}
		wanted = append(wanted, id)
	}
	sort.Slice(wanted, func(i, j int) bool { return wanted[i] < wanted[j] })

	var cn model.CatalogCreditName
	if err := db.WithContext(ctx).First(&cn, r.CreditNameID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			st.Refused++
			st.Refusals = append(st.Refusals, Refusal{r.CreditNameID, "credit_name no longer exists (merged away?)"})
			return nil
		}
		return err
	}
	var refs []model.CatalogExternalRef
	if err := db.WithContext(ctx).Where("entity_type = ? AND entity_id = ?",
		model.EntityTypeCreditName, r.CreditNameID).Order("source_id, external_id").Find(&refs).Error; err != nil {
		return err
	}

	var drop []model.CatalogExternalRef
	for _, ref := range refs {
		for _, id := range wanted {
			if ref.SourceID == id {
				drop = append(drop, ref)
			}
		}
	}
	// A split that takes every anchor off the row leaves a name with no source
	// at all — that is not a split, it is a deletion, and it means the worklist
	// row is wrong. Refuse rather than perform it.
	if len(drop) > 0 && len(drop) == len(refs) {
		st.Refused++
		st.Refusals = append(st.Refusals, Refusal{r.CreditNameID,
			"detaching every anchor would leave the name sourceless — refusing"})
		return nil
	}
	unlink := cn.PersonID != nil
	if len(drop) == 0 && !unlink {
		st.Skipped++
		return nil
	}
	st.WouldDropAnchor += len(drop)
	if unlink {
		st.WouldUnlink++
	}

	rec := Receipt{CreditNameID: cn.ID, Name: cn.Name, DetachSources: r.DetachSources}
	for _, ref := range drop {
		rec.DroppedAnchors = append(rec.DroppedAnchors, Anchor{
			SourceID: ref.SourceID, SourceKey: sourceKey(sources, ref.SourceID),
			ExternalID: ref.ExternalID, LinkKind: ref.LinkKind, MatchedBy: ref.MatchedBy,
		})
	}
	if unlink {
		rec.PersonUnlinked = cn.PersonID
	}
	st.Receipts = append(st.Receipts, rec)
	if !opts.Apply {
		return nil
	}

	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// The snapshot is taken BEFORE the mutation: the revision must carry the
		// pre-split state, which is what makes the operation reversible.
		snap, err := json.Marshal(map[string]any{
			"credit_name": cn,
			"anchors":     refs,
		})
		if err != nil {
			return err
		}
		for _, ref := range drop {
			res := tx.Exec(`DELETE FROM catalog_external_ref
			                 WHERE entity_type = ? AND entity_id = ? AND source_id = ? AND external_id = ?`,
				model.EntityTypeCreditName, cn.ID, ref.SourceID, ref.ExternalID)
			if res.Error != nil {
				return res.Error
			}
			st.AnchorsDropped += int(res.RowsAffected)
		}
		if unlink {
			res := tx.Exec(`UPDATE catalog_credit_name SET person_id = NULL, updated_at = now()
			                 WHERE id = ? AND person_id IS NOT NULL`, cn.ID)
			if res.Error != nil {
				return res.Error
			}
			st.PersonsUnlinked += int(res.RowsAffected)
		}
		changed, err := json.Marshal(map[string]any{
			"detached_sources": r.DetachSources,
			"person_id":        unlink,
			"reason":           r.Reason,
		})
		if err != nil {
			return err
		}
		rev, err := repository.NextRevision(tx, model.EntityTypeCreditName, cn.ID)
		if err != nil {
			return err
		}
		if err := tx.Create(&model.CatalogRevision{
			EntityType:    model.EntityTypeCreditName,
			EntityID:      cn.ID,
			Revision:      rev,
			Action:        model.RevisionActionSplit,
			Snapshot:      datatypes.JSON(snap),
			ChangedFields: datatypes.JSON(changed),
			ActorID:       opts.ActorID,
			Note:          "wave 156 credit-name split: " + r.Reason,
		}).Error; err != nil {
			return err
		}
		st.Revisions++
		return nil
	})
}

func loadSources(db *gorm.DB) (map[string]int16, error) {
	var rows []model.CatalogSource
	if err := db.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]int16, len(rows))
	for _, r := range rows {
		out[r.Key] = r.ID
	}
	// The wave's artefacts spell erogamespace "eg" throughout (wave 152/154).
	if id, ok := out["erogamespace"]; ok {
		out["eg"] = id
	}
	if id, ok := out["bangumi"]; ok {
		out["bgm"] = id
	}
	return out, nil
}

func sourceKey(sources map[string]int16, id int16) string {
	for k, v := range sources {
		if v == id && k != "eg" && k != "bgm" {
			return k
		}
	}
	return fmt.Sprintf("source#%d", id)
}
