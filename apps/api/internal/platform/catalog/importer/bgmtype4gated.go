package importer

// Bangumi type-4 GATED expansion (refs/proj/78, B1). Gates the UNANCHORED
// Bangumi type=4 (game) subjects through a precision-first three-way OR signal
// gate and creates a BODYLESS (site=NULL) medium=galgame catalog_work + an
// EXACT Bangumi anchor (rule:bgm-type4-gated) + title rows + an imported
// revision per gated subject. It reuses the stub-creation discipline of the
// dlsite / bangumi-xmedia waves (selfRef / importedRev / workSnapshotJSON /
// batchRefsRevs / mediumGalgame) — zero new schema, works+titles+anchor+rev only.
//
// Governance (user ruling 2026-07-21, precision over recall — 错收难删/漏收可补):
// console/mobile games are EXCLUDED; the wiki scope is 二次元游戏 (galgame/eroge).
// Every predicate below is GROUNDED in the step-78 survey of the actual
// src_bangumi.subject.meta_tags / tags value distributions.
//
// Eligibility guard (主机/手游排除): a subject carrying ANY console / handheld /
// arcade / mobile platform token AND no PC-family token is dropped up front —
// this is what removes Xenoblade / Disgaea / Madoka-PSP / mobile-otome.
//
// Signal P (PC-platform galgame genre): meta_tags has PC/Windows AND
// (VN|乙女, OR (ADV|AVG AND R18|全年龄)). Bare "PC" is far too loose — 30k+ PC
// FPS/ACT/RPG titles (Team Fortress 2 carries "PC") would leak — so the platform
// token is CONJOINED with a galgame-family genre; the ADV/AVG genre is polluted
// with western/console adventures (Silent Hill 2, Nancy Drew), so it is admitted
// only when age-rated.
//
// Signal T (explicit galgame classification): meta_tags has the CURATED
// "Galgame" token, OR a folksonomy tag (galgame/黄油/エロゲ/…) applied by ≥3 users.
// Explicit enough to stand alone.
//
// Signal X (cross-source galgame corpus): the subject's NFKC-lower title
// (name or name_cn, ≥4 chars) appears in the erogamespace / dlsite-game /
// VNDB-ja release-title corpus — three Japanese PC galgame/VN corpora, all
// computed with the IDENTICAL lower(normalize(col,NFKC)) fold so equality is
// byte-isomorphic with the Bangumi name_norm generated columns (the 56a recipe).
//
// Creation safety rope (防重创建): before creating, the subject's normalized
// title is checked against the ENTIRE existing catalog_work_title set — a hit is
// SKIPPED (a reconcile candidate for the 56a anchor rule, NOT a creation
// candidate). Idempotent: an anchored subject leaves the pool, so a second
// --apply writes zero. Dry-run is the DEFAULT and reports the full survey.

