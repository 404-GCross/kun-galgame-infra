package main

import (
	"sort"
	"strconv"
	"strings"

	"gorm.io/gorm"
)

// Entity classes this batch deduplicates.
const (
	classCharacter  = "character"
	classCreditName = "credit_name"
	// classOrphanCreditName (step 50) merges cross-source ORPHAN voice-actor
	// names (person_id NULL) that co-occur on the same work under the same
	// folded name. Step 49's credit_name class only merged names under a
	// shared person anchor; orphans have none, so same-work co-occurrence of a
	// voice credit is the structural "same person" signal here (the twin of
	// step 49's same-work character rule).
	classOrphanCreditName = "orphan-creditname"
	// classMixedCreditName (step 98) merges an ORPHAN credit_name into a
	// person-ANCHORED credit_name that co-occurs on the same work under the
	// same role and folded name — the same credit imported once with and once
	// without its person link (an import artifact, NOT a new person
	// assertion: the orphan joins an assertion that already exists; buckets
	// whose anchored rows disagree on the person are FROZEN and skipped).
	classMixedCreditName = "mixed-creditname"
	// classPerson (step 156) merges two catalog_person entities. It has NO SQL
	// detector and is reachable only through -worklist: person equivalence is
	// an identity judgement made by the evidence graph plus adjudication
	// (refs/proj/156), never by a name-shaped signal inside this binary.
	// The merge itself is fully implemented in the shared machinery — a person
	// merge repoints catalog_credit_name.person_id, moves the et=0 anchors,
	// records the redirect and soft-deletes the loser — and touches NO work:
	// person sits one level below the credited name and never reaches a work's
	// read face (the wave-118 touch matrix).
	classPerson = "person"
	// classLabel (step 175) merges two catalog_label entities. Like the person
	// class it has NO SQL detector and is reachable only through -worklist:
	// "these two brands are one brand" is a curation judgement (the wave-175
	// case is one DLsite maker page imported twice under two maker ids), and
	// same-name labels are routinely NOT the same brand — the slash-name survey
	// that opened this wave is a catalogue of names that look alike on purpose.
	// The merge itself is fully implemented in the shared machinery: the label
	// rehang moves brand edges, aliases, intros and credits, and DOES collect
	// the host work ids, because a work renders its labels.
	classLabel = "label"
)

// mergeGroup is one resolved dedup group: a survivor entity that absorbs its
// sources. The survivor is chosen deterministically (see the per-class rules).
type mergeGroup struct {
	class    string
	survivor int64
	sources  []int64
	sample   string // human-readable evidence for the report
}

// detectStats carries the diagnostic counters of a detection pass so the dry
// report can explain what was found and — just as importantly — what was
// deliberately left alone.
type detectStats struct {
	charGroups        int
	charPairs         int
	charDirtyBkt      int // (work,fold) buckets skipped: a source split ≥2 chars there
	charBridged       int // components skipped: cross-bucket bridging reintroduced a shared source
	charInstanceDetox int // buckets where a vndb instance stepped aside for its in-bucket base (step 106)
	creditGroups      int
	creditPairs       int
	// orphan* mirror the character counters for the step-50 orphan voice-name
	// class (same source-distinctness guard + union-find).
	orphanGroups   int
	orphanPairs    int
	orphanDirtyBkt int
	orphanBridged  int
	// mixed* mirror the orphan counters for the step-98 mixed class; frozen
	// counts buckets/components whose anchored rows carry ≥2 distinct persons
	// (an implicit person merge — never touched).
	mixedGroups   int
	mixedPairs    int
	mixedDirtyBkt int
	mixedBridged  int
	mixedFrozen   int
}

// charRow is one bucket-member character with its survivor-ordering signal and
// the set of import sources that assert it.
type charRow struct {
	WorkID      int64  `gorm:"column:work_id"`
	Fold        string `gorm:"column:fold"`
	CharacterID int64  `gorm:"column:character_id"`
	HasImage    bool   `gorm:"column:has_image"`
	Naliases    int    `gorm:"column:naliases"`
	Sources     string `gorm:"column:sources"` // comma-joined external_ref source ids
}

type charMeta struct {
	hasImage bool
	naliases int
	sources  map[int16]bool
	fold     string
}

