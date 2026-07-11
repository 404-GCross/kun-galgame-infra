package catalogsync

import (
	"encoding/json"
	"fmt"
	"strings"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"

	"gorm.io/gorm"
)

// Official→Label registration wave (doc 10 §11 step 1, C-04). Every wiki
// galgame_official (the galgame domain's brand/会社 layer) becomes a first-party
// catalog Label, and every already-claimed galgame gains a Brand attribution
// edge (catalog_work_label kind=Brand).
//
// Register, don't assert (design correction, doc 10 §11): the wiki officials
// carry NO external identity (no VNDB pid — only official-site URLs and plain
// names), so this wave REGISTERS them as first-party labels rather than
// asserting a cross-source identity. Name collisions with existing labels (the
// Bangumi/DLsite waves already minted company/circle labels — Key, TYPE-MOON …
// very likely exist) are surfaced as match_candidates for human review; nothing
// is ever auto-merged (R8: name equality is candidate-only, a wrong auto-equate
// poisons the whole graph).
//
// Dependency direction: this is a PRODUCT-domain package importing the
// PLATFORM-domain catalog (galgame → catalog is allowed; the reverse is not).
// Driven only by cmd/register-galgame-officials, never by the running API.
//
// Idempotency key = galgame_official.catalog_label_id IS NULL: a mapped official
// is never re-minted, so a second full pass writes nothing.

// ruleOfficialImport tags every label this wave mints, recorded in
// catalog_label.note (the labels carry no external_ref to hold a matched_by, so
// the note is the traceable/revocable provenance channel — `note` is internal,
// never surfaced in the read projection). The bidirectional link lives in
// galgame_official.catalog_label_id.
const ruleOfficialImport = "rule:galgame-official-import"

// officialChunk batches label mints / edge upserts per statement.
const officialChunk = 1000

// officialLabelKind maps a galgame_official.category to a catalog Label kind.
// The wiki's three curated categories: company/individual are commercial or
// solo makers → game brand (ブランド); amateur is a doujin/同人 maker → circle.
// (The wiki has no developer/publisher split, so the work attribution edge is
// uniformly WorkLabelKindBrand regardless of this label kind.)
func officialLabelKind(category string) int16 {
	if category == "amateur" {
		return model.LabelKindDoujinCircle
	}
	return model.LabelKindGameBrand // company, individual, and any unexpected value
}

// labelLang falls back to ja for an official with no recorded language (the
// import convention across the Bangumi/DLsite label waves).
func labelLang(lang string) string {
	if strings.TrimSpace(lang) == "" {
		return "ja"
	}
	return lang
}

// OfficialRegistrar registers wiki officials into catalog labels and builds the
// Brand attribution edges. wiki and catalog may be the SAME *gorm.DB (the
// integration test co-locates both schemas, as CI does).
type OfficialRegistrar struct {
	wiki    *gorm.DB
	catalog *gorm.DB
	dryRun  bool
	limit   int
}

// NewOfficialRegistrar wires a registrar. limit caps the officials minted per
// run (0 = all); a debugging aid.
func NewOfficialRegistrar(wiki, catalog *gorm.DB, dryRun bool, limit int) *OfficialRegistrar {
	return &OfficialRegistrar{wiki: wiki, catalog: catalog, dryRun: dryRun, limit: limit}
}

// OfficialStats is the wave tally. Identity (full run): Officials = Minted +
// Already; Relations = EdgesWritten + EdgesAlready + EdgesSkippedUnclaimed.
type OfficialStats struct {
	Officials             int // total galgame_official rows
	Minted                int // labels newly created this run (catalog_label_id was NULL)
	Already               int // officials already mapped at run start
	AliasesWritten        int // catalog_label_alias rows inserted
	NameCandidates        int // catalog_match_candidate rows inserted (name-norm collisions)
	EdgesWritten          int // catalog_work_label rows inserted (dry: would-write)
	EdgesAlready          int // brand edges already present (apply only)
	EdgesSkippedUnclaimed int // relations whose galgame is not a claimed catalog_work
}

