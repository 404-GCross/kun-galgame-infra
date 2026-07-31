package creditsplit

// Migrate mode pays off the debt the split leaves behind (wave 163,
// refs/proj/163-anchor-debt-and-deferred-trios.md).
//
// A split deletes the false source anchor and nothing else. The credit rows
// that source wrote stay on the original credit_name — and importers resolve a
// credit name by ANCHOR, never by name string, so the next import of the
// detached source id mints a FRESH credit_name for it and writes that source's
// credits a second time. The work page then shows the same staff member twice.
//
// Migrate does now, deliberately, what the importer would do later by accident:
//
//   - mint the credit_name the importer would mint (the SOURCE-side spelling,
//     which is not always the merged row's spelling — the import-era merge was
//     by folded name, so 'U'/'u', 'Q'/'Ｑ', '高濱 亮'/'高濱亮' all landed on one
//     row) and hang the detached anchor on it;
//   - re-point that source's credit rows from the old name to the new one;
//   - leave person_id NULL on the new row — the split's whole finding is that
//     this source is not that person.
//
// The attribution predicate is credit.source_id: every importer writes
// SourceID = its own source and resolves credit_name through that source's
// anchor map (importer.go materialize / dlsite.go / eg.go), so
// (credit_name_id, source_id) identifies exactly the rows one source wrote —
// PROVIDED the name carries at most one anchor of that source, which is
// guarded per row below. Evidence: refs/proj/163-artifacts/DESIGN.md.
//
// Idempotent: a second --apply finds the anchor already minted and no credit
// row left to move, and writes nothing at all.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"

	"gorm.io/datatypes"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// MigrateRow is one line of the migrate worklist: one anchor that wave 156b