// detectCharacters finds cross-source character duplicates, STRICTLY scoped to
// characters that co-occur in the SAME work (via a roster edge or a voice
// credit) AND fold to the same name. Fold = lower(NFKC(name)) with every
// Unicode space stripped, so Bangumi's "冬月十夜" and EG/VNDB's "冬月 十夜" collide.
//
// Safety beyond same-work+same-name (the raw signal is NOT enough — a work may
// legitimately hold 15 distinct characters all named "Student", all minted by
// one source): a (work, fold) bucket is only merged when no single import
// source contributed two or more of its characters. A source that gave a work
// two rows with the same name already decided they are two characters; we
// respect that. Bridged components (a character shared across buckets that
// reunites two same-source rows from different works) are dropped whole.
func detectCharacters(db *gorm.DB) ([]mergeGroup, detectStats, error) {
	var rows []charRow
	if err := db.Raw(`
		WITH char_work AS (
		  SELECT character_id, work_id FROM catalog_work_character
		  UNION
		  SELECT character_id, work_id FROM catalog_credit WHERE character_id IS NOT NULL),
		folded AS (
		  SELECT DISTINCT cw.character_id, cw.work_id,
		         regexp_replace(c.display_name_norm, '\s', '', 'g') AS fold
		  FROM char_work cw
		  JOIN catalog_character c ON c.id = cw.character_id AND c.deleted_at IS NULL),
		bn AS (
		  SELECT work_id, fold FROM folded
		  GROUP BY work_id, fold HAVING count(DISTINCT character_id) > 1)
		SELECT f.work_id, f.fold, f.character_id,
		       (c.image_hash IS NOT NULL) AS has_image,
		       (SELECT count(*) FROM catalog_character_alias a WHERE a.character_id = f.character_id) AS naliases,
		       COALESCE((SELECT string_agg(DISTINCT r.source_id::text, ',' ORDER BY r.source_id::text)
		                   FROM catalog_external_ref r
		                  WHERE r.entity_type = 4 AND r.entity_id = f.character_id), '') AS sources
		FROM folded f
		JOIN bn USING (work_id, fold)
		JOIN catalog_character c ON c.id = f.character_id
		ORDER BY f.work_id, f.fold, f.character_id`).Scan(&rows).Error; err != nil {
		return nil, detectStats{}, err
	}

	instOf, err := loadVndbInstanceMap(db)
	if err != nil {
		return nil, detectStats{}, err
	}

	meta := make(map[int64]charMeta, len(rows))
	for _, r := range rows {
		if _, ok := meta[r.CharacterID]; !ok {
			meta[r.CharacterID] = charMeta{hasImage: r.HasImage, naliases: r.Naliases, sources: parseSources(r.Sources), fold: r.Fold}
		}
	}
	srcOf := func(id int64) map[int16]bool { return meta[id].sources }

	uf := newUnionFind()
	var st detectStats
	inClean := map[int64]bool{}
	// Walk buckets in the ordered stream (work_id, fold).
	for i := 0; i < len(rows); {
		j := i
		for j < len(rows) && rows[j].WorkID == rows[i].WorkID && rows[j].Fold == rows[i].Fold {
			j++
		}
		bucket := rows[i:j]
		i = j
		// vndb main+instance detox (step 106): an instance row whose base twin
		// sits in the same bucket is vndb's own deliberate split (main_spoil
		// carries the spoiler) — the instance steps aside instead of dirtying
		// the bucket; the remaining members merge onto the base.
		if filtered := detoxVndbInstances(bucket, instOf); len(filtered) < len(bucket) {
			st.charInstanceDetox++
			bucket = filtered
		}
		if len(bucket) < 2 {
			continue
		}
		if hasSourceCollision(idsOf(bucket), srcOf) {
			st.charDirtyBkt++
			continue
		}
		for k := 1; k < len(bucket); k++ {
			uf.union(bucket[0].CharacterID, bucket[k].CharacterID)
		}
		for _, m := range bucket {
			inClean[m.CharacterID] = true
		}
	}

	// Assemble components, then re-check each for a source collision that only
	// cross-bucket bridging could have created.
	comps := map[int64][]int64{}
	for id := range inClean {
		root := uf.find(id)
		comps[root] = append(comps[root], id)
	}
	var groups []mergeGroup
	for _, ids := range comps {
		if len(ids) < 2 {
			continue
		}
		if hasSourceCollision(ids, srcOf) {
			st.charBridged++
			continue
		}
		survivor, sources := pickCharSurvivor(ids, meta)
		st.charGroups++
		st.charPairs += len(sources)
		groups = append(groups, mergeGroup{
			class: classCharacter, survivor: survivor, sources: sources,
			sample: "'" + meta[survivor].fold + "' survivor=" + itoa(survivor) + " sources=" + joinIDs(sources),
		})
	}
	sort.Slice(groups, func(a, b int) bool { return groups[a].survivor < groups[b].survivor })
	return groups, st, nil
}

