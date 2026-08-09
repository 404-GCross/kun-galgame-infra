package importer

import (
	"sort"
	"strings"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The VNDB roster wave (step 47) lands VNDB character DATA — catalog_character
// entities + catalog_work_character roster edges (with the spoiler level) — for
// works that hold an EXACT VNDB work anchor (source 2). VNDB is catalog's
// largest work-anchor source and the only one carrying character roles
// (main/primary/side/appears) and a per-appearance spoiler bit.
//
// Identity discipline (steps 13/45): a NEW character is created with a self
// exact anchor (rule:vndb-character-import); zero persons; the VNDB romaji goes
// into a spelling_variant alias, the native name into display_name.
//
// ⚠️ Same-work same-name attach (the step-47 ruling that stops the duplicate
// explosion): a VNDB source is the 3rd roster source, so the same character can
// already exist as a Bangumi and/or EG entity on the same work. For each in-gate
// VNDB character, if an existing catalog character on ANY of its gated works has
// a display_name/alias whose NFKC fold equals the VNDB character's native or
// romaji name, the VNDB self-anchor is ATTACHED to that existing entity
// (matched_by rule:same-work-character-name, an exact link) instead of minting a
// new one — same work + same name is a strong structural signal, an auto-exact
// rule, and every attach is one row that a human can undo. The edge is still
// built; a pre-existing edge keeps its kind/spoiler (first source wins).

const vndbSource int16 = 2

const (
	ruleVNDBChar         = "rule:vndb-character-import"
	ruleSameWorkCharName = "rule:same-work-character-name"
)

// vndbName is a VNDB character's chosen display name (native, ja-preferred) plus
// its romaji (the spelling_variant alias) and the native name's language.
type vndbName struct {
	native string
	romaji string
	lang   string
}

// vndbAliasPlan queues a spelling_variant romaji alias for a resolved entity.
type vndbAliasPlan struct {
	charExtID string
	name      string
	lang      string
}

// anchorItem is an exact anchor to ATTACH to an already-existing entity (no new
// entity row, no revision — the entity already has its history).
type anchorItem struct {
	extID    string
	entityID int64
}

// vndbGateRow is one work-level in-gate roster row (char × work with the
// collapsed kind + spoiler).
type vndbGateRow struct {
	CharID  string `gorm:"column:char_id"`
	WorkID  int64  `gorm:"column:work_id"`
	Kind    int16  `gorm:"column:kind"`
	Spoiler int16  `gorm:"column:spoiler"`
}

func (im *Importer) runRosterVNDB() (RosterStats, error) {
	var st RosterStats

	// 1. Gate + work-level roster rows. chars_vns.vid matches
	// catalog_external_ref.external_id VERBATIM ("v38"), so the JOIN is the gate
	// (no cast, no cross-media noise). Release-scoped rows (rid) for one
	// (char, work) collapse to the most-prominent role (min kind) and the least
	// spoiler (min spoil) — the work-level view.
	var gates []vndbGateRow
	if err := im.catalog.Raw(`
		SELECT cv.id AS char_id, r.entity_id AS work_id,
		       min(CASE cv.role WHEN 'main' THEN 1 WHEN 'primary' THEN 1
		                        WHEN 'side' THEN 2 WHEN 'appears' THEN 3 ELSE 9 END) AS kind,
		       min(cv.spoil) AS spoiler
		FROM src_vndb.chars_vns cv
		JOIN catalog_external_ref r ON r.entity_type = ? AND r.source_id = ? AND r.link_kind = ? AND r.external_id = cv.vid
		GROUP BY cv.id, r.entity_id`,
		model.EntityTypeWork, vndbSource, model.LinkKindExact).Scan(&gates).Error; err != nil {
		return st, err
	}
	if im.limit > 0 {
		gates = capGatesByWork(gates, im.limit)
	}

	// 2. Per-character native name (ja-preferred) + romaji, from chars_names.
	names, err := im.loadVNDBNames()
	if err != nil {
		return st, err
	}
	// 3. Existing source-2 char anchors — the idempotent resume index.
	anchorV, err := im.loadCharAnchors(vndbSource)
	if err != nil {
		return st, err
	}
	// 4. Same-work same-name attach targets (charExtID → existing entity id).
	attach, err := im.loadVNDBAttachTargets()
	if err != nil {
		return st, err
	}
	// 5. VNDB ids an ALIVE character already claims at a NON-exact grade (the
	// merge engine demotes both anchors to probable when a merge would leave two
	// same-source exacts on the survivor). Such an id is NOT unclaimed: minting a
	// second body for it would create a duplicate the merge just removed. An id
	// that also has an alive EXACT anchor resumes normally — exactness only
	// decides which link resolves, never whether an id is free.
	claimed, err := im.loadClaimedCharExtIDs(vndbSource)
	if err != nil {
		return st, err
	}
	for ext := range claimed {
		if anchorV[ext] != 0 {
			delete(claimed, ext)
		}
	}
	// 6. Ids whose only exact holder is soft-deleted. A retired character SHOULD
	// free its id, but uq_catalog_external_ref_exact is not deleted_at-aware
	// (source_id, external_id, entity_type WHERE link_kind = 0), so the retired
	// row still squats the exact slot and minting would fail the whole wave on a
	// unique violation. Skip and count instead: the id is unavailable until the
	// retired row leaves the identity index. Disjoint from anchorV by that same
	// index — one exact holder per id, alive or not.
	retiredExact, err := im.loadRetiredExactCharExtIDs(vndbSource)
	if err != nil {
		return st, err
	}

	// Resolve each in-gate character to an entity: already-anchored (resume),
	// attach to existing, or create new. knownIDs holds the ids known before the
	// transaction (anchored + attach); createCharacters supplies the rest.
	var newChars []charItem
	var attachItems []anchorItem
	var aliasPlans []vndbAliasPlan
	knownIDs := map[string]int64{}
	seen := map[string]bool{}
	var plans []rosterPlan

	for _, g := range gates {
		// A claimed-but-not-exact id is left alone entirely: no mint, no attach,
		// no edge, and no promotion of the probable link either — re-grading an
		// anchor is an adjudication, not something an import may do silently.
		// Counted once per character so the number is per-entity, not per-edge.
		if claimed[g.CharID] != 0 || retiredExact[g.CharID] != 0 {
			if !seen[g.CharID] {
				seen[g.CharID] = true
				if claimed[g.CharID] != 0 {
					st.SkippedClaimedProbable++
				} else {
					st.SkippedRetiredExactSquat++
				}
			}
			continue
		}

		kind := g.Kind
		if kind == 9 { // unexpected role (never in-gate today) → unknown
			kind = model.WorkCharacterKindUnknown
		}
		plans = append(plans, rosterPlan{workID: g.WorkID, charExtID: g.CharID, kind: kind, spoiler: g.Spoiler})

		if seen[g.CharID] {
			continue
		}
		seen[g.CharID] = true
		nm, ok := names[g.CharID]
		if !ok || strings.TrimSpace(nm.native) == "" {
			st.SkippedNoName++
			continue
		}
		if nm.romaji != "" && nm.romaji != nm.native {
			aliasPlans = append(aliasPlans, vndbAliasPlan{charExtID: g.CharID, name: nm.romaji, lang: nm.lang})
		}
		switch {
		case anchorV[g.CharID] != 0: // already imported — resume (idempotent)
			knownIDs[g.CharID] = anchorV[g.CharID]
		case attach[g.CharID] != 0: // same-work same-name → attach to existing entity
			knownIDs[g.CharID] = attach[g.CharID]
			attachItems = append(attachItems, anchorItem{extID: g.CharID, entityID: attach[g.CharID]})
			st.AttachedExisting++
		default: // new entity
			newChars = append(newChars, charItem{extID: g.CharID, name: nm.native, lang: nm.lang})
		}
	}
	st.CharactersCreated = len(newChars)

	// Portrait backfill candidate size (stable across dry/apply for idempotency):
	// distinct in-gate characters whose portrait clears the moderation threshold.
	st.PortraitCandidates, err = im.countVNDBPortraitCandidates()
	if err != nil {
		return st, err
	}

	if im.dryRun {
		st.EdgesWritten = len(plans) // would-be (clean-state == apply)
		st.AliasesCreated = len(aliasPlans)
		return st, nil
	}

	err = im.catalog.Transaction(func(tx *gorm.DB) error {
		freshIDs, err := im.createCharacters(tx, vndbSource, ruleVNDBChar, newChars)
		if err != nil {
			return err
		}
		if err := im.attachCharAnchors(tx, vndbSource, ruleSameWorkCharName, attachItems); err != nil {
			return err
		}
		resolve := func(ext string) (int64, bool) {
			if id, ok := freshIDs[ext]; ok {
				return id, true
			}
			if id, ok := knownIDs[ext]; ok && id != 0 {
				return id, true
			}
			return 0, false
		}
		edges, dropped := materializeRoster(plans, resolve, ruleRosterVNDB)
		st.Errors += dropped
		written, err := insertRosterEdges(tx, edges)
		if err != nil {
			return err
		}
		st.EdgesWritten = written
		st.Already = len(edges) - written

		aliasesWritten, err := im.insertCharAliases(tx, aliasPlans, resolve)
		if err != nil {
			return err
		}
		st.AliasesCreated = aliasesWritten

		return rebuildVNDBPortraitBackfill(tx)
	})
	return st, err
}

// loadVNDBNames returns charExtID → its native display name (ja-preferred) +
// romaji + lang. DISTINCT ON picks the ja row when present, else the
// lexicographically first language (deterministic).
func (im *Importer) loadVNDBNames() (map[string]vndbName, error) {
	var rows []struct {
		ID    string `gorm:"column:id"`
		Lang  string `gorm:"column:lang"`
		Name  string `gorm:"column:name"`
		Latin string `gorm:"column:latin"`
	}
	if err := im.catalog.Raw(`
		SELECT DISTINCT ON (id) id, lang, name, latin
		FROM src_vndb.chars_names
		WHERE btrim(name) <> ''
		ORDER BY id, (lang <> 'ja'), lang`).Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[string]vndbName, len(rows))
	for _, r := range rows {
		m[r.ID] = vndbName{native: r.Name, romaji: strings.TrimSpace(r.Latin), lang: r.Lang}
	}
	return m, nil
}

