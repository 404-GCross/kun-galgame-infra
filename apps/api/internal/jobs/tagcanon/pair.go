package tagcanon

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"api/internal/platform/galgame/vndbresolve"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// ProposeOpts configures the propose phase (doc 87 P2 --propose): blocking →
// candidate pairs → LLM seam → JSONL verdicts, plus single-source tier/kind
// proposals for names clearing the usage gate.
type ProposeOpts struct {
	// DSN is REQUIRED — the catalog DB holding all three vocabularies (single
	// pool, as 74). A bare run cannot touch a live DB.
	DSN string
	// DlsiteDSN is OPTIONAL: the dlsite staging DB (genre_taxonomy) used ONLY to
	// enrich dlsite names with their ja original (doc 87 ruling 2; the same
	// second-pool convention as cmd/backfill-dlsite-genres). Blank → dlsite orig
	// falls back to the zh name (degraded, still functional).
	DlsiteDSN string
	// TagMapPath overrides docs/tagMap.ts (env KUN_VNDB_TAGMAP_PATH also works).
	// The inverted map gives every vndb tag its English original. Missing →
	// vndb orig falls back to the zh name.
	TagMapPath string
	// SingleThreshold gates single-source admission (doc 87 ruling 4; default
	// 100). A single-source name with usage ≥ threshold gets an LLM tier/kind
	// proposal; below it is dropped this wave.
	SingleThreshold int
	// Out is the JSONL verdict path (REQUIRED).
	Out string
	// Workers sizes the LLM call pool (<=1 serial). MaxPairs caps the blocking
	// budget (0 = pinned default).
	Workers  int
	MaxPairs int
	MaxEdit  int
	// Prior is OPTIONAL: a previous wave's verdict/decisions JSONL (doc 90
	// ruling 5). Pairs already judged there are skipped (counted, never
	// re-judged); singles are never filtered — a judged single is either
	// mapped (already out of the pool) or errored (must be re-judged).
	Prior string
}

// ProposeStats reports a propose run.
type ProposeStats struct {
	Block          blockStats
	Pairs          int
	SingleProposed int
	RelationCounts map[Relation]int
	Errors         int
	SkippedPrior   int
}

