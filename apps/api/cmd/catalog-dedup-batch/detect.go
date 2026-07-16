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
	charGroups   int
	charPairs    int
	charDirtyBkt int // (work,fold) buckets skipped: a source split ≥2 chars there
	charBridged  int // components skipped: cross-bucket bridging reintroduced a shared source
	creditGroups int
	creditPairs  int
	// orphan* mirror the character counters for the step-50 orphan voice-name
	// class (same source-distinctness guard + union-find).
	orphanGroups   int
	orphanPairs    int
	orphanDirtyBkt int
	orphanBridged  int
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
// signal and the set of import sources that assert it.
type orphanRow struct {
	WorkID       int64  `gorm:"column:work_id"`
	Fold         string `gorm:"column:fold"`
	CreditNameID int64  `gorm:"column:credit_name_id"`
	Ncredits     int    `gorm:"column:ncredits"`
	Naliases     int    `gorm:"column:naliases"`
	Sources      string `gorm:"column:sources"` // comma-joined external_ref source ids
}

type orphanMeta struct {
	ncredits int
	naliases int
	sources  map[int16]bool
	fold     string
}

// detectOrphanCreditNames finds cross-source ORPHAN voice-actor names
// (person_id NULL) that co-occur on the SAME work via a voice credit AND fold
// to the same name — the step-50 twin of the character detector. Fold =
// lower(NFKC(name)) with every Unicode space stripped, so EG's 藤咲ウサ and
// DLsite's 藤咲ウサ collide even across width/space variants. Step 49's
// credit_name class merged names under a shared person anchor; orphans have no
// anchor, so same-work co-occurrence of a voice credit IS the "same person"
// evidence — and once judged the same, the merge repoints ALL of the source's
// credits globally (a voice actor is one person, not per-work).
//
// Same safety as the character detector: a (work, fold) bucket is only merged
// when no single import source (external_ref) contributed two or more of its
// names — a source that voiced a work under two rows of the same name already
// decided they are two names. Bridged components are dropped whole.
func detectOrphanCreditNames(db *gorm.DB) ([]mergeGroup, detectStats, error) {
	var rows []orphanRow
	if err := db.Raw(`
		WITH cn_work AS (
		  SELECT DISTINCT c.credit_name_id, c.work_id
		  FROM catalog_credit c
		  JOIN catalog_credit_name cn ON cn.id = c.credit_name_id AND cn.person_id IS NULL
		  WHERE c.role_id = (SELECT id FROM catalog_role WHERE key = 'voice-actor')),
		folded AS (
		  SELECT cw.credit_name_id, cw.work_id,
		         regexp_replace(cn.name_norm, '\s', '', 'g') AS fold
		  FROM cn_work cw
		  JOIN catalog_credit_name cn ON cn.id = cw.credit_name_id),
		bn AS (
		  SELECT work_id, fold FROM folded
		  GROUP BY work_id, fold HAVING count(DISTINCT credit_name_id) > 1)
		SELECT f.work_id, f.fold, f.credit_name_id,
		       (SELECT count(*) FROM catalog_credit c WHERE c.credit_name_id = f.credit_name_id) AS ncredits,
		       (SELECT count(*) FROM catalog_name_alias a WHERE a.credit_name_id = f.credit_name_id) AS naliases,
		       COALESCE((SELECT string_agg(DISTINCT r.source_id::text, ',' ORDER BY r.source_id::text)
		                   FROM catalog_external_ref r
		                  WHERE r.entity_type = 1 AND r.entity_id = f.credit_name_id), '') AS sources
		FROM folded f
		JOIN bn USING (work_id, fold)
		ORDER BY f.work_id, f.fold, f.credit_name_id`).Scan(&rows).Error; err != nil {
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
		for j < len(rows) && rows[j].WorkID == rows[i].WorkID && rows[j].Fold == rows[i].Fold {
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
