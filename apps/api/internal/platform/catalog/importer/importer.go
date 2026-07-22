// Package importer lands the first real entity-layer data — persons (as
// credit names), characters and credit edges — from Bangumi and erogamespace
// into the catalog Gold tables, under the doc-10 doctrine:
//
//   - Orphan-credit-name-first (invariant 2): every source person becomes ONE
//     catalog_credit_name with person_id=NULL plus a SELF-REFERENTIAL exact
//     anchor (rule:<source>-person-import). Person rows are never created and
//     name→person links are never written — that is a human-review step under
//     the §10 visibility policy. (Cross-source "same person" only ever becomes
//     a probable match_candidate, never an automatic merge.)
//   - Identity-gated: the Bangumi wave imports every work with an EXACT
//     Bangumi anchor (step 69 widened it from the step-12 bid-audit pass
//     layer); the EG wave only the eg-vndb-rosetta works.
//   - Whole-source rollback: every credit carries source_id, so an entire
//     source's import can be reverted cheaply.
//
// The self anchor is NOT a cross-source identity claim (those stay probable):
// it asserts only "this credit name is source X's entity Y", a first-party
// structural fact (R8 tier-0), which is what makes re-runs idempotent.
package importer

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"api/internal/platform/catalog/model"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

const (
	bangumiSource int16 = 3
	dlsiteSource  int16 = 4
	egSource      int16 = 5

	ruleBangumiPerson = "rule:bangumi-person-import"
	ruleBangumiChar   = "rule:bangumi-character-import"
	ruleEGCreater     = "rule:eg-creater-import"
	ruleEGChar        = "rule:eg-character-import"
	ruleDLsiteWork    = "rule:dlsite-work-import"
	ruleDLsiteMaker   = "rule:dlsite-maker-import"
	ruleDLsiteCreater = "rule:dlsite-creater-import"
	ruleBangumiXmedia = "rule:bangumi-xmedia-import"

	roleVoiceActor int64 = 1 // hand-pinned in seed (声優)
	mediumASMR     int16 = 5 // catalog_medium key=asmr
)

// Options configures an import run.
type Options struct {
	Source string // "bangumi" | "eg" | "all"
	DryRun bool
	Limit  int // cap works processed per wave (0 = all)
	// ResolveAmbiguous turns on the step-29 three-layer handling of dlsite_ids
	// claimed by several EG games (default off = skip them, step-28 behavior).
	ResolveAmbiguous bool
	// ConflictsOut, when set, writes the B3 conflict worklist (a dlsite_id whose
	// claimants resolve to several different wiki works) to this TSV path.
	ConflictsOut string
}

// Stats is the eight-item per-wave tally.
type Stats struct {
	NamesCreated        int
	LabelsCreated       int
	CharactersCreated   int
	CreditsWritten      int
	SkippedUnmappedRole int
	SkippedGate         int
	Already             int
	Errors              int
}

func (s *Stats) add(o Stats) {
	s.NamesCreated += o.NamesCreated
	s.LabelsCreated += o.LabelsCreated
	s.CharactersCreated += o.CharactersCreated
	s.CreditsWritten += o.CreditsWritten
	s.SkippedUnmappedRole += o.SkippedUnmappedRole
	s.SkippedGate += o.SkippedGate
	s.Already += o.Already
	s.Errors += o.Errors
}

// Importer holds the catalog + eg handles and the preloaded anchor maps.
type Importer struct {
	catalog          *gorm.DB
	eg               *gorm.DB
	dryRun           bool
	limit            int
	resolveAmbiguous bool
	conflictsOut     string
}

func New(catalog, eg *gorm.DB, opts Options) *Importer {
	return &Importer{
		catalog: catalog, eg: eg, dryRun: opts.DryRun, limit: opts.Limit,
		resolveAmbiguous: opts.ResolveAmbiguous, conflictsOut: opts.ConflictsOut,
	}
}

// Run dispatches the requested wave(s).
func (im *Importer) Run(source string) (Stats, error) {
	var total Stats
	if source == "bangumi" || source == "all" {
		s, err := im.runBangumi()
		if err != nil {
			return total, fmt.Errorf("bangumi wave: %w", err)
		}
		total.add(s)
	}
	if source == "eg" || source == "all" {
		if im.eg == nil {
			return total, fmt.Errorf("eg wave requested but no erogamespace connection")
		}
		s, err := im.runEG()
		if err != nil {
			return total, fmt.Errorf("eg wave: %w", err)
		}
		total.add(s)
	}
	if source == "vndb" || source == "all" {
		s, err := im.runVNDBCredits()
		if err != nil {
			return total, fmt.Errorf("vndb credits wave: %w", err)
		}
		total.add(s)
	}
	return total, nil
}

