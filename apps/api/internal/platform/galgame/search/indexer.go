package search

import (
	"context"
	"strconv"
	"strings"
	"time"

	"api/internal/infrastructure/search"
	"api/internal/platform/galgame/model"
)

// Indexer maps GORM models to Meilisearch documents and pushes upserts/deletes.
type Indexer struct {
	client *search.Client
}

// NewIndexer creates a new Indexer.
func NewIndexer(client *search.Client) *Indexer {
	return &Indexer{client: client}
}

// ─────────────────────────── Galgame ───────────────────────────

// ToGalgameDoc converts a galgame model (with relations preloaded) into a doc.
//
// The model must have Alias, Tag.Tag, Official.Official, Engine.Engine, Cover
// preloaded. Series preload is optional — we only use series_id scalar.
// Cover is required for the effective_banner_hash derivation; missing the
// preload silently empties that field rather than failing.
func ToGalgameDoc(g *model.Galgame) *GalgameDoc {
	doc := &GalgameDoc{
		ID:               g.ID,
		VNDBID:           g.VNDBID,
		NameZhCN:         g.NameZhCN,
		NameJaJP:         g.NameJaJP,
		NameEnUS:         g.NameEnUS,
		NameZhTW:         g.NameZhTW,
		IntroZhCN:        g.IntroZhCN,
		IntroJaJP:        g.IntroJaJP,
		IntroEnUS:        g.IntroEnUS,
		IntroZhTW:        g.IntroZhTW,
		Banner:           g.Banner,
		ContentLimit:     g.ContentLimit,
		AgeLimit:         g.AgeLimit,
		OriginalLanguage: g.OriginalLanguage,
		Status:           g.Status,
		UserID:           g.UserID,
		View:             g.View,
		ReleaseDate:      formatGalgameReleaseDate(g.ReleaseDate),
		ReleaseDateTBA:   g.ReleaseDateTBA,
		UpdatedTS:        g.Updated.Time().Unix(),
		CreatedTS:        g.Created.Time().Unix(),
	}
	if g.BangumiID != nil {
		doc.BID = g.BangumiID
	}
	if g.SeriesID != nil {
		doc.SeriesID = g.SeriesID
	}

	// Aliases
	aliases := make([]string, 0, len(g.Alias))
	for _, a := range g.Alias {
		if a.Name != "" {
			aliases = append(aliases, a.Name)
		}
	}
	doc.Aliases = aliases

	// Tags
	tagNames := make([]string, 0, len(g.Tag))
	tagIDs := make([]int, 0, len(g.Tag))
	for _, rel := range g.Tag {
		if rel.Tag != nil {
			tagNames = append(tagNames, rel.Tag.Name)
			tagIDs = append(tagIDs, rel.Tag.ID)
		}
	}
	doc.TagNames = tagNames
	doc.TagIDs = tagIDs

	// Officials
	officialNames := make([]string, 0, len(g.Official))
	officialIDs := make([]int, 0, len(g.Official))
	for _, rel := range g.Official {
		if rel.Official != nil {
			officialNames = append(officialNames, rel.Official.Name)
			if rel.Official.Original != "" && rel.Official.Original != rel.Official.Name {
				officialNames = append(officialNames, rel.Official.Original)
			}
			officialIDs = append(officialIDs, rel.Official.ID)
		}
	}
	doc.OfficialNames = officialNames
	doc.OfficialIDs = officialIDs

	// Engines
	engineNames := make([]string, 0, len(g.Engine))
	engineIDs := make([]int, 0, len(g.Engine))
	for _, rel := range g.Engine {
		if rel.Engine != nil {
			engineNames = append(engineNames, rel.Engine.Name)
			engineIDs = append(engineIDs, rel.Engine.ID)
		}
	}
	doc.EngineNames = engineNames
	doc.EngineIDs = engineIDs

	// Derive effective_banner_hash from preloaded Cover (sort_order=0 row).
	// Caller must Preload("Cover") — empty when no pinned cover exists.
	for i := range g.Cover {
		if g.Cover[i].SortOrder == 0 {
			doc.EffectiveBannerHash = g.Cover[i].ImageHash
			break
		}
	}

	// Derive year + timestamp from the typed date column. Cheap filter/sort
	// fields stay nil when ReleaseDate is unknown.
	if g.ReleaseDate != nil {
		t := g.ReleaseDate.Time().UTC()
		y := t.Year()
		doc.ReleasedYear = &y
		m := int(t.Month())
		doc.ReleasedMonth = &m
		ts := t.Unix()
		doc.ReleasedTS = &ts
	}

	return doc
}

// formatGalgameReleaseDate renders the model's date column for indexing:
// nil → "" (empty string, so the doc field is present but blank); valid
// date → "YYYY-MM-DD" UTC string. Mirrors the JSON shape used elsewhere.
func formatGalgameReleaseDate(d *model.Date) string {
	if d == nil {
		return ""
	}
	return d.Time().UTC().Format("2006-01-02")
}