// officialRow is one to-mint official with its NFKC-lower norms computed by the
// SAME Postgres expression as the catalog generated norm columns (byte-
// isomorphic collision detection).
type officialRow struct {
	ID           int64  `gorm:"column:id"`
	Name         string `gorm:"column:name"`
	Original     string `gorm:"column:original"`
	Lang         string `gorm:"column:lang"`
	Category     string `gorm:"column:category"`
	NameNorm     string `gorm:"column:name_norm"`
	OriginalNorm string `gorm:"column:original_norm"`
}

// officialAliasRow is one galgame_official_alias with its NFKC-lower norm.
type officialAliasRow struct {
	OfficialID int64  `gorm:"column:galgame_official_id"`
	Name       string `gorm:"column:name"`
	Norm       string `gorm:"column:name_norm"`
}

// Run executes the registration wave and returns the aggregated stats.
func (r *OfficialRegistrar) Run() (OfficialStats, error) {
	var st OfficialStats
	if err := r.wiki.Raw(`SELECT count(*) FROM galgame_official`).Scan(&st.Officials).Error; err != nil {
		return st, fmt.Errorf("count officials: %w", err)
	}
	if err := r.wiki.Raw(`SELECT count(*) FROM galgame_official WHERE catalog_label_id IS NOT NULL`).
		Scan(&st.Already).Error; err != nil {
		return st, fmt.Errorf("count mapped officials: %w", err)
	}
	claimed, err := repository.LoadClaimedWorkIDs(r.catalog, siteGalgame)
	if err != nil {
		return st, fmt.Errorf("load claimed works: %w", err)
	}
	// Existing-label norm index — built BEFORE minting so it reflects only
	// pre-wave labels (a fresh mint is never a candidate against itself).
	normIndex, err := r.loadLabelNormIndex()
	if err != nil {
		return st, fmt.Errorf("load label norm index: %w", err)
	}
	toMint, err := r.loadUnmapped()
	if err != nil {
		return st, fmt.Errorf("load unmapped officials: %w", err)
	}
	aliasIdx, err := r.loadAliases()
	if err != nil {
		return st, fmt.Errorf("load official aliases: %w", err)
	}
	st.Minted = len(toMint)

	if r.dryRun {
		for _, o := range toMint {
			st.AliasesWritten += len(aliasSpecsFor(o, aliasIdx[o.ID]))
		}
		if err := r.writeCandidates(toMint, aliasIdx, nil, normIndex, &st); err != nil {
			return st, fmt.Errorf("plan candidates: %w", err)
		}
		if err := r.writeEdges(claimed, &st); err != nil {
			return st, fmt.Errorf("plan edges: %w", err)
		}
		return st, nil
	}

	labelID, err := r.mint(toMint, aliasIdx, &st)
	if err != nil {
		return st, fmt.Errorf("mint labels: %w", err)
	}
	if err := r.writeCandidates(toMint, aliasIdx, labelID, normIndex, &st); err != nil {
		return st, fmt.Errorf("write candidates: %w", err)
	}
	if err := r.writeEdges(claimed, &st); err != nil {
		return st, fmt.Errorf("write edges: %w", err)
	}
	return st, nil
}

// loadUnmapped returns the officials still needing a label (the idempotency
// key), NFKC-lower normalized by the same expression as the catalog norm
// columns. --limit caps the slice (deterministic by id).
func (r *OfficialRegistrar) loadUnmapped() ([]officialRow, error) {
	q := `SELECT id, name, coalesce(original,'') AS original, coalesce(lang,'') AS lang, category,
	             lower(normalize(name, NFKC)) AS name_norm,
	             lower(normalize(coalesce(original,''), NFKC)) AS original_norm
	      FROM galgame_official WHERE catalog_label_id IS NULL ORDER BY id`
	if r.limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", r.limit)
	}
	var rows []officialRow
	return rows, r.wiki.Raw(q).Scan(&rows).Error
}

