package service

import (
	"context"
	"encoding/json"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/repository"

	"gorm.io/gorm"
)

// NextMoe open-API public taxonomy projection (W1b). PublicTaxonomyService maps
// the four taxonomy families (tags / officials / engines / series) to the FROZEN
// curated public DTOs, reusing the existing taxonomy repositories (the internal
// bridge handlers are untouched). It borrows the galgame service for the two
// places that embed thin galgame items — the tag-intersection face and the
// series member preview — so those hydrate through the same batched (non-N+1)
// pin loader the aggregate list/search use.
type PublicTaxonomyService struct {
	tagRepo      *repository.TagRepository
	officialRepo *repository.OfficialRepository
	engineRepo   *repository.EngineRepository
	seriesRepo   *repository.SeriesRepository
	galgame      *GalgameService
}

// NewPublicTaxonomyService wires the public taxonomy projection service.
func NewPublicTaxonomyService(
	tagRepo *repository.TagRepository,
	officialRepo *repository.OfficialRepository,
	engineRepo *repository.EngineRepository,
	seriesRepo *repository.SeriesRepository,
	galgame *GalgameService,
) *PublicTaxonomyService {
	return &PublicTaxonomyService{
		tagRepo:      tagRepo,
		officialRepo: officialRepo,
		engineRepo:   engineRepo,
		seriesRepo:   seriesRepo,
		galgame:      galgame,
	}
}

// PublicThinItemsFromRows builds thin public items from already-loaded galgame
// rows in ONE batched pin-loading pass (non-N+1) — the same projection the
// aggregate list/search use, exposed for the taxonomy faces (tag multi + series
// member previews) that already hold the rows. Returns items in row order,
// 1:1 with rows.
func (s *GalgameService) PublicThinItemsFromRows(ctx context.Context, rows []model.Galgame, contentLimit string, inc PublicItemInclude) []dto.PublicGalgameItem {
	return s.thinItems(ctx, rows, contentLimit, inc)
}

// ─────────────────────────── Tags ───────────────────────────

// TagList returns a page of curated tag records + the filtered total. contentLimit
// gates the tag CATEGORIES (sfw hides the sexual set), resolved by the caller via
// the three-state P5 gate — a no-scope key silently stays sfw.
func (s *PublicTaxonomyService) TagList(ctx context.Context, page, limit int, contentLimit string) (dto.PublicTagListData, error) {
	rows, total, err := s.tagRepo.List(ctx, page, limit, contentLimit)
	if err != nil {
		return dto.PublicTagListData{}, err
	}
	items := make([]dto.PublicTagEntity, len(rows))
	for i := range rows {
		items[i] = projectTagEntity(&rows[i])
	}
	return dto.PublicTagListData{Items: items, Total: total}, nil
}

// Tag returns one tag's curated record by id. found=false (→ 404) when the id is
// unknown.
func (s *PublicTaxonomyService) Tag(ctx context.Context, id int) (dto.PublicTagEntity, bool, error) {
	t, err := s.tagRepo.FindByID(ctx, id)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return dto.PublicTagEntity{}, false, nil
		}
		return dto.PublicTagEntity{}, false, err
	}
	return projectTagEntity(t), true, nil
}

// TagGalgameIDs returns the ids of every published galgame carrying this tag.
func (s *PublicTaxonomyService) TagGalgameIDs(ctx context.Context, id int) ([]int, error) {
	ids, err := s.tagRepo.GalgameIDsByTagID(ctx, id)
	if err != nil {
		return nil, err
	}
	if ids == nil {
		ids = []int{}
	}
	return ids, nil
}

// TagMulti returns the published galgames carrying ALL given tag ids, projected
// to thin /v1 items + the filtered total. contentLimit gates the rows (and drops
// NSFW pins on the sfw face). Empty ids → empty page.
func (s *PublicTaxonomyService) TagMulti(ctx context.Context, ids []int, page, limit int, contentLimit string, inc PublicItemInclude) (dto.PublicItemListData, error) {
	if len(ids) == 0 {
		return dto.PublicItemListData{Items: []dto.PublicGalgameItem{}, Total: 0}, nil
	}
	rows, total, err := s.tagRepo.FindGalgamesByMultipleTags(ctx, ids, page, limit, contentLimit)
	if err != nil {
		return dto.PublicItemListData{}, err
	}
	items := s.galgame.PublicThinItemsFromRows(ctx, rows, contentLimit, inc)
	return dto.PublicItemListData{Items: items, Total: total}, nil
}