// loadCharAnchors returns external_id → entity_id for one source's EXACT
// character anchors held by an ALIVE character (the idempotent resume index for
// the VNDB wave; empty on the first run since no VNDB character has ever been
// imported). A merged-away holder is deliberately NOT a resume target: a
// soft-deleted row keeps its refs but has left the identity indexes, so the id
// is free again and VNDB may re-create the character.
func (im *Importer) loadCharAnchors(source int16) (map[string]int64, error) {
	return im.loadCharRefsByKind(source, model.LinkKindExact)
}

// loadClaimedCharExtIDs returns external_id → entity_id for one source's
// PROBABLE character refs held by an ALIVE character. `related` is excluded on
// purpose: it is a non-identity link and must never participate in identity
// resolution or dedup.
func (im *Importer) loadClaimedCharExtIDs(source int16) (map[string]int64, error) {
	return im.loadCharRefsByKind(source, model.LinkKindProbable)
}

// loadRetiredExactCharExtIDs returns external_id → entity_id for one source's
// EXACT character refs whose holder is soft-deleted. Non-empty only while a
// retired row still occupies the exact identity index (see the caller).
func (im *Importer) loadRetiredExactCharExtIDs(source int16) (map[string]int64, error) {
	var rows []struct {
		ExternalID string `gorm:"column:external_id"`
		EntityID   int64  `gorm:"column:entity_id"`
	}
	if err := im.catalog.Raw(`
		SELECT r.external_id, min(r.entity_id) AS entity_id
		FROM catalog_external_ref r
		JOIN catalog_character c ON c.id = r.entity_id AND c.deleted_at IS NOT NULL
		WHERE r.entity_type = ? AND r.source_id = ? AND r.link_kind = ?
		GROUP BY r.external_id`,
		model.EntityTypeCharacter, source, model.LinkKindExact).Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[string]int64, len(rows))
	for _, r := range rows {
		m[r.ExternalID] = r.EntityID
	}
	return m, nil
}