// hasSourceCollision reports whether any single import source asserts two or
// more of the given ids — the "one source split ≥2 same-name entities, so it
// already decided they differ" guard (the "15 Students, all VNDB" false
// positive). Shared by the character and orphan voice-name detectors; sourcesOf
// yields a member's external_ref source set.
func hasSourceCollision(ids []int64, sourcesOf func(int64) map[int16]bool) bool {
	seen := map[int16]bool{}
	for _, id := range ids {
		for s := range sourcesOf(id) {
			if seen[s] {
				return true
			}
			seen[s] = true
		}
	}
	return false
}

// pickCharSurvivor applies the deterministic rule: keep a portrait-bearing
// character, then the richest alias set, then the lowest id (stable). Never
// drop a portrait — the survivorship pass is the backstop, this is the front stop.
func pickCharSurvivor(ids []int64, meta map[int64]charMeta) (survivor int64, sources []int64) {
	ordered := append([]int64(nil), ids...)
	sort.Slice(ordered, func(a, b int) bool {
		ma, mb := meta[ordered[a]], meta[ordered[b]]
		if ma.hasImage != mb.hasImage {
			return ma.hasImage
		}
		if ma.naliases != mb.naliases {
			return ma.naliases > mb.naliases
		}
		return ordered[a] < ordered[b]
	})
	return ordered[0], ordered[1:]
}

// creditRow is one member of a same-person folded-name group.
type creditRow struct {
	ID       int64  `gorm:"column:id"`
	PersonID int64  `gorm:"column:person_id"`
	Fold     string `gorm:"column:fold"`
	Naliases int    `gorm:"column:naliases"`
	Ncredits int    `gorm:"column:ncredits"`
}

// detectCreditNames finds duplicate credit names UNDER THE SAME person: the
// person id is a hard identity anchor, so folded-name equality there means one
// person's name written twice (width/space variants across sources). Orphan
// names (person_id NULL) are never name-merged — no anchor, too risky.
func detectCreditNames(db *gorm.DB) ([]mergeGroup, detectStats, error) {
	var rows []creditRow
	if err := db.Raw(`
		WITH g AS (
		  SELECT person_id, regexp_replace(name_norm, '\s', '', 'g') AS fold
		  FROM catalog_credit_name WHERE person_id IS NOT NULL
		  GROUP BY person_id, regexp_replace(name_norm, '\s', '', 'g')
		  HAVING count(*) > 1)
		SELECT cn.id, cn.person_id, regexp_replace(cn.name_norm, '\s', '', 'g') AS fold,
		       (SELECT count(*) FROM catalog_name_alias a WHERE a.credit_name_id = cn.id) AS naliases,
		       (SELECT count(*) FROM catalog_credit c WHERE c.credit_name_id = cn.id) AS ncredits
		FROM catalog_credit_name cn
		JOIN g ON g.person_id = cn.person_id
		      AND g.fold = regexp_replace(cn.name_norm, '\s', '', 'g')
		ORDER BY cn.person_id, fold, cn.id`).Scan(&rows).Error; err != nil {
		return nil, detectStats{}, err
	}

	var groups []mergeGroup
	var st detectStats
	for i := 0; i < len(rows); {
		j := i
		for j < len(rows) && rows[j].PersonID == rows[i].PersonID && rows[j].Fold == rows[i].Fold {
			j++
		}
		bucket := rows[i:j]
		i = j
		// Survivor: richest name (aliases, then credits), then lowest id.
		ordered := append([]creditRow(nil), bucket...)
		sort.Slice(ordered, func(a, b int) bool {
			if ordered[a].Naliases != ordered[b].Naliases {
				return ordered[a].Naliases > ordered[b].Naliases
			}
			if ordered[a].Ncredits != ordered[b].Ncredits {
				return ordered[a].Ncredits > ordered[b].Ncredits
			}
			return ordered[a].ID < ordered[b].ID
		})
		survivor := ordered[0].ID
		sources := make([]int64, 0, len(ordered)-1)
		for _, r := range ordered[1:] {
			sources = append(sources, r.ID)
		}
		st.creditGroups++
		st.creditPairs += len(sources)
		groups = append(groups, mergeGroup{
			class: classCreditName, survivor: survivor, sources: sources,
			sample: "person=" + itoa(ordered[0].PersonID) + " '" + ordered[0].Fold + "' survivor=" + itoa(survivor) + " sources=" + joinIDs(sources),
		})
	}
	return groups, st, nil
}