// deleted, plus the name the source itself uses for it.
type MigrateRow struct {
	// CreditNameID is the row the anchor was detached FROM.
	CreditNameID int64  `json:"credit_name_id"`
	Source       string `json:"source"`
	ExternalID   string `json:"external_id"`
	// Name is the SOURCE-side spelling (dlsite product_json creaters.name /
	// erogamespace creaters.raw->>'name' / the vndb-created row's own name),
	// not necessarily the spelling of the row it was merged into.
	Name string `json:"name"`
	Lang string `json:"lang,omitempty"`
	// MatchedBy is the importer rule the original anchor carried; the re-minted
	// anchor keeps it so provenance reads the same as an importer's own row.
	MatchedBy string `json:"matched_by,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// MigrateStats reports a migrate run. The decided counters (Would*) are
// identical in dry and apply.
type MigrateStats struct {
	Rows           int
	Skipped        int // anchor already in place and no credit row left to move
	Refused        int
	WouldMint      int
	WouldMoveCredz int
	Minted         int
	Reused         int
	CreditsMoved   int
	Revisions      int
	Refusals       []Refusal
	Receipts       []MigrateReceipt
}

// MigrateReceipt records what one row did (or would do), so the move can be
// undone by hand from the file alone.
type MigrateReceipt struct {
	CreditNameID   int64   `json:"credit_name_id"`
	Source         string  `json:"source"`
	ExternalID     string  `json:"external_id"`
	Name           string  `json:"name"`
	ToCreditNameID int64   `json:"to_credit_name_id"`
	Minted         bool    `json:"minted"`
	CreditsMoved   int     `json:"credits_moved"`
	CreditIDs      []int64 `json:"credit_ids,omitempty"`
}

// LoadMigrateWorklist reads and validates the migrate worklist. Two rows for
// one anchor, or two rows minting the same (source, external_id), are refused
// at load: the second would decide against a state the first already changed.
func LoadMigrateWorklist(path string) ([]MigrateRow, error) {
	if path == "" {
		return nil, fmt.Errorf("--worklist is required")
	}
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	var out []MigrateRow
	seen := map[string]int{}
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 1<<16), 8<<20)
	for line := 1; sc.Scan(); line++ {
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var r MigrateRow
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		if r.CreditNameID <= 0 {
			return nil, fmt.Errorf("%s:%d: credit_name_id must be a positive id", path, line)
		}
		r.Source = strings.ToLower(strings.TrimSpace(r.Source))
		r.ExternalID = strings.TrimSpace(r.ExternalID)
		if r.Source == "" || r.ExternalID == "" {
			return nil, fmt.Errorf("%s:%d: source and external_id are required — the anchor is what is being moved", path, line)
		}
		if strings.TrimSpace(r.Name) == "" {
			return nil, fmt.Errorf("%s:%d: name is required — the minted row must carry the source's own spelling, never a guess", path, line)
		}
		key := r.Source + "\x00" + r.ExternalID
		if prev, dup := seen[key]; dup {
			return nil, fmt.Errorf("%s:%d: anchor %s:%s already listed on line %d", path, line, r.Source, r.ExternalID, prev)
		}
		seen[key] = line
		out = append(out, r)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no rows", path)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreditNameID != out[j].CreditNameID {
			return out[i].CreditNameID < out[j].CreditNameID
		}
		return out[i].Source < out[j].Source
	})
	return out, nil
}

// WriteMigrateReceipts persists the per-row receipts.
func WriteMigrateReceipts(path string, receipts []MigrateReceipt) error {
	if path == "" {
		return nil
	}
	fh, err := os.Create(path)
	if err != nil {
		return err
	}
	defer fh.Close()
	w := bufio.NewWriter(fh)
	for _, r := range receipts {
		raw, err := json.Marshal(r)
		if err != nil {
			return err
		}
		if _, err := w.Write(append(raw, '\n')); err != nil {
			return err
		}
	}
	return w.Flush()
}

// RunMigrate executes the migrate worklist against the catalog.
func RunMigrate(ctx context.Context, opts Opts) (*MigrateStats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn); refusing to guess")
	}
	rows, err := LoadMigrateWorklist(opts.WorklistPath)
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
	st := &MigrateStats{Rows: len(rows)}
	for _, r := range rows {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err := migrateOne(ctx, db, sources, r, opts, st); err != nil {
			return nil, err
		}
	}
	slog.Info("credit-split migrate done", "apply", opts.Apply, "rows", st.Rows,
		"skipped", st.Skipped, "refused", st.Refused, "would_mint", st.WouldMint,
		"would_move_credits", st.WouldMoveCredz, "minted", st.Minted, "reused", st.Reused,
		"credits_moved", st.CreditsMoved, "revisions", st.Revisions)
	return st, nil
}

// creditKey is the uq_catalog_credit key: (work_id, role_id, character_id or 0).
type creditKey struct {
	WorkID, RoleID, CharID int64
}

type creditRow struct {
	ID     int64  `gorm:"column:id"`
	WorkID int64  `gorm:"column:work_id"`
	RoleID int64  `gorm:"column:role_id"`
	CharID *int64 `gorm:"column:character_id"`
}

func (c creditRow) key() creditKey {
	k := creditKey{WorkID: c.WorkID, RoleID: c.RoleID}
	if c.CharID != nil {
		k.CharID = *c.CharID
	}
	return k
}

func migrateOne(ctx context.Context, db *gorm.DB, sources map[string]int16, r MigrateRow, opts Opts, st *MigrateStats) error {
	refuse := func(reason string) {
		st.Refused++
		st.Refusals = append(st.Refusals, Refusal{r.CreditNameID, reason})
	}
	srcID, ok := sources[r.Source]
	if !ok {
		refuse(fmt.Sprintf("unknown source key %q", r.Source))
		return nil
	}
	var from model.CatalogCreditName
	if err := db.WithContext(ctx).First(&from, r.CreditNameID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			refuse("credit_name no longer exists (merged away?)")
			return nil
		}
		return err
	}
	// The whole predicate rests on this: if the row still carries an anchor of
	// this source, credit.source_id cannot tell that source's rows apart from
	// the detached id's rows. Refuse rather than move the wrong credits.
	var stillAnchored int64
	if err := db.WithContext(ctx).Model(&model.CatalogExternalRef{}).
		Where("entity_type = ? AND entity_id = ? AND source_id = ?",
			model.EntityTypeCreditName, from.ID, srcID).Count(&stillAnchored).Error; err != nil {
		return err
	}
	if stillAnchored > 0 {
		refuse(fmt.Sprintf("credit_name %d still carries a %s anchor — source-scoped attribution would be ambiguous", from.ID, r.Source))
		return nil
	}

	// Has an importer (or an earlier pass of this wave) already re-minted the
	// detached id? Then reuse that row — never mint a competing anchor.
	var existing model.CatalogExternalRef
	err := db.WithContext(ctx).Where("entity_type = ? AND source_id = ? AND external_id = ? AND link_kind = ?",
		model.EntityTypeCreditName, srcID, r.ExternalID, model.LinkKindExact).First(&existing).Error
	reuse := err == nil
	if err != nil && err != gorm.ErrRecordNotFound {
		return err
	}
	if reuse && existing.EntityID == from.ID {
		refuse("the detached anchor is back on the original row — refusing to move credits onto itself")
		return nil
	}

	var moving []creditRow
	if err := db.WithContext(ctx).Model(&model.CatalogCredit{}).
		Select("id, work_id, role_id, character_id").
		Where("credit_name_id = ? AND source_id = ?", from.ID, srcID).
		Order("id").Scan(&moving).Error; err != nil {
		return err
	}
	if reuse && len(moving) > 0 {
		// Moving onto an existing row can collide with uq_catalog_credit. A
		// collision means the duplicate this wave exists to prevent already
		// happened; deleting rows is not this tool's mandate, so refuse the row
		// and let it be adjudicated.
		var have []creditRow
		if err := db.WithContext(ctx).Model(&model.CatalogCredit{}).
			Select("id, work_id, role_id, character_id").
			Where("credit_name_id = ?", existing.EntityID).Scan(&have).Error; err != nil {
			return err
		}
		occupied := make(map[creditKey]bool, len(have))
		for _, c := range have {
			occupied[c.key()] = true
		}
		var clash int
		for _, c := range moving {
			if occupied[c.key()] {
				clash++
			}
		}
		if clash > 0 {
			refuse(fmt.Sprintf("%d of %d credit rows already exist on credit_name %d (uq_catalog_credit) — the duplicate is already live, adjudicate by hand",
				clash, len(moving), existing.EntityID))
			return nil
		}
	}
	if reuse && len(moving) == 0 {
		st.Skipped++
		return nil
	}
	if !reuse {
		st.WouldMint++
	} else {
		st.Reused++
	}
	st.WouldMoveCredz += len(moving)

	rec := MigrateReceipt{
		CreditNameID: from.ID, Source: r.Source, ExternalID: r.ExternalID, Name: r.Name,
		Minted: !reuse, CreditsMoved: len(moving),
	}
	for _, c := range moving {
		rec.CreditIDs = append(rec.CreditIDs, c.ID)
	}
	if reuse {
		rec.ToCreditNameID = existing.EntityID
	}
	if !opts.Apply {
		st.Receipts = append(st.Receipts, rec)
		return nil
	}

	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		target := existing.EntityID
		if !reuse {
			minted := model.CatalogCreditName{
				Name: r.Name, Lang: r.Lang, Kind: model.CreditNameKindMain,
				LinkVisibility: model.LinkVisibilityPublic,
				// person_id stays NULL on purpose: the split's finding is that
				// this source is NOT the person the old row is linked to.
			}
			if err := tx.Create(&minted).Error; err != nil {
				return err
			}
			matchedBy := r.MatchedBy
			if matchedBy == "" {
				matchedBy = "rule:wave163-anchor-debt"
			}
			if err := tx.Create(&model.CatalogExternalRef{
				EntityType: model.EntityTypeCreditName, EntityID: minted.ID,
				SourceID: srcID, ExternalID: r.ExternalID,
				LinkKind: model.LinkKindExact, MatchedBy: matchedBy,
			}).Error; err != nil {
				return err
			}
			snap, err := json.Marshal(map[string]any{"credit_name": minted, "from_credit_name_id": from.ID})
			if err != nil {
				return err
			}
			changed, err := json.Marshal(map[string]any{
				"wave": 163, "source": r.Source, "external_id": r.ExternalID,
				"detached_from": from.ID, "reason": r.Reason,
			})
			if err != nil {
				return err
			}
			rev, err := repository.NextRevision(tx, model.EntityTypeCreditName, minted.ID)
			if err != nil {
				return err
			}
			if err := tx.Create(&model.CatalogRevision{
				EntityType: model.EntityTypeCreditName, EntityID: minted.ID, Revision: rev,
				Action: model.RevisionActionCreated, Snapshot: datatypes.JSON(snap),
				ChangedFields: datatypes.JSON(changed), ActorID: opts.ActorID,
				Note: "wave 163 anchor-debt migrate: re-mint of the detached " + r.Source + " id",
			}).Error; err != nil {
				return err
			}
			st.Minted++
			st.Revisions++
			target = minted.ID
		}
		rec.ToCreditNameID = target

		if len(moving) > 0 {
			ids := make([]int64, 0, len(moving))
			for _, c := range moving {
				ids = append(ids, c.ID)
			}
			res := tx.Exec(`UPDATE catalog_credit SET credit_name_id = ?, updated_at = now() WHERE id IN ?`, target, ids)
			if res.Error != nil {
				return res.Error
			}
			st.CreditsMoved += int(res.RowsAffected)

			// The revision on the ORIGIN carries the pre-move state (the credit
			// ids and where they went), which is what makes the move reversible.
			snap, err := json.Marshal(map[string]any{"credit_name": from, "moved_credit_ids": ids})
			if err != nil {
				return err
			}
			changed, err := json.Marshal(map[string]any{
				"wave": 163, "source": r.Source, "external_id": r.ExternalID,
				"to_credit_name_id": target, "credits_moved": len(ids), "reason": r.Reason,
			})
			if err != nil {
				return err
			}
			rev, err := repository.NextRevision(tx, model.EntityTypeCreditName, from.ID)
			if err != nil {
				return err
			}
			if err := tx.Create(&model.CatalogRevision{
				EntityType: model.EntityTypeCreditName, EntityID: from.ID, Revision: rev,
				Action: model.RevisionActionSplit, Snapshot: datatypes.JSON(snap),
				ChangedFields: datatypes.JSON(changed), ActorID: opts.ActorID,
				Note: "wave 163 anchor-debt migrate: " + r.Source + " credits moved off this name",
			}).Error; err != nil {
				return err
			}
			st.Revisions++
		}
		return nil
	})
	if err != nil {
		return err
	}
	st.Receipts = append(st.Receipts, rec)
	return nil
}