// loadCharRefsByKind returns external_id → entity_id for one source's character
// refs at one link grade, restricted to alive holders. The same external id can
// legitimately sit on several entities (the PK carries entity_id), so the
// lowest alive holder is picked for determinism.
func (im *Importer) loadCharRefsByKind(source, linkKind int16) (map[string]int64, error) {
	var rows []struct {
		ExternalID string `gorm:"column:external_id"`
		EntityID   int64  `gorm:"column:entity_id"`
	}
	if err := im.catalog.Raw(`
		SELECT r.external_id, min(r.entity_id) AS entity_id
		FROM catalog_external_ref r
		JOIN catalog_character c ON c.id = r.entity_id AND c.deleted_at IS NULL
		WHERE r.entity_type = ? AND r.source_id = ? AND r.link_kind = ?
		GROUP BY r.external_id`,
		model.EntityTypeCharacter, source, linkKind).Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[string]int64, len(rows))
	for _, r := range rows {
		m[r.ExternalID] = r.EntityID
	}
	return m, nil
}

// loadVNDBAttachTargets returns charExtID → the (deterministic min) existing
// catalog character id that shares a gated work with the VNDB character and has
// an NFKC-equal display_name or alias. The norms are computed in Postgres with
// the SAME lower(normalize(x, NFKC)) the catalog generated columns use, so the
// fold is byte-identical.
func (im *Importer) loadVNDBAttachTargets() (map[string]int64, error) {
	var rows []struct {
		CharID     string `gorm:"column:char_id"`
		ExistingID int64  `gorm:"column:existing_id"`
	}
	if err := im.catalog.Raw(`
		WITH vndb_norms AS (
			SELECT id, lower(normalize(name, NFKC)) AS norm FROM src_vndb.chars_names WHERE btrim(name) <> ''
			UNION
			SELECT id, lower(normalize(latin, NFKC)) AS norm FROM src_vndb.chars_names WHERE btrim(latin) <> ''
		),
		existing AS (
			SELECT wc.work_id, wc.character_id, ch.display_name_norm AS norm
			FROM catalog_work_character wc JOIN catalog_character ch ON ch.id = wc.character_id AND ch.deleted_at IS NULL
			UNION
			SELECT wc.work_id, wc.character_id, al.name_norm AS norm
			FROM catalog_work_character wc JOIN catalog_character_alias al ON al.character_id = wc.character_id
		)
		SELECT cv.id AS char_id, min(e.character_id) AS existing_id
		FROM src_vndb.chars_vns cv
		JOIN catalog_external_ref r ON r.entity_type = ? AND r.source_id = ? AND r.link_kind = ? AND r.external_id = cv.vid
		JOIN vndb_norms vn ON vn.id = cv.id
		JOIN existing e ON e.work_id = r.entity_id AND e.norm = vn.norm
		GROUP BY cv.id`,
		model.EntityTypeWork, vndbSource, model.LinkKindExact).Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[string]int64, len(rows))
	for _, r := range rows {
		m[r.CharID] = r.ExistingID
	}
	return m, nil
}