// Propose runs blocking + the LLM seam and writes the verdict JSONL. mt is the
// matcher seam (REQUIRED). Sources are resolved by key so the JSONL is
// environment-portable.
func Propose(ctx context.Context, mt Matcher, opts ProposeOpts) (*ProposeStats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn)")
	}
	if opts.Out == "" {
		return nil, fmt.Errorf("output path is required (--out)")
	}
	if mt == nil {
		return nil, fmt.Errorf("a matcher is required (use MockMatcher for the offline rehearsal)")
	}
	if opts.SingleThreshold <= 0 {
		opts.SingleThreshold = 100
	}
	db, err := openGorm(opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect catalog db: %w", err)
	}
	if sqlDB, e := db.DB(); e == nil {
		defer sqlDB.Close()
	}

	pool, err := buildPool(ctx, db, opts)
	if err != nil {
		return nil, err
	}

	// Blocking runs over the non-meta slice (ruling 3: meta stays out of pairing).
	blockingPool := make([]candName, 0, len(pool))
	for _, c := range pool {
		if !isMeta(c.Norm) {
			blockingPool = append(blockingPool, c)
		}
	}
	pairs, bst := buildCandidatePairs(blockingPool, blockOpts{MaxPairs: opts.MaxPairs, MaxEdit: opts.MaxEdit})
	slog.Info("tag-pair blocking", "pool", len(blockingPool), "pairs", bst.Total,
		"substring", bst.Substring, "edit", bst.Edit, "cooccur", bst.Cooccur, "capped", bst.Capped)

	st := &ProposeStats{Block: bst, RelationCounts: map[Relation]int{}}

	// --prior: skip pairs a previous wave already judged (doc 90 ruling 5). The
	// key is the unordered (source,name) x (source,name) identity blocking
	// on, so a pair re-surfacing across waves is judged exactly once. Only pairs
	// are filtered: a previously judged single is either already mapped (and so
	// excluded from the pool upstream) or errored (and must be re-judged).
	if opts.Prior != "" {
		priorKeys, err := loadPriorPairKeys(opts.Prior)
		if err != nil {
			return nil, fmt.Errorf("load prior verdicts: %w", err)
		}
		kept := pairs[:0]
		for _, p := range pairs {
			if _, judged := priorKeys[pairKey(p.A.SourceKey, p.A.Name, p.B.SourceKey, p.B.Name)]; judged {
				st.SkippedPrior++
				continue
			}
			kept = append(kept, p)
		}
		pairs = kept
		slog.Info("tag-pair prior skip", "prior", opts.Prior, "skipped", st.SkippedPrior, "remaining", len(pairs))
	}

	var mu sync.Mutex
	var recs []pairRec

	// ── pair matching ─────────────────────────────────────────────────────────
	runPooled(ctx, len(pairs), opts.Workers, func(i int) {
		p := pairs[i]
		v, model, err := mt.MatchPair(ctx, PairInput{
			ASourceKey: p.A.SourceKey, AName: p.A.Name, AOrig: p.A.Orig, AUsage: p.A.Usage,
			BSourceKey: p.B.SourceKey, BName: p.B.Name, BOrig: p.B.Orig, BUsage: p.B.Usage,
		})
		rec := pairRec{
			Kind:    "pair",
			ASource: p.A.SourceKey, AName: p.A.Name, AOrig: p.A.Orig, AUsage: p.A.Usage,
			BSource: p.B.SourceKey, BName: p.B.Name, BOrig: p.B.Orig, BUsage: p.B.Usage,
			Block: p.Block,
		}
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			st.Errors++
			slog.Warn("match pair", "a", p.A.Name, "b", p.B.Name, "err", err)
			return
		}
		rec.Relation = string(v.Relation)
		rec.Confidence = v.Confidence
		rec.Reason = v.Reason
		rec.Model = model
		st.RelationCounts[v.Relation]++
		recs = append(recs, rec)
	})
	st.Pairs = len(pairs)

	// ── single-source admission (usage ≥ threshold) ──────────────────────────
	// Every ≥threshold name is proposed, INCLUDING names that also appear in an
	// exact pair. We do NOT suppress those here on the pair verdict: an exact
	// pair only canonicalizes the name if it survives review (a low/medium-exact
	// pair can be dropped or human-rejected), and suppressing early would leave a
	// high-usage name with no canonical row at all. Precedence is resolved at
	// apply time — apply-reviewed's `absorbed` set drops a single row only when
	// an APPROVED exact group actually covers the name (group membership wins).
	singles := make([]candName, 0)
	for _, c := range pool {
		if c.Usage < opts.SingleThreshold {
			continue
		}
		singles = append(singles, c)
	}
	runPooled(ctx, len(singles), opts.Workers, func(i int) {
		c := singles[i]
		v, model, err := mt.ClassifyName(ctx, NameInput{SourceKey: c.SourceKey, Name: c.Name, Orig: c.Orig, Usage: c.Usage})
		rec := pairRec{Kind: "single", Source: c.SourceKey, Name: c.Name, Orig: c.Orig, Usage: c.Usage}
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			st.Errors++
			slog.Warn("classify name", "name", c.Name, "err", err)
			return
		}
		rec.Tier = i16p(v.Tier)
		rec.Kind_ = i16p(v.Kind)
		rec.Confidence = v.Confidence
		rec.Reason = v.Reason
		rec.Model = model
		recs = append(recs, rec)
		st.SingleProposed++
	})

	sortRecords(recs)
	if err := writeRecords(opts.Out, recs); err != nil {
		return nil, fmt.Errorf("write verdicts: %w", err)
	}
	slog.Info("tag-pair propose done", "pairs", st.Pairs, "singles", st.SingleProposed,
		"errors", st.Errors, "out", opts.Out)
	return st, nil
}