// ─────────────────────────── Officials ───────────────────────────

// OfficialList returns a page of curated maker records + the total.
func (s *PublicTaxonomyService) OfficialList(ctx context.Context, page, limit int) (dto.PublicOfficialListData, error) {
	rows, total, err := s.officialRepo.List(ctx, page, limit)
	if err != nil {
		return dto.PublicOfficialListData{}, err
	}
	items := make([]dto.PublicOfficialEntity, len(rows))
	for i := range rows {
		items[i] = projectOfficialEntity(&rows[i])
	}
	return dto.PublicOfficialListData{Items: items, Total: total}, nil
}

// Official returns one maker's curated record by id.
func (s *PublicTaxonomyService) Official(ctx context.Context, id int) (dto.PublicOfficialEntity, bool, error) {
	o, err := s.officialRepo.FindByID(ctx, id)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return dto.PublicOfficialEntity{}, false, nil
		}
		return dto.PublicOfficialEntity{}, false, err
	}
	return projectOfficialEntity(o), true, nil
}

// OfficialGalgameIDs returns the ids of every published galgame under this maker.
func (s *PublicTaxonomyService) OfficialGalgameIDs(ctx context.Context, id int) ([]int, error) {
	ids, err := s.officialRepo.GalgameIDsByOfficialID(ctx, id)
	if err != nil {
		return nil, err
	}
	if ids == nil {
		ids = []int{}
	}
	return ids, nil
}

// ─────────────────────────── Engines ───────────────────────────

// EngineList returns a page of curated engine records + the total.
func (s *PublicTaxonomyService) EngineList(ctx context.Context, page, limit int) (dto.PublicEngineListData, error) {
	rows, total, err := s.engineRepo.PublicEngineListPaged(ctx, page, limit)
	if err != nil {
		return dto.PublicEngineListData{}, err
	}
	items := make([]dto.PublicEngineEntity, len(rows))
	for i := range rows {
		items[i] = projectEngineEntity(&rows[i])
	}
	return dto.PublicEngineListData{Items: items, Total: total}, nil
}

// Engine returns one engine's curated record by id.
func (s *PublicTaxonomyService) Engine(ctx context.Context, id int) (dto.PublicEngineEntity, bool, error) {
	e, err := s.engineRepo.FindByID(ctx, id)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return dto.PublicEngineEntity{}, false, nil
		}
		return dto.PublicEngineEntity{}, false, err
	}
	return projectEngineEntity(e), true, nil
}

// EngineGalgameIDs returns the ids of every published galgame on this engine.
func (s *PublicTaxonomyService) EngineGalgameIDs(ctx context.Context, id int) ([]int, error) {
	ids, err := s.engineRepo.GalgameIDsByEngineID(ctx, id)
	if err != nil {
		return nil, err
	}
	if ids == nil {
		ids = []int{}
	}
	return ids, nil
}

// ─────────────────────────── Series ───────────────────────────

// SeriesList returns a page of curated series records + the total. Each record
// embeds a member preview (thin items, capped by the repo) hydrated in ONE
// batched pass across the whole page (non-N+1). galgame_count and the preview are
// both content_limit-gated (they stay in sync, mirroring the bridge).
func (s *PublicTaxonomyService) SeriesList(ctx context.Context, page, limit int, contentLimit string, inc PublicItemInclude) (dto.PublicSeriesListData, error) {
	rows, total, err := s.seriesRepo.List(ctx, page, limit, contentLimit)
	if err != nil {
		return dto.PublicSeriesListData{}, err
	}

	// Gather every preview galgame across the page, hydrate once (non-N+1), then
	// slice the thin items back per series (thinItems preserves row order 1:1).
	var combined []model.Galgame
	counts := make([]int, len(rows))
	for i := range rows {
		counts[i] = len(rows[i].Galgame)
		combined = append(combined, rows[i].Galgame...)
	}
	previews := s.galgame.PublicThinItemsFromRows(ctx, combined, contentLimit, inc)

	items := make([]dto.PublicSeriesEntity, len(rows))
	off := 0
	for i := range rows {
		n := counts[i]
		items[i] = dto.PublicSeriesEntity{
			ID:           rows[i].ID,
			Name:         rows[i].Name,
			Description:  rows[i].Description,
			GalgameCount: rows[i].GalgameCount,
			Galgames:     sliceItems(previews, off, n),
			Created:      fmtPublicTS(rows[i].Created),
			Updated:      fmtPublicTS(rows[i].Updated),
		}
		off += n
	}
	return dto.PublicSeriesListData{Items: items, Total: total}, nil
}