// countVNDBPortraitCandidates counts distinct in-gate VNDB characters whose
// portrait image clears the moderation threshold (sexual_avg ≤ 100 AND
// violence_avg ≤ 100 on VNDB's 0-200 scale, i.e. average vote ≤ 1.0). Stable
// across dry/apply — the step-48 backfill set size.
func (im *Importer) countVNDBPortraitCandidates() (int, error) {
	var n int64
	if err := im.catalog.Raw(`
		SELECT count(DISTINCT cv.id)
		FROM src_vndb.chars_vns cv
		JOIN catalog_external_ref r ON r.entity_type = ? AND r.source_id = ? AND r.link_kind = ? AND r.external_id = cv.vid
		JOIN src_vndb.chars c ON c.id = cv.id
		JOIN src_vndb.images i ON i.id = c.image
		WHERE btrim(c.image) <> '' AND i.c_sexual_avg <= 100 AND i.c_violence_avg <= 100`,
		model.EntityTypeWork, vndbSource, model.LinkKindExact).Scan(&n).Error; err != nil {
		return 0, err
	}
	return int(n), nil
}

// rebuildVNDBPortraitBackfill (re)materializes the step-48 backfill set: one
// portrait per catalog character (DISTINCT ON collapses the rare case where an
// attached entity carries several VNDB anchors, each with an image). Rebuilt
// wholesale so a re-run is idempotent (no new rows).
func rebuildVNDBPortraitBackfill(tx *gorm.DB) error {
	if err := tx.Exec(`TRUNCATE src_vndb.portrait_backfill`).Error; err != nil {
		return err
	}
	return tx.Exec(`
		INSERT INTO src_vndb.portrait_backfill (catalog_character_id, vndb_char_id, image_id, sexual_avg, violence_avg)
		SELECT DISTINCT ON (r.entity_id) r.entity_id, c.id, c.image, i.c_sexual_avg, i.c_violence_avg
		FROM src_vndb.chars c
		JOIN catalog_external_ref r ON r.entity_type = ? AND r.source_id = ? AND r.link_kind = ? AND r.external_id = c.id
		JOIN src_vndb.images i ON i.id = c.image
		WHERE btrim(c.image) <> '' AND i.c_sexual_avg <= 100 AND i.c_violence_avg <= 100
		ORDER BY r.entity_id, c.id`,
		model.EntityTypeCharacter, vndbSource, model.LinkKindExact).Error
}