// orphanRow is one bucket-member orphan credit name with its survivor-ordering
// signal and the set of import sources that assert it. Carries role_id since
// step 98 (buckets are (work, role, fold)).
type orphanRow struct {
	WorkID       int64  `gorm:"column:work_id"`
	RoleID       int64  `gorm:"column:role_id"`
	Fold         string `gorm:"column:fold"`
	CreditNameID int64  `gorm:"column:credit_name_id"`
	Ncredits     int    `gorm:"column:ncredits"`
	Naliases     int    `gorm:"column:naliases"`
	Sources      string `gorm:"column:sources"`
}

type orphanMeta struct {
	ncredits int
	naliases int
	sources  map[int16]bool
	fold     string
}

// detectOrphanCreditNames finds cross-source ORPHAN names (person_id NULL)
// that co-occur on the SAME work under the SAME role AND fold to the same
// name. Step 50 scoped this to voice credits; step 98 (refs/proj/98) widens it
// to ALL roles — the QA undermerge scan showed the identical import artifact
// across scenario/art/music/staff — and tightens the bucket to
// (work, role, fold): a scenario writer and an artist sharing a pen name on
// one work stay two names (the role is part of the "same credit" evidence).
// Fold = lower(NFKC(name)) with every Unicode space stripped, so EG's 藤咲ウサ
// and DLsite's 藤咲ウサ collide even across width/space variants. Once judged
// the same, the merge repoints ALL of the source's credits globally (a name is
// one entity, not per-work).
//
// Same safety as the character detector: a (work, fold) bucket is only merged
// when no single import source (external_ref) contributed two or more of its
// names — a source that voiced a work under two rows of the same name already
// decided they are two names. Bridged components are dropped whole.
func detectOrphanCreditNames(db *gorm.DB) ([]mergeGroup, detectStats, error) {
	var rows []orphanRow
	if err := db.Raw(`
		WITH cn_work AS (
		  SELECT DISTINCT c.credit_name_id, c.work_id, c.role_id
		  FROM catalog_credit c
		  JOIN catalog_credit_name cn ON cn.id = c.credit_name_id AND cn.person_id IS NULL),
		folded AS (
		  SELECT cw.credit_name_id, cw.work_id, cw.role_id,
		         regexp_replace(cn.name_norm, '\s', '', 'g') AS fold
		  FROM cn_work cw
		  JOIN catalog_credit_name cn ON cn.id = cw.credit_name_id),
		bn AS (
		  SELECT work_id, role_id, fold FROM folded
		  GROUP BY work_id, role_id, fold HAVING count(DISTINCT credit_name_id) > 1)
		SELECT f.work_id, f.role_id, f.fold, f.credit_name_id,
		       (SELECT count(*) FROM catalog_credit c WHERE c.credit_name_id = f.credit_name_id) AS ncredits,
		       (SELECT count(*) FROM catalog_name_alias a WHERE a.credit_name_id = f.credit_name_id) AS naliases,
		       COALESCE((SELECT string_agg(DISTINCT r.source_id::text, ',' ORDER BY r.source_id::text)
		                   FROM catalog_external_ref r
		                  WHERE r.entity_type = 1 AND r.entity_id = f.credit_name_id), '') AS sources
		FROM folded f
		JOIN bn USING (work_id, role_id, fold)
		ORDER BY f.work_id, f.role_id, f.fold, f.credit_name_id`).Scan(&rows).Error; err != nil {
		return nil, detectStats{}, err
	}

	meta := make(map[int64]orphanMeta, len(rows))
	for _, r := range rows {
		if _, ok := meta[r.CreditNameID]; !ok {
			meta[r.CreditNameID] = orphanMeta{ncredits: r.Ncredits, naliases: r.Naliases, sources: parseSources(r.Sources), fold: r.Fold}
		}
	}
	srcOf := func(id int64) map[int16]bool { return meta[id].sources }

	uf := newUnionFind()
	var st detectStats
	inClean := map[int64]bool{}
	for i := 0; i < len(rows); {
		j := i
		for j < len(rows) && rows[j].WorkID == rows[i].WorkID && rows[j].RoleID == rows[i].RoleID && rows[j].Fold == rows[i].Fold {
			j++
		}
		bucket := rows[i:j]
		i = j
		ids := make([]int64, len(bucket))
		for k, r := range bucket {
			ids[k] = r.CreditNameID
		}
		if hasSourceCollision(ids, srcOf) {
			st.orphanDirtyBkt++
			continue
		}
		for k := 1; k < len(ids); k++ {
			uf.union(ids[0], ids[k])
		}
		for _, id := range ids {
			inClean[id] = true
		}
	}

	comps := map[int64][]int64{}
	for id := range inClean {
		root := uf.find(id)
		comps[root] = append(comps[root], id)
	}
	var groups []mergeGroup
	for _, ids := range comps {
		if len(ids) < 2 {
			continue
		}
		if hasSourceCollision(ids, srcOf) {
			st.orphanBridged++
			continue
		}
		survivor, sources := pickOrphanSurvivor(ids, meta)
		st.orphanGroups++
		st.orphanPairs += len(sources)
		groups = append(groups, mergeGroup{
			class: classOrphanCreditName, survivor: survivor, sources: sources,
			sample: "'" + meta[survivor].fold + "' survivor=" + itoa(survivor) + " sources=" + joinIDs(sources),
		})
	}
	sort.Slice(groups, func(a, b int) bool { return groups[a].survivor < groups[b].survivor })
	return groups, st, nil
}