import (
	"fmt"
	"log/slog"
	"math/rand"
	"strconv"

	"api/internal/platform/catalog/model"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

const (
	ruleBgmType4Gated  = "rule:bgm-type4-gated"
	bgmGatedMinLen     = 4    // normalized-title length floor (chars) — both sides
	bgmGatedChunk      = 1000 // work-creation transaction batch size
	bgmGatedSampleSeed = 78   // deterministic random-100 (the reviewer's approval artifact)
	bgmGatedSampleN    = 100
	bgmCollisionSample = 20
)

// SQL array literals for the meta_tags gate predicates. Kept as inline literals
// (a fixed, injection-free vocabulary) so the grounded predicate is legible in
// one place. The jsonb `?`/`?|` OPERATORS are avoided on purpose — GORM treats
// `?` as a bind placeholder — in favor of the equivalent jsonb_exists /
// jsonb_exists_any FUNCTION forms.
const (
	sqlPCPlatforms = `array['PC','Windows']`
	sqlPCFamily    = `array['PC','Windows','Mac','Linux','DOS']`
	// Console / handheld / arcade / mobile — the 主机/手游 exclusion tokens.
	sqlConsoleMobile = `array['PS','PS2','PS3','PS4','PS5','PSP','PSV','NS','NS2','3DS','Wii','WiiU',` +
		`'Xbox','XBOX','Xbox360','XboxOne','XSX','NGC','GB','GBA','GBC','FC','SFC','SS','MD','DC','N64','NDS','WS','PCE','街机','Android','iOS','Symbian']`
	sqlGenreStrict = `array['VN','乙女']`   // clean galgame-family genres
	sqlGenreAdv    = `array['ADV','AVG']` // adventure — needs age-rating to be clean
	sqlAgeRated    = `array['R18','全年龄']` // R18 / all-ages age-rating tokens
	sqlGalgameFolk = `array['galgame','黄油','エロゲ','エロゲー','美少女ゲーム','ギャルゲー']`
)

// dlsiteGameTypes is the DLsite work_type_string allowlist for the cross-source
// corpus — the GAME types only (excludes manga / CG / voice-ASMR / video / audio
// / image-material / tools), so a Bangumi title cannot cross-match a DLsite
// non-game work of a coincidentally-equal name.
var dlsiteGameTypes = []string{
	"ノベル", "デジタルノベル", "アドベンチャー", "ロールプレイング", "シミュレーション",
	"アクション", "パズル", "その他ゲーム", "テーブル", "シューティング", "タイピング", "クイズ",
}

// BgmGatedStats is the survey + apply tally.
type BgmGatedStats struct {
	PoolTotal             int // unanchored type-4 subjects
	ExcludedConsoleMobile int // dropped by the 主机/手游 guard
	EligiblePool          int // PoolTotal - ExcludedConsoleMobile

	SigP, SigT, SigX    int // per-signal counts over the eligible pool
	PT, PX, TX, All3    int // intersections
	POnly, TOnly, XOnly int // each-only

	GatedTotal            int // eligible AND (P|T|X)
	SkippedTitleCollision int // gated but title collides with an existing work
	SkippedIntraCollision int // two gated survivors share a normalized title
	ToCreate              int // works to create (= GatedTotal - the two skip counts)

	WorksCreated     int
	TitlesCreated    int
	AnchorsCreated   int
	RevisionsCreated int

	RandomSample     []BgmGatedSample    // deterministic random-100 of ToCreate
	CollisionSamples []BgmGatedCollision // up to 20 existing-work collisions
}

// BgmGatedSample is one to-create subject for the reviewer's random-100 list.
type BgmGatedSample struct {
	SubjectID int64
	Name      string
	NameCN    string
	Signals   string // e.g. "P", "X", "P+T+X"
}

// BgmGatedCollision is one gated subject skipped because its title already
// exists on a work (a reconcile candidate).
type BgmGatedCollision struct {
	SubjectID    int64
	Name         string
	NameCN       string
	CollidedNorm string
	WorkID       int64
	WorkTitle    string
}

// poolRow is one unanchored type-4 subject with its SQL-computed P/T flags and
// the console/mobile-exclusion verdict; X is decided Go-side against the corpus.
type poolRow struct {
	ID         int64  `gorm:"column:id"`
	Name       string `gorm:"column:name"`
	NameCN     string `gorm:"column:name_cn"`
	NameNorm   string `gorm:"column:name_norm"`
	NameCNNorm string `gorm:"column:name_cn_norm"`
	NSFW       bool   `gorm:"column:nsfw"`
	SigP       bool   `gorm:"column:sig_p"`
	SigT       bool   `gorm:"column:sig_t"`
	Excluded   bool   `gorm:"column:excluded"`
}

// wtNorm identifies the existing work a colliding title belongs to (for the
// collision sample).
type wtNorm struct {
	workID int64
	title  string
}

// candidate is a gated survivor queued for creation.
type candidate struct {
	row     poolRow
	signals string
}

// RunBgmType4Gated executes the gate + creation. eg comes from im.eg, dlsite is
// passed in (the import-eg-dlsite-releases wiring); src_vndb lives in im.catalog.
// Dry-run (im.dryRun) reports the full survey and writes nothing.
func (im *Importer) RunBgmType4Gated(dlsiteDB *gorm.DB) (BgmGatedStats, error) {
	var st BgmGatedStats
	if im.eg == nil || dlsiteDB == nil {
		return st, fmt.Errorf("bgm-type4-gated needs both erogamespace (eg) and dlsite connections")
	}

	xsrc, err := im.loadCrossSourceNorms(dlsiteDB)
	if err != nil {
		return st, fmt.Errorf("load cross-source corpus: %w", err)
	}
	wt, err := im.loadExistingWorkTitleNorms()
	if err != nil {
		return st, fmt.Errorf("load existing work titles: %w", err)
	}
	pool, err := im.loadGatedPool()
	if err != nil {
		return st, fmt.Errorf("load pool: %w", err)
	}

	var toCreate []candidate
	for _, r := range pool {
		st.PoolTotal++
		if r.Excluded {
			st.ExcludedConsoleMobile++
			continue
		}
		st.EligiblePool++
		x := (runeLen(r.NameNorm) >= bgmGatedMinLen && xsrc[r.NameNorm]) ||
			(runeLen(r.NameCNNorm) >= bgmGatedMinLen && xsrc[r.NameCNNorm])
		p, t := r.SigP, r.SigT
		tallySignals(&st, p, t, x)
		if !(p || t || x) {
			continue
		}
		st.GatedTotal++
		if col, ok := collide(r, wt); ok {
			st.SkippedTitleCollision++
			if len(st.CollisionSamples) < bgmCollisionSample {
				st.CollisionSamples = append(st.CollisionSamples, col)
			}
			continue
		}
		toCreate = append(toCreate, candidate{row: r, signals: signalString(p, t, x)})
	}

	toCreate = dropIntraCollisions(toCreate, &st)
	st.ToCreate = len(toCreate)
	st.RandomSample = pickRandomSample(toCreate) // from the FULL to-create set (reviewer artifact)

	if im.dryRun {
		logBgmGated(&st, true)
		return st, nil
	}
	// --limit caps the works actually minted (a small-batch rehearsal aid); the
	// survey above is always over the full pool.
	createSet := toCreate
	if im.limit > 0 && im.limit < len(createSet) {
		createSet = createSet[:im.limit]
	}
	if err := im.createGatedWorks(createSet, &st); err != nil {
		return st, err
	}
	logBgmGated(&st, false)
	return st, nil
}

// tallySignals accumulates the per-signal + overlap matrix over the eligible pool.
func tallySignals(st *BgmGatedStats, p, t, x bool) {
	if p {
		st.SigP++
	}
	if t {
		st.SigT++
	}
	if x {
		st.SigX++
	}
	switch {
	case p && t && x:
		st.All3++
	}
	if p && t {
		st.PT++
	}
	if p && x {
		st.PX++
	}
	if t && x {
		st.TX++
	}
	if p && !t && !x {
		st.POnly++
	}
	if t && !p && !x {
		st.TOnly++
	}
	if x && !p && !t {
		st.XOnly++
	}
}

// collide reports whether either normalized title (≥4) already exists on a work.
func collide(r poolRow, wt map[string]wtNorm) (BgmGatedCollision, bool) {
	for _, n := range []string{r.NameNorm, r.NameCNNorm} {
		if runeLen(n) < bgmGatedMinLen {
			continue
		}
		if w, ok := wt[n]; ok {
			return BgmGatedCollision{
				SubjectID: r.ID, Name: r.Name, NameCN: r.NameCN,
				CollidedNorm: n, WorkID: w.workID, WorkTitle: w.title,
			}, true
		}
	}
	return BgmGatedCollision{}, false
}

// dropIntraCollisions removes every survivor that shares a normalized title with
// ANOTHER survivor (the bidirectional-uniqueness guard within the wave: two
// same-title subjects are ambiguous, so neither is minted — they stay reconcile
// candidates, 漏收可补).
func dropIntraCollisions(cands []candidate, st *BgmGatedStats) []candidate {
	subjectsPerNorm := make(map[string]map[int64]struct{})
	note := func(norm string, id int64) {
		if runeLen(norm) < bgmGatedMinLen {
			return
		}
		if subjectsPerNorm[norm] == nil {
			subjectsPerNorm[norm] = make(map[int64]struct{})
		}
		subjectsPerNorm[norm][id] = struct{}{}
	}
	for _, c := range cands {
		note(c.row.NameNorm, c.row.ID)
		note(c.row.NameCNNorm, c.row.ID)
	}
	dupNorm := func(norm string) bool {
		return runeLen(norm) >= bgmGatedMinLen && len(subjectsPerNorm[norm]) > 1
	}
	out := cands[:0]
	for _, c := range cands {
		if dupNorm(c.row.NameNorm) || dupNorm(c.row.NameCNNorm) {
			st.SkippedIntraCollision++
			continue
		}
		out = append(out, c)
	}
	return out
}

// pickRandomSample returns a deterministic (seeded) random sample of ≤100
// to-create subjects — the reviewer's approval artifact.
func pickRandomSample(cands []candidate) []BgmGatedSample {
	idx := make([]int, len(cands))
	for i := range idx {
		idx[i] = i
	}
	rng := rand.New(rand.NewSource(bgmGatedSampleSeed))
	rng.Shuffle(len(idx), func(i, j int) { idx[i], idx[j] = idx[j], idx[i] })
	n := min(bgmGatedSampleN, len(idx))
	out := make([]BgmGatedSample, 0, n)
	for _, i := range idx[:n] {
		c := cands[i]
		out = append(out, BgmGatedSample{SubjectID: c.row.ID, Name: c.row.Name, NameCN: c.row.NameCN, Signals: c.signals})
	}
	return out
}

// createGatedWorks mints the bodyless works + titles + exact anchors + imported
// revisions in chunked transactions (the dlsite/xmedia stub-creation discipline).
func (im *Importer) createGatedWorks(cands []candidate, st *BgmGatedStats) error {
	for start := 0; start < len(cands); start += bgmGatedChunk {
		end := min(start+bgmGatedChunk, len(cands))
		chunk := cands[start:end]
		if err := im.catalog.Transaction(func(tx *gorm.DB) error {
			return im.createGatedChunk(tx, chunk, st)
		}); err != nil {
			return err
		}
	}
	return nil
}

func (im *Importer) createGatedChunk(tx *gorm.DB, chunk []candidate, st *BgmGatedStats) error {
	works := make([]model.CatalogWork, len(chunk))
	for i, c := range chunk {
		display := c.row.Name
		olang := "ja"
		if display == "" { // Chinese-only subject (e.g. 国产 galgame with no ja name)
			display = c.row.NameCN
			olang = "zh-Hans"
		}
		cr := model.ContentRatingAllAges
		if c.row.NSFW { // Bangumi nsfw → R18 (doc 17 §6: all-ages is never inferred, but 0 is the not-null floor)
			cr = model.ContentRatingR18
		}
		works[i] = model.CatalogWork{
			MediumID: mediumGalgame, OLang: olang, DisplayName: display,
			ContentRating: cr, Status: model.WorkStatusLive,
			Extra: datatypes.JSON(`{}`), FieldProvenance: datatypes.JSON(`{}`),
		}
	}
	if err := tx.CreateInBatches(works, 1000).Error; err != nil {
		return err
	}

	var titles []model.CatalogWorkTitle
	for i, c := range chunk {
		wid := works[i].ID
		if c.row.Name != "" {
			titles = append(titles, model.CatalogWorkTitle{WorkID: wid, Lang: "ja", Title: c.row.Name, Kind: model.WorkTitleKindOfficial})
		}
		if c.row.NameCN != "" && c.row.NameCN != c.row.Name {
			titles = append(titles, model.CatalogWorkTitle{WorkID: wid, Lang: "zh-Hans", Title: c.row.NameCN, Kind: model.WorkTitleKindOfficial})
		}
	}
	if err := tx.CreateInBatches(titles, 1000).Error; err != nil {
		return err
	}
	// Rebuild the per-work title index from the INSERTED slice so the revision
	// snapshot carries the real title ids (the dlsite createDLWorkChunk pattern).
	titlesByWork := make(map[int64][]model.CatalogWorkTitle, len(chunk))
	for _, t := range titles {
		titlesByWork[t.WorkID] = append(titlesByWork[t.WorkID], t)
	}

	refs := make([]model.CatalogExternalRef, len(chunk))
	revs := make([]model.CatalogRevision, len(chunk))
	for i, c := range chunk {
		wid := works[i].ID
		refs[i] = selfRef(model.EntityTypeWork, wid, bangumiSource, strconv.FormatInt(c.row.ID, 10), ruleBgmType4Gated)
		revs[i] = importedRev(model.EntityTypeWork, wid, workSnapshotJSON(works[i], titlesByWork[wid]))
	}
	if err := im.batchRefsRevs(tx, refs, revs); err != nil {
		return err
	}

	st.WorksCreated += len(works)
	st.TitlesCreated += len(titles)
	st.AnchorsCreated += len(refs)
	st.RevisionsCreated += len(revs)
	return nil
}

func signalString(p, t, x bool) string {
	var parts []string
	if p {
		parts = append(parts, "P")
	}
	if t {
		parts = append(parts, "T")
	}
	if x {
		parts = append(parts, "X")
	}
	out := ""
	for i, s := range parts {
		if i > 0 {
			out += "+"
		}
		out += s
	}
	return out
}

func logBgmGated(st *BgmGatedStats, dry bool) {
	slog.Info("bgm-type4-gated survey",
		"dry", dry, "pool_total", st.PoolTotal, "excluded_console_mobile", st.ExcludedConsoleMobile,
		"eligible_pool", st.EligiblePool, "sig_p", st.SigP, "sig_t", st.SigT, "sig_x", st.SigX,
		"gated_total", st.GatedTotal, "p_and_t", st.PT, "p_and_x", st.PX, "t_and_x", st.TX, "all_three", st.All3,
		"p_only", st.POnly, "t_only", st.TOnly, "x_only", st.XOnly,
		"skipped_title_collision", st.SkippedTitleCollision, "skipped_intra_collision", st.SkippedIntraCollision,
		"to_create", st.ToCreate, "works_created", st.WorksCreated, "titles_created", st.TitlesCreated,
		"anchors_created", st.AnchorsCreated, "revisions_created", st.RevisionsCreated)
}

// runeLen is the character length of s (Postgres length() semantics), the
// isomorphic counterpart to the SQL-side `length(norm) >= 4` floors.
func runeLen(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