// Series returns one series' curated record by id, with the full gated member
// set projected to thin items. galgame_count is the gated member count (the
// detail returns every gated member, so len == the content_limit-filtered count,
// matching the list's count semantics).
func (s *PublicTaxonomyService) Series(ctx context.Context, id int, contentLimit string, inc PublicItemInclude) (dto.PublicSeriesEntity, bool, error) {
	series, err := s.seriesRepo.FindByID(ctx, id, contentLimit)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return dto.PublicSeriesEntity{}, false, nil
		}
		return dto.PublicSeriesEntity{}, false, err
	}
	previews := s.galgame.PublicThinItemsFromRows(ctx, series.Galgame, contentLimit, inc)
	return dto.PublicSeriesEntity{
		ID:           series.ID,
		Name:         series.Name,
		Description:  series.Description,
		GalgameCount: len(previews),
		Galgames:     previews,
		Created:      fmtPublicTS(series.Created),
		Updated:      fmtPublicTS(series.Updated),
	}, true, nil
}

// ─────────────────────────── mappers ───────────────────────────

func projectTagEntity(t *model.GalgameTag) dto.PublicTagEntity {
	return dto.PublicTagEntity{
		ID:           t.ID,
		Name:         t.Name,
		Category:     t.Category,
		Description:  t.Description,
		Aliases:      tagAliasNames(t.Alias),
		GalgameCount: t.GalgameCount,
		Created:      fmtPublicTS(t.Created),
		Updated:      fmtPublicTS(t.Updated),
	}
}

func projectOfficialEntity(o *model.GalgameOfficial) dto.PublicOfficialEntity {
	return dto.PublicOfficialEntity{
		ID:           o.ID,
		Name:         o.Name,
		Category:     o.Category,
		Original:     o.Original,
		Link:         o.Link,
		Lang:         o.Lang,
		Description:  o.Description,
		Aliases:      officialAliasNames(o.Alias),
		GalgameCount: o.GalgameCount,
	}
}

func projectEngineEntity(e *model.GalgameEngine) dto.PublicEngineEntity {
	return dto.PublicEngineEntity{
		ID:           e.ID,
		Name:         e.Name,
		Description:  e.Description,
		Alias:        engineAliasNames(e.Alias),
		GalgameCount: e.GalgameCount,
	}
}

// tagAliasNames / officialAliasNames project a preloaded alias slice to a plain
// []string (always non-nil so the JSON key is [] not null).
func tagAliasNames(aliases []model.GalgameTagAlias) []string {
	out := make([]string, 0, len(aliases))
	for i := range aliases {
		out = append(out, aliases[i].Name)
	}
	return out
}

func officialAliasNames(aliases []model.GalgameOfficialAlias) []string {
	out := make([]string, 0, len(aliases))
	for i := range aliases {
		out = append(out, aliases[i].Name)
	}
	return out
}

// engineAliasNames parses the engine's inline jsonb alias array to []string
// (always non-nil; a malformed / absent column yields []).
func engineAliasNames(raw []byte) []string {
	out := []string{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	if out == nil {
		out = []string{}
	}
	return out
}

// sliceItems returns items[off:off+n] as a non-nil slice (so an empty member set
// serializes as [] rather than null).
func sliceItems(items []dto.PublicGalgameItem, off, n int) []dto.PublicGalgameItem {
	out := make([]dto.PublicGalgameItem, 0, n)
	if n > 0 && off+n <= len(items) {
		out = append(out, items[off:off+n]...)
	}
	return out
}