// loadEGRosettaWorkMap returns EG game id → catalog work id for the
// eg-vndb-rosetta probable refs (the EG identity gate).
func (im *Importer) loadEGRosettaWorkMap() (map[int64]int64, error) {
	var rows []struct {
		ExternalID int64 `gorm:"column:external_id"`
		WorkID     int64 `gorm:"column:work_id"`
	}
	if err := im.catalog.Raw(`
		SELECT external_id::bigint AS external_id, entity_id AS work_id
		FROM catalog_external_ref WHERE matched_by = 'rule:eg-vndb-rosetta'`).Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[int64]int64, len(rows))
	for _, r := range rows {
		m[r.ExternalID] = r.WorkID
	}
	return m, nil
}

// anchorKey identifies a source entity: source|external_id.
func anchorKey(source int16, extID string) string { return fmt.Sprintf("%d|%s", source, extID) }

// loadAnchors preloads (source|external_id) → entity_id for one entity type,
// scoped to the two import sources — the resume/dedup index.
func (im *Importer) loadAnchors(entityType int16) (map[string]int64, error) {
	var rows []struct {
		SourceID   int16  `gorm:"column:source_id"`
		ExternalID string `gorm:"column:external_id"`
		EntityID   int64  `gorm:"column:entity_id"`
	}
	if err := im.catalog.Raw(
		`SELECT source_id, external_id, entity_id FROM catalog_external_ref
		 WHERE entity_type = ? AND source_id IN (?, ?, ?)`, entityType, bangumiSource, dlsiteSource, egSource,
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[string]int64, len(rows))
	for _, r := range rows {
		m[anchorKey(r.SourceID, r.ExternalID)] = r.EntityID
	}
	return m, nil
}

// roleMap loads source_role → role_id for one source (bangumi source_role is
// "type:position"; eg is the shubetu integer as text).
func (im *Importer) roleMap(source int16) (map[string]int64, error) {
	var rows []struct {
		SourceRole string `gorm:"column:source_role"`
		RoleID     int64  `gorm:"column:role_id"`
	}
	if err := im.catalog.Raw(`SELECT source_role, role_id FROM catalog_source_role_map WHERE source_id = ?`, source).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[string]int64, len(rows))
	for _, r := range rows {
		m[r.SourceRole] = r.RoleID
	}
	return m, nil
}

// nameItem / labelItem / charItem are new entities queued for creation.
type nameItem struct{ extID, name, lang string }
type labelItem struct {
	extID, name, lang string
	kind              int16
}
type charItem struct{ extID, name, lang string }

func entitySnapshot(kind string, entity any) datatypes.JSON {
	b, _ := json.Marshal(map[string]any{kind: entity, "aliases": []any{}})
	return b
}

// batchRefsRevs writes the self exact anchors + action=imported revisions
// (revision 1 — these entities are brand new).
func (im *Importer) batchRefsRevs(tx *gorm.DB, refs []model.CatalogExternalRef, revs []model.CatalogRevision) error {
	if err := tx.CreateInBatches(refs, 1000).Error; err != nil {
		return err
	}
	return tx.CreateInBatches(revs, 1000).Error
}

// createCreditNames creates orphan (person_id NULL) credit names + self anchors
// + imported revisions, returning extID → new id.
func (im *Importer) createCreditNames(tx *gorm.DB, source int16, rule string, items []nameItem) (map[string]int64, error) {
	out := make(map[string]int64, len(items))
	if len(items) == 0 {
		return out, nil
	}
	rows := make([]model.CatalogCreditName, len(items))
	for i, it := range items {
		rows[i] = model.CatalogCreditName{Name: it.name, Lang: it.lang, Kind: model.CreditNameKindMain, LinkVisibility: model.LinkVisibilityPublic}
	}
	if err := tx.CreateInBatches(rows, 1000).Error; err != nil {
		return nil, err
	}
	refs := make([]model.CatalogExternalRef, len(rows))
	revs := make([]model.CatalogRevision, len(rows))
	for i, r := range rows {
		out[items[i].extID] = r.ID
		refs[i] = selfRef(model.EntityTypeCreditName, r.ID, source, items[i].extID, rule)
		revs[i] = importedRev(model.EntityTypeCreditName, r.ID, entitySnapshot("credit_name", r))
	}
	return out, im.batchRefsRevs(tx, refs, revs)
}

func (im *Importer) createLabels(tx *gorm.DB, source int16, rule string, items []labelItem) (map[string]int64, error) {
	out := make(map[string]int64, len(items))
	if len(items) == 0 {
		return out, nil
	}
	rows := make([]model.CatalogLabel, len(items))
	for i, it := range items {
		rows[i] = model.CatalogLabel{DisplayName: it.name, Lang: it.lang, Kind: it.kind}
	}
	if err := tx.CreateInBatches(rows, 1000).Error; err != nil {
		return nil, err
	}
	refs := make([]model.CatalogExternalRef, len(rows))
	revs := make([]model.CatalogRevision, len(rows))
	for i, r := range rows {
		out[items[i].extID] = r.ID
		refs[i] = selfRef(model.EntityTypeLabel, r.ID, source, items[i].extID, rule)
		revs[i] = importedRev(model.EntityTypeLabel, r.ID, entitySnapshot("label", r))
	}
	return out, im.batchRefsRevs(tx, refs, revs)
}