// loadAliases indexes every galgame_official_alias by official id (only ~1.7k
// rows total — loaded whole, cheaper than an IN over the mint set).
func (r *OfficialRegistrar) loadAliases() (map[int64][]officialAliasRow, error) {
	var rows []officialAliasRow
	if err := r.wiki.Raw(`SELECT galgame_official_id, name, lower(normalize(name, NFKC)) AS name_norm
	                      FROM galgame_official_alias WHERE name <> ''`).Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[int64][]officialAliasRow, len(rows))
	for _, a := range rows {
		m[a.OfficialID] = append(m[a.OfficialID], a)
	}
	return m, nil
}

// loadLabelNormIndex builds norm → existing label ids from both the label
// display names and their aliases (the RHS "both sides' aliases" half).
func (r *OfficialRegistrar) loadLabelNormIndex() (map[string][]int64, error) {
	idx := map[string][]int64{}
	var labels []struct {
		ID   int64  `gorm:"column:id"`
		Norm string `gorm:"column:display_name_norm"`
	}
	if err := r.catalog.Raw(`SELECT id, display_name_norm FROM catalog_label WHERE display_name_norm <> ''`).
		Scan(&labels).Error; err != nil {
		return nil, err
	}
	for _, l := range labels {
		idx[l.Norm] = append(idx[l.Norm], l.ID)
	}
	var aliases []struct {
		LabelID int64  `gorm:"column:label_id"`
		Norm    string `gorm:"column:name_norm"`
	}
	if err := r.catalog.Raw(`SELECT label_id, name_norm FROM catalog_label_alias WHERE name_norm <> ''`).
		Scan(&aliases).Error; err != nil {
		return nil, err
	}
	for _, a := range aliases {
		idx[a.Norm] = appendUnique(idx[a.Norm], a.LabelID)
	}
	return idx, nil
}

// mint creates the labels + aliases + imported revisions per chunk (catalog tx),
// then writes the label id back onto the wiki official rows. Returns official
// id → new label id. Mirrors the DLsite/Bangumi label-mint path (direct model
// creates + action=imported revision in one transaction — the accepted importer
// form; the revision is never bypassed).
func (r *OfficialRegistrar) mint(toMint []officialRow, aliasIdx map[int64][]officialAliasRow, st *OfficialStats) (map[int64]int64, error) {
	labelIDByOfficial := make(map[int64]int64, len(toMint))
	for start := 0; start < len(toMint); start += officialChunk {
		end := min(start+officialChunk, len(toMint))
		chunk := toMint[start:end]
		labels := make([]model.CatalogLabel, len(chunk))
		var aliases []model.CatalogLabelAlias
		var revs []model.CatalogRevision
		err := r.catalog.Transaction(func(tx *gorm.DB) error {
			for i, o := range chunk {
				labels[i] = model.CatalogLabel{
					DisplayName: o.Name, Lang: labelLang(o.Lang),
					Kind: officialLabelKind(o.Category), Note: ruleOfficialImport,
				}
			}
			if err := tx.CreateInBatches(labels, officialChunk).Error; err != nil {
				return err
			}
			for i, o := range chunk {
				lid := labels[i].ID
				la := aliasRowsFor(o, aliasIdx[o.ID], lid)
				aliases = append(aliases, la...)
				revs = append(revs, importedLabelRev(lid, labels[i], la))
			}
			if len(aliases) > 0 {
				if err := tx.CreateInBatches(aliases, officialChunk).Error; err != nil {
					return err
				}
			}
			return tx.CreateInBatches(revs, officialChunk).Error
		})
		if err != nil {
			return nil, err
		}
		// Cross-DB write-back: catalog and wiki are separate databases in
		// production, so the mapping column cannot ride the catalog tx. Done
		// per chunk to keep the (minted-but-unmapped) crash window small.
		if err := r.writeBack(chunk, labels); err != nil {
			return nil, err
		}
		for i, o := range chunk {
			labelIDByOfficial[o.ID] = labels[i].ID
		}
		st.AliasesWritten += len(aliases)
	}
	return labelIDByOfficial, nil
}