// pickOrphanSurvivor keeps the richest name: most credits, then most aliases,
// then the lowest id (stable). The survivor absorbs the others' credits.
func pickOrphanSurvivor(ids []int64, meta map[int64]orphanMeta) (survivor int64, sources []int64) {
	ordered := append([]int64(nil), ids...)
	sort.Slice(ordered, func(a, b int) bool {
		ma, mb := meta[ordered[a]], meta[ordered[b]]
		if ma.ncredits != mb.ncredits {
			return ma.ncredits > mb.ncredits
		}
		if ma.naliases != mb.naliases {
			return ma.naliases > mb.naliases
		}
		return ordered[a] < ordered[b]
	})
	return ordered[0], ordered[1:]
}

// --- small helpers ----------------------------------------------------------

func parseSources(s string) map[int16]bool {
	out := map[int16]bool{}
	if s == "" {
		return out
	}
	for _, p := range strings.Split(s, ",") {
		if n, err := strconv.Atoi(p); err == nil {
			out[int16(n)] = true
		}
	}
	return out
}

func idsOf(bucket []charRow) []int64 {
	out := make([]int64, len(bucket))
	for i, r := range bucket {
		out[i] = r.CharacterID
	}
	return out
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func joinIDs(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = itoa(id)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// unionFind is a tiny disjoint-set over int64 ids (path-compressed).
type unionFind struct{ parent map[int64]int64 }

func newUnionFind() *unionFind { return &unionFind{parent: map[int64]int64{}} }

func (u *unionFind) find(x int64) int64 {
	p, ok := u.parent[x]
	if !ok {
		u.parent[x] = x
		return x
	}
	if p != x {
		u.parent[x] = u.find(p)
	}
	return u.parent[x]
}

func (u *unionFind) union(a, b int64) {
	ra, rb := u.find(a), u.find(b)
	if ra != rb {
		u.parent[ra] = rb
	}
}

// mixedRow is one bucket-member of the step-98 mixed class: an orphan OR
// person-anchored credit name sharing (work, role, fold) with the others.
type mixedRow struct {
	WorkID       int64  `gorm:"column:work_id"`
	RoleID       int64  `gorm:"column:role_id"`
	Fold         string `gorm:"column:fold"`
	CreditNameID int64  `gorm:"column:credit_name_id"`
	PersonID     *int64 `gorm:"column:person_id"`
	Ncredits     int    `gorm:"column:ncredits"`
	Naliases     int    `gorm:"column:naliases"`
	Sources      string `gorm:"column:sources"`
}

type mixedMeta struct {
	personID *int64
	ncredits int
	naliases int
	sources  map[int16]bool
	fold     string
}

// detectMixedCreditNames finds the step-98 mixed class (refs/proj/98 ruling):
// a (work, role, fold) bucket carrying BOTH person-anchored and orphan rows of
// the same folded name — the same credit imported once with and once without
// its person link. The merge absorbs the orphan(s) into the anchored survivor;
// the person link is PRESERVED, not created (the orphan joins an assertion
// that already exists — person identity resolution stays frozen).
//
// Guards:
//   - anchored rows carrying ≥2 DISTINCT persons in one bucket/component →
//     FROZEN (an implicit person merge; counted, never touched);
//   - the same source-distinctness rule as the character/orphan classes (a
//     source that credited a work under two rows of the same name already
//     decided they are two names);
//   - bridged components re-checked whole (union-find across buckets).
func detectMixedCreditNames(db *gorm.DB) ([]mergeGroup, detectStats, error) {
	var rows []mixedRow
	if err := db.Raw(`
		WITH cn_work AS (
		  SELECT DISTINCT c.credit_name_id, c.work_id, c.role_id
		  FROM catalog_credit c),
		folded AS (
		  SELECT cw.credit_name_id, cw.work_id, cw.role_id, cn.person_id,
		         regexp_replace(cn.name_norm, '\s', '', 'g') AS fold
		  FROM cn_work cw
		  JOIN catalog_credit_name cn ON cn.id = cw.credit_name_id),
		bn AS (
		  SELECT work_id, role_id, fold FROM folded
		  GROUP BY work_id, role_id, fold
		  HAVING count(DISTINCT credit_name_id) > 1
		     AND count(*) FILTER (WHERE person_id IS NULL) > 0
		     AND count(*) FILTER (WHERE person_id IS NOT NULL) > 0)
		SELECT f.work_id, f.role_id, f.fold, f.credit_name_id, f.person_id,
		       (SELECT count(*) FROM catalog_credit c WHERE c.credit_name_id = f.credit_name_id) AS ncredits,
		       (SELECT count(*) FROM catalog_name_alias a WHERE a.credit_name_id = f.credit_name_id) AS naliases,
		       COALESCE((SELECT string_agg(DISTINCT r.source_id::text, ',' ORDER BY r.source_id::text)
		                   FROM catalog_external_ref r
		                  WHERE r.entity_type = 1 AND r.entity_id = f.credit_name_id), '') AS sources
		FROM folded f
		JOIN bn USING (work_id, role_id, fold)
		ORDER BY f.work_id, f.role_id, f.fold, f.credit_name_id`).Scan(&rows).Error; err != nil {
		return nil, detectStats{}, err
	}

	meta := make(map[int64]mixedMeta, len(rows))
	for _, r := range rows {
		if _, ok := meta[r.CreditNameID]; !ok {
			meta[r.CreditNameID] = mixedMeta{personID: r.PersonID, ncredits: r.Ncredits,
				naliases: r.Naliases, sources: parseSources(r.Sources), fold: r.Fold}
		}
	}
	srcOf := func(id int64) map[int16]bool { return meta[id].sources }

	// distinctPersons counts the DISTINCT non-null persons among ids.
	distinctPersons := func(ids []int64) map[int64]bool {
		out := map[int64]bool{}
		for _, id := range ids {
			if p := meta[id].personID; p != nil {
				out[*p] = true
			}
		}
		return out
	}

	uf := newUnionFind()
	var st detectStats
	inClean := map[int64]bool{}
	for i := 0; i < len(rows); {
		j := i
		for j < len(rows) && rows[j].WorkID == rows[i].WorkID && rows[j].RoleID == rows[i].RoleID && rows[j].Fold == rows[i].Fold {
			j++
		}
		bucket := rows[i:j]
		i = j
		ids := make([]int64, len(bucket))
		for k, r := range bucket {
			ids[k] = r.CreditNameID
		}
		if len(distinctPersons(ids)) >= 2 {
			st.mixedFrozen++
			continue
		}
		if hasSourceCollision(ids, srcOf) {
			st.mixedDirtyBkt++
			continue
		}
		for k := 1; k < len(ids); k++ {
			uf.union(ids[0], ids[k])
		}
		for _, id := range ids {
			inClean[id] = true
		}
	}

	comps := map[int64][]int64{}
	for id := range inClean {
		root := uf.find(id)
		comps[root] = append(comps[root], id)
	}
	var groups []mergeGroup
	for _, ids := range comps {
		if len(ids) < 2 {
			continue
		}
		if len(distinctPersons(ids)) >= 2 {
			st.mixedFrozen++
			continue
		}
		if hasSourceCollision(ids, srcOf) {
			st.mixedBridged++
			continue
		}
		survivor, sources := pickMixedSurvivor(ids, meta)
		if survivor == 0 {
			continue // no anchored member (pure-orphan component — the orphan class's turf)
		}
		st.mixedGroups++
		st.mixedPairs += len(sources)
		groups = append(groups, mergeGroup{
			class: classMixedCreditName, survivor: survivor, sources: sources,
			sample: "'" + meta[survivor].fold + "' survivor=" + itoa(survivor) + " (anchored) sources=" + joinIDs(sources),
		})
	}
	sort.Slice(groups, func(a, b int) bool { return groups[a].survivor < groups[b].survivor })
	return groups, st, nil
}

// pickMixedSurvivor keeps the person-ANCHORED member (the assertion that
// already exists); among several anchored members (same person by the frozen
// guard), the richest wins. Returns survivor=0 when no member is anchored.
func pickMixedSurvivor(ids []int64, meta map[int64]mixedMeta) (survivor int64, sources []int64) {
	var anchored []int64
	for _, id := range ids {
		if meta[id].personID != nil {
			anchored = append(anchored, id)
		}
	}
	if len(anchored) == 0 {
		return 0, nil
	}
	sort.Slice(anchored, func(a, b int) bool {
		ma, mb := meta[anchored[a]], meta[anchored[b]]
		if ma.ncredits != mb.ncredits {
			return ma.ncredits > mb.ncredits
		}
		if ma.naliases != mb.naliases {
			return ma.naliases > mb.naliases
		}
		return anchored[a] < anchored[b]
	})
	survivor = anchored[0]
	for _, id := range ids {
		if id != survivor {
			sources = append(sources, id)
		}
	}
	return survivor, sources
}

// loadVndbInstanceMap resolves the vndb main+instance links to catalog ids:
// instance char id → base char id, both ends holding a live EXACT vndb anchor.
// Read straight from src_vndb (not catalog_character.instance_of) so detect
// never depends on the backfill's run order.
func loadVndbInstanceMap(db *gorm.DB) (map[int64]int64, error) {
	var rows []struct {
		Inst int64 `gorm:"column:inst"`
		Main int64 `gorm:"column:main_id"`
	}
	if err := db.Raw(`
		SELECT ri.entity_id AS inst, rm.entity_id AS main_id
		FROM catalog_external_ref ri
		JOIN catalog_source s ON s.id = ri.source_id AND s.key = 'vndb'
		JOIN src_vndb.chars ch ON ch.id = ri.external_id AND ch.main <> ''
		JOIN catalog_external_ref rm ON rm.source_id = ri.source_id AND rm.entity_type = 4
			AND rm.link_kind = 0 AND rm.external_id = ch.main AND rm.entity_id <> ri.entity_id
		WHERE ri.entity_type = 4 AND ri.link_kind = 0`).Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[int64]int64, len(rows))
	for _, r := range rows {
		m[r.Inst] = r.Main
	}
	return m, nil
}

// detoxVndbInstances drops every bucket member that is a vndb INSTANCE of
// another member of the same bucket. The instance is not a duplicate — it is
// vndb's deliberate same-name split (alt-universe/spoiler identity), so it
// steps aside; other-source rows then merge onto the base without tripping
// the same-source guard.
func detoxVndbInstances(bucket []charRow, instOf map[int64]int64) []charRow {
	if len(instOf) == 0 {
		return bucket
	}
	present := make(map[int64]bool, len(bucket))
	for _, m := range bucket {
		present[m.CharacterID] = true
	}
	out := bucket[:0:0]
	for _, m := range bucket {
		if base, ok := instOf[m.CharacterID]; ok && present[base] {
			continue
		}
		out = append(out, m)
	}
	return out
}