// UpsertGalgame pushes a single galgame doc.
func (i *Indexer) UpsertGalgame(ctx context.Context, g *model.Galgame) error {
	doc := ToGalgameDoc(g)
	_, err := i.client.Index(IndexGalgames).AddDocumentsWithContext(
		ctx,
		[]*GalgameDoc{doc},
		nil,
	)
	return err
}

// UpsertGalgamesBatch pushes many galgame docs in one API call.
func (i *Indexer) UpsertGalgamesBatch(ctx context.Context, gs []*model.Galgame) error {
	if len(gs) == 0 {
		return nil
	}
	docs := make([]*GalgameDoc, len(gs))
	for idx, g := range gs {
		docs[idx] = ToGalgameDoc(g)
	}
	_, err := i.client.Index(IndexGalgames).AddDocumentsWithContext(ctx, docs, nil)
	return err
}

// DeleteGalgame removes a galgame doc by id.
func (i *Indexer) DeleteGalgame(ctx context.Context, id int) error {
	_, err := i.client.Index(IndexGalgames).DeleteDocumentWithContext(
		ctx,
		strconv.Itoa(id),
		nil,
	)
	return err
}

// ─────────────────────────── Tag ───────────────────────────

// ToTagDoc converts a tag model (with aliases preloaded) into a doc.
func ToTagDoc(t *model.GalgameTag) *TagDoc {
	aliases := make([]string, 0, len(t.Alias))
	for _, a := range t.Alias {
		if a.Name != "" {
			aliases = append(aliases, a.Name)
		}
	}
	return &TagDoc{
		ID:           t.ID,
		Name:         t.Name,
		Aliases:      aliases,
		Category:     t.Category,
		GalgameCount: t.GalgameCount,
	}
}

func (i *Indexer) UpsertTag(ctx context.Context, t *model.GalgameTag) error {
	_, err := i.client.Index(IndexTags).AddDocumentsWithContext(
		ctx,
		[]*TagDoc{ToTagDoc(t)},
		nil,
	)
	return err
}

func (i *Indexer) UpsertTagsBatch(ctx context.Context, ts []*model.GalgameTag) error {
	if len(ts) == 0 {
		return nil
	}
	docs := make([]*TagDoc, len(ts))
	for idx, t := range ts {
		docs[idx] = ToTagDoc(t)
	}
	_, err := i.client.Index(IndexTags).AddDocumentsWithContext(ctx, docs, nil)
	return err
}

func (i *Indexer) DeleteTag(ctx context.Context, id int) error {
	_, err := i.client.Index(IndexTags).DeleteDocumentWithContext(
		ctx,
		strconv.Itoa(id),
		nil,
	)
	return err
}

// ─────────────────────────── Official ───────────────────────────

// ToOfficialDoc converts an official model (with aliases preloaded) into a doc.
func ToOfficialDoc(o *model.GalgameOfficial) *OfficialDoc {
	aliases := make([]string, 0, len(o.Alias))
	for _, a := range o.Alias {
		if a.Name != "" {
			aliases = append(aliases, a.Name)
		}
	}
	return &OfficialDoc{
		ID:           o.ID,
		Name:         o.Name,
		Original:     o.Original,
		Aliases:      aliases,
		Category:     o.Category,
		Lang:         o.Lang,
		GalgameCount: o.GalgameCount,
	}
}

func (i *Indexer) UpsertOfficial(ctx context.Context, o *model.GalgameOfficial) error {
	_, err := i.client.Index(IndexOfficials).AddDocumentsWithContext(
		ctx,
		[]*OfficialDoc{ToOfficialDoc(o)},
		nil,
	)
	return err
}

func (i *Indexer) UpsertOfficialsBatch(ctx context.Context, os []*model.GalgameOfficial) error {
	if len(os) == 0 {
		return nil
	}
	docs := make([]*OfficialDoc, len(os))
	for idx, o := range os {
		docs[idx] = ToOfficialDoc(o)
	}
	_, err := i.client.Index(IndexOfficials).AddDocumentsWithContext(ctx, docs, nil)
	return err
}

func (i *Indexer) DeleteOfficial(ctx context.Context, id int) error {
	_, err := i.client.Index(IndexOfficials).DeleteDocumentWithContext(
		ctx,
		strconv.Itoa(id),
		nil,
	)
	return err
}

// ─────────────────────────── helpers ───────────────────────────

// parseReleased turns Galgame.Released into (year, unixTS, ok).
// Accepted formats: YYYY, YYYY-MM, YYYY-MM-DD. Other inputs → ok=false.
func parseReleased(s string) (year int, ts int64, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "unknown" || s == "tba" {
		return 0, 0, false
	}
	parts := strings.SplitN(s, "-", 3)
	if len(parts) == 0 {
		return 0, 0, false
	}
	y, err := strconv.Atoi(parts[0])
	if err != nil || y < 1900 || y > 2100 {
		return 0, 0, false
	}

	m, d := 1, 1
	if len(parts) >= 2 {
		if v, err := strconv.Atoi(parts[1]); err == nil && v >= 1 && v <= 12 {
			m = v
		}
	}
	if len(parts) >= 3 {
		if v, err := strconv.Atoi(parts[2]); err == nil && v >= 1 && v <= 31 {
			d = v
		}
	}

	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC).Unix()
	return y, t, true
}