// writeBack stamps galgame_official.catalog_label_id for one minted chunk in a
// single UPDATE ... FROM (VALUES ...) statement.
func (r *OfficialRegistrar) writeBack(chunk []officialRow, labels []model.CatalogLabel) error {
	if len(chunk) == 0 {
		return nil
	}
	var sb strings.Builder
	sb.WriteString("UPDATE galgame_official AS o SET catalog_label_id = v.lid FROM (VALUES ")
	args := make([]any, 0, len(chunk)*2)
	for i := range chunk {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString("(?::bigint,?::bigint)")
		args = append(args, chunk[i].ID, labels[i].ID)
	}
	sb.WriteString(") AS v(oid, lid) WHERE o.id = v.oid")
	return r.wiki.Exec(sb.String(), args...).Error
}

// --- helpers ---------------------------------------------------------------

// aliasSpec is one deduped label alias to write.
type aliasSpec struct{ name, lang, norm string }

// aliasSpecsFor builds the catalog_label_alias specs for one official: its
// wiki alias rows (lang unknown = the empty string) plus its `original`
// native name (in the official's own language), both excluding the display
// name. Deduped by (name, lang) — the catalog_label_alias unique key.
func aliasSpecsFor(o officialRow, aliases []officialAliasRow) []aliasSpec {
	seen := map[[2]string]bool{}
	var out []aliasSpec
	add := func(name, lang, norm string) {
		name = strings.TrimSpace(name)
		if name == "" || name == o.Name {
			return
		}
		key := [2]string{name, lang}
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, aliasSpec{name: name, lang: lang, norm: norm})
	}
	for _, a := range aliases {
		add(a.Name, "", a.Norm)
	}
	if o.Original != "" {
		add(o.Original, o.Lang, o.OriginalNorm)
	}
	return out
}

func aliasRowsFor(o officialRow, aliases []officialAliasRow, labelID int64) []model.CatalogLabelAlias {
	specs := aliasSpecsFor(o, aliases)
	out := make([]model.CatalogLabelAlias, len(specs))
	for i, s := range specs {
		out[i] = model.CatalogLabelAlias{
			LabelID: labelID, Name: s.name, Lang: s.lang,
			Kind: model.AliasKindSpellingVariant, IsPrimaryForLocale: false,
		}
	}
	return out
}

// lhsNormsFor is the deduped set of NFKC-lower norms for an official's display
// name, original, and aliases — the left side of the name-collision check.
func lhsNormsFor(o officialRow, aliases []officialAliasRow) []string {
	seen := map[string]bool{}
	var out []string
	add := func(n string) {
		if n == "" || seen[n] {
			return
		}
		seen[n] = true
		out = append(out, n)
	}
	add(o.NameNorm)
	if o.Original != "" {
		add(o.OriginalNorm)
	}
	for _, s := range aliasSpecsFor(o, aliases) {
		add(s.norm)
	}
	return out
}

func importedLabelRev(labelID int64, label model.CatalogLabel, aliases []model.CatalogLabelAlias) model.CatalogRevision {
	snap, _ := json.Marshal(map[string]any{"label": label, "aliases": aliases})
	return model.CatalogRevision{
		EntityType: model.EntityTypeLabel, EntityID: labelID, Revision: 1,
		Action: model.RevisionActionImported, Snapshot: snap, IsMinor: false,
	}
}

func appendUnique(ids []int64, id int64) []int64 {
	for _, x := range ids {
		if x == id {
			return ids
		}
	}
	return append(ids, id)
}
