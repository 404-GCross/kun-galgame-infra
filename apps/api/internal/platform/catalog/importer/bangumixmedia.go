package importer

import (
	"strconv"

	"api/internal/platform/catalog/model"

	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Bangumi cross-media registration wave (step 31, doc 17 C-03). Step 30 imported
// the game↔game relation graph and deliberately skipped the cross-media
// adaptation edge (relation_type 1, 改编) because its target medium was not yet
// registered. This wave registers the anime/manga/novel subjects REACHABLE from
// an exact-anchored galgame via the 改编 relation, then lays the adaptation edge.
//
// Evidence gate (step-28 discipline — only a corroborated subset, no blind
// wave): the subject on the other end of a 改编 relation from an anchored galgame.
// Bangumi stores 改编 bidirectionally with the same id on both sides (verified:
// 775/781 rows carry a mirror), so the row direction does NOT encode which work
// is the original — we take the canonical galgame→cross-media direction (the
// registered anime/manga/novel is the adaptation OF the galgame), matching the
// step's CLANNAD/Fate anchoring. (A minority of galgames are themselves based on
// a novel; the symmetric 改编 gives no signal to reverse those, so they default
// to the common direction — a documented limitation.)
//
// Registration is identity-level only: work + titles + a bid self-anchor + an
// adaptation edge. NO credits/characters/persons — those belong to each
// medium's own product wave (same "归属后置" rationale as step 14).

// xmediaMedium routes a Bangumi (subject_type, platform) to a catalog medium.
// type 2 (anime) → anime; type 1 (book) by platform: 1001 漫画 → manga, 1002
// 小说 → novel. Every other book platform (1003 画集 / 1004 绘本 / 1005 写真 /
// 1006 公式书 / 0 其他) is a non-adaptation merchandise book — skipped, not
// force-fit into manga/novel.
func xmediaMedium(stype, platform int) (int16, bool) {
	switch stype {
	case 2:
		return mediumAnime, true
	case 1:
		switch platform {
		case 1001:
			return mediumManga, true
		case 1002:
			return mediumNovel, true
		}
	}
	return 0, false
}

const (
	mediumManga int16 = 2
	mediumNovel int16 = 3
	mediumAnime int16 = 4
)

// XmediaStats is the wave tally.
type XmediaStats struct {
	RegisteredAnime int
	RegisteredManga int
	RegisteredNovel int
	Edges           int // NEW adaptation edges written
	EdgesWritten    int // actual inserts (== Edges on --run; 0 on dry)
	AlreadyEdge     int // adaptation edge already present (idempotent)
	AlreadyWork     int // the cross-media subject already carries a bangumi anchor
	SkippedPlatform int // a book whose platform is not manga/novel
	SkippedNoTitle  int // subject has no name at all (cannot register)
	Errors          int
}

// xmediaSubject is a cross-media subject to register.
type xmediaSubject struct {
	sid      int64
	stype    int
	platform int
	name     string
	nameCN   string
	medium   int16
}

// RunBangumiXmedia registers the anime/manga/novel subjects reachable from an
// anchored galgame via the 改编 relation and lays the adaptation edges.
// Idempotent (bid self-anchor + ON CONFLICT edges); dry by default.
func (im *Importer) RunBangumiXmedia() (XmediaStats, error) {
	var st XmediaStats

	galgameAnchor, allAnchor, err := im.loadBangumiAnchorsByMedium()
	if err != nil {
		return st, err
	}

	// The 改编 rows touching an anchored galgame → (galgame work, cross-media sid).
	var rows []struct {
		Subject int64 `gorm:"column:subject_id"`
		Related int64 `gorm:"column:related_subject_id"`
	}
	if err := im.catalog.Raw(`SELECT subject_id, related_subject_id
		FROM src_bangumi.subject_relation WHERE relation_type = 1`).Scan(&rows).Error; err != nil {
		return st, err
	}
	pairs := make(map[[2]int64]struct{}) // (galgameWork, xSid)
	xSids := make(map[int64]struct{})
	consider := func(gW, xSid int64) {
		pairs[[2]int64{gW, xSid}] = struct{}{}
		xSids[xSid] = struct{}{}
	}
	for _, r := range rows {
		if gW, ok := galgameAnchor[r.Subject]; ok {
			consider(gW, r.Related)
		}
		if gW, ok := galgameAnchor[r.Related]; ok {
			consider(gW, r.Subject)
		}
	}

	// Load the candidate subjects; keep only the anime/book ones with a medium.
	subs, err := im.loadXmediaSubjects(xSids)
	if err != nil {
		return st, err
	}

	// Register the fresh cross-media works; resolve every candidate to a work id.
	xWork := make(map[int64]int64, len(subs)) // xSid → work id (registered or already)
	var toRegister []xmediaSubject
	for _, s := range subs {
		if w, ok := allAnchor[s.sid]; ok {
			st.AlreadyWork++
			xWork[s.sid] = w
			continue
		}
		medium, ok := xmediaMedium(s.stype, s.platform)
		if !ok {
			st.SkippedPlatform++
			continue
		}
		if s.name == "" && s.nameCN == "" {
			st.SkippedNoTitle++
			continue
		}
		s.medium = medium
		toRegister = append(toRegister, s)
	}

	if !im.dryRun {
		if err := im.registerXmedia(toRegister, xWork, &st); err != nil {
			return st, err
		}
	} else {
		// Dry: give each would-be work a distinct placeholder id so the edge
		// count below is accurate (a real re-run assigns real ids).
		for i, s := range toRegister {
			countMedium(&st, s.medium)
			xWork[s.sid] = int64(-(i + 1))
		}
	}

	// Adaptation edges: the cross-media work is the adaptation OF the galgame.
	existing, err := im.loadExistingWorkRelations()
	if err != nil {
		return st, err
	}
	src := bangumiSource
	var edges []model.CatalogWorkRelation
	for p := range pairs {
		gW, xSid := p[0], p[1]
		xW, ok := xWork[xSid]
		if !ok {
			continue // the subject was skipped (not anime/book, or platform-skipped)
		}
		key := [3]int64{xW, gW, relAdaptationOf}
		if _, in := existing[key]; in {
			st.AlreadyEdge++
			continue
		}
		existing[key] = struct{}{}
		edges = append(edges, model.CatalogWorkRelation{
			AWorkID: xW, BWorkID: gW, RelationTypeID: relAdaptationOf, SourceID: &src,
		})
	}
	st.Edges = len(edges)
	if im.dryRun || len(edges) == 0 {
		return st, nil
	}
	res := im.catalog.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(&edges, 1000)
	if res.Error != nil {
		return st, res.Error
	}
	st.EdgesWritten = int(res.RowsAffected)
	st.AlreadyEdge += st.Edges - st.EdgesWritten
	// Edges here are the planned-NEW set (stored pairs were filtered out above),
	// and the galgame end is a pre-existing work that just gained an adaptation
	// link. A re-run plans no edges and returns before this point.
	if err := touchWorks(im.catalog, relationEndpoints(edges)); err != nil {
		return st, err
	}
	return st, nil
}

func countMedium(st *XmediaStats, medium int16) {
	switch medium {
	case mediumAnime:
		st.RegisteredAnime++
	case mediumManga:
		st.RegisteredManga++
	case mediumNovel:
		st.RegisteredNovel++
	}
}

// registerXmedia creates each cross-media work + its titles + a bid self-anchor
// + an imported revision, chunked, and fills xWork with the new ids.
func (im *Importer) registerXmedia(subs []xmediaSubject, xWork map[int64]int64, st *XmediaStats) error {
	const chunk = 1000
	for start := 0; start < len(subs); start += chunk {
		end := min(start+chunk, len(subs))
		batch := subs[start:end]
		if err := im.catalog.Transaction(func(tx *gorm.DB) error {
			works := make([]model.CatalogWork, len(batch))
			for i, s := range batch {
				name := s.name
				if name == "" {
					name = s.nameCN
				}
				works[i] = model.CatalogWork{
					MediumID: s.medium, OLang: "ja", DisplayName: name,
					ContentRating: 0, Status: model.WorkStatusLive,
					Extra: datatypes.JSON(`{}`), FieldProvenance: datatypes.JSON(`{}`),
				}
			}
			if err := tx.CreateInBatches(works, 1000).Error; err != nil {
				return err
			}

			var titles []model.CatalogWorkTitle
			var refs []model.CatalogExternalRef
			var revs []model.CatalogRevision
			for i, s := range batch {
				wid := works[i].ID
				xWork[s.sid] = wid
				countMedium(st, s.medium)
				if s.name != "" {
					titles = append(titles, model.CatalogWorkTitle{WorkID: wid, Lang: "ja", Title: s.name, Kind: model.WorkTitleKindOfficial})
				}
				if s.nameCN != "" && s.nameCN != s.name {
					titles = append(titles, model.CatalogWorkTitle{WorkID: wid, Lang: "zh", Title: s.nameCN, Kind: model.WorkTitleKindOfficial})
				}
				refs = append(refs, selfRef(model.EntityTypeWork, wid, bangumiSource, strconv.FormatInt(s.sid, 10), ruleBangumiXmedia))
				revs = append(revs, importedRev(model.EntityTypeWork, wid, workSnapshotJSON(works[i], nil)))
			}
			if err := tx.CreateInBatches(titles, 1000).Error; err != nil {
				return err
			}
			return im.batchRefsRevs(tx, refs, revs)
		}); err != nil {
			return err
		}
	}
	return nil
}

// loadBangumiAnchorsByMedium returns the exact bangumi work anchors split into
// the galgame-only map (the gate's galgame side) and the full map (to detect a
// cross-media subject that is already registered).
func (im *Importer) loadBangumiAnchorsByMedium() (galgame, all map[int64]int64, err error) {
	var rows []struct {
		Ext    string `gorm:"column:external_id"`
		Wid    int64  `gorm:"column:entity_id"`
		Medium int16  `gorm:"column:medium_id"`
	}
	if err := im.catalog.Raw(`SELECT r.external_id, r.entity_id, w.medium_id
		FROM catalog_external_ref r JOIN catalog_work w ON w.id = r.entity_id
		WHERE r.source_id = ? AND r.entity_type = ? AND r.link_kind = ?`,
		bangumiSource, model.EntityTypeWork, model.LinkKindExact).Scan(&rows).Error; err != nil {
		return nil, nil, err
	}
	galgame = make(map[int64]int64)
	all = make(map[int64]int64, len(rows))
	for _, r := range rows {
		sid, e := strconv.ParseInt(r.Ext, 10, 64)
		if e != nil {
			continue
		}
		all[sid] = r.Wid
		if r.Medium == mediumGalgame {
			galgame[sid] = r.Wid
		}
	}
	return galgame, all, nil
}

// loadXmediaSubjects loads the type-1/2 (book/anime) subjects among the
// candidate ids.
func (im *Importer) loadXmediaSubjects(sids map[int64]struct{}) ([]xmediaSubject, error) {
	if len(sids) == 0 {
		return nil, nil
	}
	ids := make([]int64, 0, len(sids))
	for id := range sids {
		ids = append(ids, id)
	}
	var rows []struct {
		ID       int64  `gorm:"column:id"`
		Type     int    `gorm:"column:type"`
		Platform int    `gorm:"column:platform"`
		Name     string `gorm:"column:name"`
		NameCN   string `gorm:"column:name_cn"`
	}
	if err := im.catalog.Raw(`SELECT id, type, platform, name, name_cn
		FROM src_bangumi.subject WHERE id IN ? AND type IN (1,2)`, ids).Scan(&rows).Error; err != nil {
		return nil, err
	}
	subs := make([]xmediaSubject, len(rows))
	for i, r := range rows {
		subs[i] = xmediaSubject{sid: r.ID, stype: r.Type, platform: r.Platform, name: r.Name, nameCN: r.NameCN}
	}
	return subs, nil
}