// buildPool assembles the enriched candidate pool: the three source vocabularies
// (reusing the 74 loaders + bgm junk prefilter), MINUS junk and names already in
// a canonical group (catalog_tag_source_map), each enriched with its original
// form and (bgm/dlsite) work-id set.
func buildPool(ctx context.Context, db *gorm.DB, opts ProposeOpts) ([]candName, error) {
	src, err := resolveSources(ctx, db)
	if err != nil {
		return nil, err
	}
	srcKeys := map[int16]string{src.vndb: sourceKeyVNDB, src.bangumi: sourceKeyBangumi, src.dlsite: sourceKeyDlsite}

	vndb, err := loadWorkTagVocab(ctx, db, src.vndb)
	if err != nil {
		return nil, err
	}
	bgm, err := loadWorkTagVocab(ctx, db, src.bangumi)
	if err != nil {
		return nil, err
	}
	dl, err := loadWorkTagVocab(ctx, db, src.dlsite)
	if err != nil {
		return nil, err
	}
	labelNorms, err := loadLabelNorms(ctx, db)
	if err != nil {
		return nil, err
	}
	mapped, err := loadMappedNames(ctx, db) // "sourceID\x00name" already canonical
	if err != nil {
		return nil, err
	}

	// original-name enrichers
	vndbOrig := loadVndbOrig(opts.TagMapPath)
	dlOrig, err := loadDlsiteOrig(ctx, opts.DlsiteDSN)
	if err != nil {
		return nil, err
	}
	// co-occurrence work-id sets (bgm/dlsite share the catalog_work id space)
	bgmWorks, err := loadNameWorkIDs(ctx, db, src.bangumi)
	if err != nil {
		return nil, err
	}
	dlWorks, err := loadNameWorkIDs(ctx, db, src.dlsite)
	if err != nil {
		return nil, err
	}

	pool := make([]candName, 0, len(vndb)+len(bgm)+len(dl))
	add := func(e vocabEntry, orig func(string) string, works map[string]map[int64]struct{}) {
		if _, already := mapped[mapKey(e.SourceID, e.Name)]; already {
			return // already in a 74 canonical group — settled, out of scope
		}
		c := candName{
			SourceID: e.SourceID, SourceKey: srcKeys[e.SourceID],
			Name: e.Name, Norm: e.Norm, Usage: e.Usage,
			Orig: e.Name,
		}
		if orig != nil {
			if o := orig(e.Name); o != "" {
				c.Orig = o
			}
		}
		if works != nil {
			c.workIDs = works[e.Name]
		}
		pool = append(pool, c)
	}
	for _, e := range vndb {
		add(e, vndbOrig, nil)
	}
	for i := range bgm {
		if bgm[i].Junk || bgmJunk(bgm[i].Norm, labelNorms) != "" {
			continue
		}
		add(bgm[i], nil, bgmWorks) // bgm orig = name (folksonomy is already original)
	}
	for _, e := range dl {
		add(e, dlOrig, dlWorks)
	}
	// deterministic pool order
	sort.Slice(pool, func(i, j int) bool {
		if pool[i].SourceID != pool[j].SourceID {
			return pool[i].SourceID < pool[j].SourceID
		}
		return pool[i].Name < pool[j].Name
	})
	return pool, nil
}

// loadMappedNames returns the set of (source_id, source_name) already carried by
// catalog_tag_source_map (the 74 canonical layer) — those names are settled and
// excluded from 70b pairing.
func loadMappedNames(ctx context.Context, db *gorm.DB) (map[string]struct{}, error) {
	var rows []struct {
		SourceID   int16  `gorm:"column:source_id"`
		SourceName string `gorm:"column:source_name"`
	}
	if err := db.WithContext(ctx).Raw(`SELECT source_id, source_name FROM catalog_tag_source_map`).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load mapped names: %w", err)
	}
	out := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		out[mapKey(r.SourceID, r.SourceName)] = struct{}{}
	}
	return out, nil
}

func mapKey(source int16, name string) string { return fmt.Sprintf("%d\x00%s", source, name) }

// loadNameWorkIDs returns, per catalog_work_tag name of a source, the set of
// work ids carrying it (co-occurrence blocking input; bgm/dlsite only).
func loadNameWorkIDs(ctx context.Context, db *gorm.DB, sourceID int16) (map[string]map[int64]struct{}, error) {
	var rows []struct {
		Name   string `gorm:"column:name"`
		WorkID int64  `gorm:"column:work_id"`
	}
	if err := db.WithContext(ctx).Raw(`SELECT name, work_id FROM catalog_work_tag WHERE source_id = ?`, sourceID).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load name work ids (source %d): %w", sourceID, err)
	}
	out := map[string]map[int64]struct{}{}
	for _, r := range rows {
		name := strings.TrimSpace(r.Name)
		if name == "" {
			continue
		}
		s := out[name]
		if s == nil {
			s = map[int64]struct{}{}
			out[name] = s
		}
		s[r.WorkID] = struct{}{}
	}
	return out, nil
}

// loadVndbOrig builds the zh→EN reverse map from docs/tagMap.ts (the exact
// function vndbresolve used to localize galgame_tag.name). Collisions (several
// English tags mapping to one Chinese) keep the lexicographically smallest
// English for determinism. Returns a lookup that falls back to the name itself
// (an unmapped tag already carries its English original).
func loadVndbOrig(tagMapPath string) func(string) string {
	path := tagMapPath
	if path == "" {
		path = vndbresolve.DefaultTagMapPath()
	}
	m, err := vndbresolve.ParseTagMap(path)
	if err != nil {
		slog.Warn("tag-pair: tagMap unreadable — vndb originals fall back to zh names", "path", path, "err", err)
		return func(s string) string { return s }
	}
	inv := make(map[string]string, len(m))
	for eng, zh := range m {
		if cur, ok := inv[zh]; !ok || eng < cur {
			inv[zh] = eng
		}
	}
	return func(zh string) string {
		if eng, ok := inv[zh]; ok {
			return eng
		}
		return zh
	}
}