// attachCharAnchors writes exact self-anchors that point at ALREADY-EXISTING
// entities (the same-work same-name attach). No entity rows and no revisions —
// each target entity already has its own history; this only records that the
// entity is also VNDB character <extID>.
func (im *Importer) attachCharAnchors(tx *gorm.DB, source int16, rule string, items []anchorItem) error {
	if len(items) == 0 {
		return nil
	}
	refs := make([]model.CatalogExternalRef, len(items))
	for i, it := range items {
		refs[i] = selfRef(model.EntityTypeCharacter, it.entityID, source, it.extID, rule)
	}
	// ON CONFLICT DO NOTHING makes a refreshed-dump re-run idempotent: an
	// already-attached self-anchor is skipped (the mint path already excludes
	// anchored chars; only this attach path could re-collide on the pkey).
	return tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(refs, 1000).Error
}

// insertCharAliases adds spelling_variant romaji aliases to resolved characters,
// ON CONFLICT DO NOTHING on (character_id, name, lang) so an attach path only
// fills a MISSING alias and re-runs write nothing. Returns the number written.
func (im *Importer) insertCharAliases(tx *gorm.DB, plans []vndbAliasPlan, resolve func(string) (int64, bool)) (int, error) {
	type row struct {
		charID int64
		name   string
		lang   string
	}
	rows := make([]row, 0, len(plans))
	for _, p := range plans {
		id, ok := resolve(p.charExtID)
		if !ok {
			continue
		}
		rows = append(rows, row{charID: id, name: p.name, lang: p.lang})
	}
	written := 0
	const batch = 1000
	for start := 0; start < len(rows); start += batch {
		end := min(start+batch, len(rows))
		var sb strings.Builder
		// Every row here is a spelling VNDB itself publishes — one upstream, and
		// never a translation this platform produced, so source_id is the file's
		// own vndbSource and provenance is flat 0. Both arrived in wave 195;
		// provenance is NOT NULL with no default, so naming it is required here,
		// not decorative.
		sb.WriteString(`INSERT INTO catalog_character_alias (character_id, name, lang, kind, is_primary_for_locale, source_id, provenance) VALUES `)
		args := make([]any, 0, (end-start)*5)
		for i := start; i < end; i++ {
			if i > start {
				sb.WriteString(",")
			}
			sb.WriteString("(?,?,?,?,false,?,0)")
			args = append(args, rows[i].charID, rows[i].name, rows[i].lang,
				model.AliasKindSpellingVariant, vndbSource)
		}
		sb.WriteString(` ON CONFLICT (character_id, name, lang) DO NOTHING`)
		res := tx.Exec(sb.String(), args...)
		if res.Error != nil {
			return written, res.Error
		}
		written += int(res.RowsAffected)
	}
	return written, nil
}

// capGatesByWork keeps only the gates for the first n distinct work ids
// (ascending) — the --limit debugging aid, deterministic.
func capGatesByWork(gates []vndbGateRow, n int) []vndbGateRow {
	works := map[int64]bool{}
	for _, g := range gates {
		works[g.WorkID] = true
	}
	keys := make([]int64, 0, len(works))
	for k := range works {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	if n < len(keys) {
		keys = keys[:n]
	}
	keep := make(map[int64]bool, len(keys))
	for _, k := range keys {
		keep[k] = true
	}
	out := gates[:0:0]
	for _, g := range gates {
		if keep[g.WorkID] {
			out = append(out, g)
		}
	}
	return out
}