func (im *Importer) createCharacters(tx *gorm.DB, source int16, rule string, items []charItem) (map[string]int64, error) {
	out := make(map[string]int64, len(items))
	if len(items) == 0 {
		return out, nil
	}
	rows := make([]model.CatalogCharacter, len(items))
	for i, it := range items {
		rows[i] = model.CatalogCharacter{DisplayName: it.name, Lang: it.lang}
	}
	if err := tx.CreateInBatches(rows, 1000).Error; err != nil {
		return nil, err
	}
	refs := make([]model.CatalogExternalRef, len(rows))
	revs := make([]model.CatalogRevision, len(rows))
	for i, r := range rows {
		out[items[i].extID] = r.ID
		refs[i] = selfRef(model.EntityTypeCharacter, r.ID, source, items[i].extID, rule)
		revs[i] = importedRev(model.EntityTypeCharacter, r.ID, entitySnapshot("character", r))
	}
	return out, im.batchRefsRevs(tx, refs, revs)
}

func selfRef(etype int16, id int64, source int16, extID, rule string) model.CatalogExternalRef {
	return model.CatalogExternalRef{
		EntityType: etype, EntityID: id, SourceID: source, ExternalID: extID,
		LinkKind: model.LinkKindExact, MatchedBy: rule,
	}
}

func importedRev(etype int16, id int64, snap datatypes.JSON) model.CatalogRevision {
	return model.CatalogRevision{
		EntityType: etype, EntityID: id, Revision: 1,
		Action: model.RevisionActionImported, Snapshot: snap, IsMinor: false,
	}
}

// insertCredits batch-inserts credit edges with ON CONFLICT DO NOTHING on the
// doc-10 expression unique (work, credit_name, role, COALESCE(character,0)) —
// a raw insert because GORM's OnConflict cannot target an expression index.
// Returns the number actually written.
func (im *Importer) insertCredits(tx *gorm.DB, credits []model.CatalogCredit) (int, error) {
	written := 0
	const batch = 1000
	for start := 0; start < len(credits); start += batch {
		end := min(start+batch, len(credits))
		var sb strings.Builder
		sb.WriteString(`INSERT INTO catalog_credit (work_id, credit_name_id, label_id, role_id, character_id, spoiler, note, source_id, created_at, updated_at) VALUES `)
		args := make([]any, 0, (end-start)*8)
		for i := start; i < end; i++ {
			c := credits[i]
			if i > start {
				sb.WriteString(",")
			}
			sb.WriteString("(?,?,?,?,?,?,?,?,now(),now())")
			args = append(args, c.WorkID, c.CreditNameID, c.LabelID, c.RoleID, c.CharacterID, c.Spoiler, c.Note, c.SourceID)
		}
		sb.WriteString(` ON CONFLICT (work_id, credit_name_id, role_id, COALESCE(character_id, 0)) DO NOTHING`)
		res := tx.Exec(sb.String(), args...)
		if res.Error != nil {
			return written, res.Error
		}
		written += int(res.RowsAffected)
	}
	return written, nil
}

// resolver looks up a source external id → entity id, preferring the
// freshly-created ids over the preloaded anchors.
func resolver(anchors map[string]int64, source int16, fresh map[string]int64) func(string) (int64, bool) {
	return func(ext string) (int64, bool) {
		if id, ok := fresh[ext]; ok {
			return id, true
		}
		if id, ok := anchors[anchorKey(source, ext)]; ok && id != 0 {
			return id, true
		}
		return 0, false
	}
}

// materialize turns credit plans into rows, resolving every source ext id to a
// catalog entity id. A plan whose credit name (or required character) does not
// resolve is dropped and counted (should be 0 — every person/character is
// created or already anchored).
func materialize(plans []creditPlan, cn, label, char func(string) (int64, bool), source int16) ([]model.CatalogCredit, int) {
	src := source
	out := make([]model.CatalogCredit, 0, len(plans))
	dropped := 0
	for _, p := range plans {
		cnID, ok := cn(p.cnExtID)
		if !ok {
			dropped++
			continue
		}
		c := model.CatalogCredit{
			WorkID: p.workID, CreditNameID: cnID, RoleID: p.roleID,
			Spoiler: model.SpoilerNone, Note: p.note, SourceID: &src,
		}
		if p.labelExt != "" {
			if lid, ok := label(p.labelExt); ok {
				c.LabelID = &lid
			}
		}
		if p.charExtID != "" {
			chid, ok := char(p.charExtID)
			if !ok {
				dropped++
				continue
			}
			c.CharacterID = &chid
		}
		out = append(out, c)
	}
	return out, dropped
}

func int64Keys(m map[int64]int64) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// capMap deterministically keeps the first n entries by ascending key (the
// --limit debugging aid).
func capMap(m map[int64]int64, n int) map[int64]int64 {
	keys := int64Keys(m)
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	if n < len(keys) {
		keys = keys[:n]
	}
	out := make(map[int64]int64, len(keys))
	for _, k := range keys {
		out[k] = m[k]
	}
	return out
}