// loadDlsiteOrig connects the optional dlsite staging DB and returns a zh→ja
// genre-name lookup (genre_taxonomy zh_CN → genre_id → ja_JP). Blank DSN → a
// pass-through (dlsite orig = zh name). This is the ONLY use of a second pool,
// and only in propose (apply is single-DSN).
func loadDlsiteOrig(ctx context.Context, dsn string) (func(string) string, error) {
	if dsn == "" {
		return func(s string) string { return s }, nil
	}
	db, err := openGorm(dsn)
	if err != nil {
		return nil, fmt.Errorf("connect dlsite staging db: %w", err)
	}
	defer func() {
		if sqlDB, e := db.DB(); e == nil {
			sqlDB.Close()
		}
	}()
	var rows []struct {
		Zh string `gorm:"column:zh"`
		Ja string `gorm:"column:ja"`
	}
	if err := db.WithContext(ctx).Raw(`
		SELECT z.name AS zh, j.name AS ja
		FROM genre_taxonomy z
		JOIN genre_taxonomy j ON j.genre_id = z.genre_id AND j.locale = 'ja_JP'
		WHERE z.locale = 'zh_CN'`).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load dlsite genre taxonomy: %w", err)
	}
	zhToJa := make(map[string]string, len(rows))
	for _, r := range rows {
		zh := strings.TrimSpace(r.Zh)
		ja := strings.TrimSpace(r.Ja)
		if zh != "" && ja != "" {
			zhToJa[zh] = ja
		}
	}
	slog.Info("tag-pair: loaded dlsite ja originals", "genres", len(zhToJa))
	return func(zh string) string {
		if ja, ok := zhToJa[zh]; ok {
			return ja
		}
		return zh
	}, nil
}

// runPooled runs fn(i) for i in [0,n) either serially (workers<=1) or across a
// worker pool. fn is responsible for its own locking. Order of execution is
// irrelevant (records are sorted before writing).
func runPooled(ctx context.Context, n, workers int, fn func(i int)) {
	if workers <= 1 {
		for i := range n {
			if ctx.Err() != nil {
				return
			}
			fn(i)
		}
		return
	}
	ch := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for i := range ch {
				if ctx.Err() != nil {
					continue
				}
				fn(i)
			}
		})
	}
	for i := range n {
		if ctx.Err() != nil {
			break
		}
		ch <- i
	}
	close(ch)
	wg.Wait()
}

// sortRecords orders records deterministically (kind, then keys) so the JSONL is
// stable across runs regardless of worker scheduling.
func sortRecords(recs []pairRec) {
	sort.SliceStable(recs, func(i, j int) bool {
		a, b := recs[i], recs[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Kind == "pair" {
			if a.ASource != b.ASource {
				return a.ASource < b.ASource
			}
			if a.AName != b.AName {
				return a.AName < b.AName
			}
			if a.BSource != b.BSource {
				return a.BSource < b.BSource
			}
			return a.BName < b.BName
		}
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		return a.Name < b.Name
	})
}

// openGorm opens a silent-logger gorm handle (shared by pair/apply phases).
func openGorm(dsn string) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
}

// pairKey is the unordered pair identity used by --prior skipping: the same
// (source,name) dedupe key blocking uses, joined order-independently.
func pairKey(aSource, aName, bSource, bName string) string {
	ka := aSource + ":" + aName
	kb := bSource + ":" + bName
	if ka > kb {
		ka, kb = kb, ka
	}
	return ka + "\x00" + kb
}

// loadPriorPairKeys reads a previous wave's verdict/decisions JSONL and returns
// the set of already-judged pair keys. Non-pair records are ignored. A missing
// or unreadable file is a hard error — falling back to a full re-judge is an
// operator decision (re-run without --prior), never a silent one.
func loadPriorPairKeys(path string) (map[string]struct{}, error) {
	recs, err := readRecords(path)
	if err != nil {
		return nil, err
	}
	out := make(map[string]struct{}, len(recs))
	for _, r := range recs {
		if r.Kind != "pair" {
			continue
		}
		out[pairKey(r.ASource, r.AName, r.BSource, r.BName)] = struct{}{}
	}
	return out, nil
}
