// Package vndbsync is the VNDB → galgame wiki sync. Moved verbatim from
// cmd/sync-vndb (algorithm/SQL/batching unchanged); main() became Run()
// returning a summary/error instead of os.Exit, plus a ctx-cancel check
// at batch boundaries. Single source of truth: both the in-process
// scheduler (internal/jobs) and cmd/sync-vndb call Run.
package vndbsync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"api/internal/infrastructure/database"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/vndbresolve"
	"api/pkg/config"

	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const (
	vndbAPI      = "https://api.vndb.org/kana"
	batchSize    = 100
	requestDelay = 2 * time.Second
)

// VNDB fields to request
const vndbFields = "id, title, titles.lang, titles.title, titles.official, " +
	"aliases, olang, released, description, " +
	"image.url, image.sexual, " +
	"tags.id, tags.name, tags.category, tags.rating, tags.spoiler, tags.lie, " +
	"developers.id, developers.name, developers.original, developers.type, developers.lang, " +
	"devstatus"

// ---------------------------------------------------------------------------
// VNDB API types
// ---------------------------------------------------------------------------

type vndbRequest struct {
	Filters any    `json:"filters,omitempty"`
	Fields  string `json:"fields"`
	Sort    string `json:"sort"`
	Reverse bool   `json:"reverse,omitempty"`
	Results int    `json:"results"`
}

type vndbResponse struct {
	Results []vndbVN `json:"results"`
	More    bool     `json:"more"`
}

type vndbVN struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Titles      []vndbTitle     `json:"titles"`
	Aliases     []string        `json:"aliases"`
	Olang       string          `json:"olang"`
	Released    *string         `json:"released"`
	Description *string         `json:"description"`
	Image       *vndbImage      `json:"image"`
	Tags        []vndbTag       `json:"tags"`
	Developers  []vndbDeveloper `json:"developers"`
	Devstatus   int             `json:"devstatus"`
}

type vndbTitle struct {
	Lang     string `json:"lang"`
	Title    string `json:"title"`
	Official bool   `json:"official"`
}

type vndbImage struct {
	URL    string  `json:"url"`
	Sexual float64 `json:"sexual"`
}

type vndbTag struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Category string  `json:"category"`
	Rating   float64 `json:"rating"`
	Spoiler  int     `json:"spoiler"`
	Lie      bool    `json:"lie"`
}

type vndbDeveloper struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Original string `json:"original"`
	Type     string `json:"type"` // co, in, ng
	Lang     string `json:"lang"`
}

// ---------------------------------------------------------------------------
// VNDB API client
// ---------------------------------------------------------------------------

type vndbClient struct {
	http        *http.Client
	lastRequest time.Time
}

func newVNDBClient() *vndbClient {
	return &vndbClient{
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *vndbClient) fetchMaxVNID() (int, error) {
	since := time.Since(c.lastRequest)
	if since < requestDelay {
		time.Sleep(requestDelay - since)
	}

	req := vndbRequest{
		Fields:  "id",
		Sort:    "id",
		Reverse: true,
		Results: 1,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return 0, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", vndbAPI+"/vn", bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	c.lastRequest = time.Now()
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return 0, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("VNDB API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result vndbResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return 0, fmt.Errorf("unmarshal response: %w", err)
	}
	if len(result.Results) == 0 {
		return 0, fmt.Errorf("no results")
	}

	id := result.Results[0].ID
	if !strings.HasPrefix(id, "v") {
		return 0, fmt.Errorf("unexpected ID format: %s", id)
	}
	n, err := strconv.Atoi(id[1:])
	if err != nil {
		return 0, fmt.Errorf("parse ID %s: %w", id, err)
	}
	return n, nil
}

func (c *vndbClient) fetchVNs(afterID string) (*vndbResponse, error) {
	// Rate limiting
	since := time.Since(c.lastRequest)
	if since < requestDelay {
		time.Sleep(requestDelay - since)
	}

	var filter any
	if afterID != "" {
		filter = []any{"id", ">", afterID}
	}
	req := vndbRequest{
		Filters: filter,
		Fields:  vndbFields,
		Sort:    "id",
		Results: batchSize,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", vndbAPI+"/vn", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	c.lastRequest = time.Now()
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == 429 {
		// Rate limited — wait and retry
		slog.Warn("rate limited, waiting 60s")
		time.Sleep(60 * time.Second)
		return c.fetchVNs(afterID)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("VNDB API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result vndbResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &result, nil
}

// ---------------------------------------------------------------------------
// Syncer
// ---------------------------------------------------------------------------

type syncer struct {
	db       *gorm.DB
	client   *vndbClient
	resolver *vndbresolve.Resolver // shared VNDB tag/official resolution

	existingVNDBIDs map[string]bool

	stats struct {
		created   int
		skipped   int
		cancelled int
	}
}

func newSyncer(db *gorm.DB, resolver *vndbresolve.Resolver) *syncer {
	return &syncer{
		db:              db,
		client:          newVNDBClient(),
		resolver:        resolver,
		existingVNDBIDs: make(map[string]bool),
	}
}

func (s *syncer) loadExistingData() error {
	// Load existing vndb_ids
	var vndbIDs []string
	if err := s.db.Model(&model.Galgame{}).
		Where("vndb_id IS NOT NULL AND vndb_id != ''").
		Pluck("vndb_id", &vndbIDs).Error; err != nil {
		return fmt.Errorf("load vndb_ids: %w", err)
	}
	for _, id := range vndbIDs {
		s.existingVNDBIDs[id] = true
	}
	slog.Info("loaded existing data", "vndb_ids", len(s.existingVNDBIDs))

	// Existing tags/officials caches live in the shared resolver.
	if err := s.resolver.LoadCaches(s.db); err != nil {
		return fmt.Errorf("load tag/official caches: %w", err)
	}

	return nil
}

func (s *syncer) getStartID(full bool) (string, error) {
	if full {
		// Empty string = no id filter on first request; VNDB defaults to sort by id asc.
		// Subsequent batches use id > lastID for pagination.
		return "", nil
	}

	// Query VNDB for its actual max VN ID — the DB may contain bogus vndb_id values
	// (e.g. from legacy migrations where users entered arbitrary strings), and VNDB
	// returns 400 "Invalid value" when filtering with an id beyond its valid range.
	vndbMax, err := s.client.fetchMaxVNID()
	if err != nil {
		return "", fmt.Errorf("fetch VNDB max id: %w", err)
	}
	slog.Info("VNDB max VN id", "id", fmt.Sprintf("v%d", vndbMax))

	// Resume from the high-water mark of SYNC-CREATED drafts (status=2),
	// capped at VNDB's real max so we skip garbage ids. We deliberately do
	// NOT take the max over all rows: a manually-created *published* entry
	// (status=0) with a VNDB id ahead of the sync would otherwise jump the
	// cursor past it and permanently skip the gap below — exactly what
	// stranded v65707..v66154 when a manual v66155 was created. Claimed
	// drafts (status 2→0) drop out of this max, so the scan may revisit a few
	// already-present ids on resume; that's harmless — processBatch dedups
	// them via the existing-id set.
	var maxID int
	if err := s.db.Raw(`
		SELECT COALESCE(MAX(CAST(SUBSTRING(vndb_id FROM 2) AS INTEGER)), 0)
		FROM galgame
		WHERE vndb_id ~ '^v[0-9]+$'
		  AND status = ?
		  AND CAST(SUBSTRING(vndb_id FROM 2) AS INTEGER) <= ?
	`, model.GalgameStatusVNDBDraft, vndbMax).Scan(&maxID).Error; err != nil {
		// Don't silently restart the whole scan from v0 on a DB error.
		return "", fmt.Errorf("max local vndb_id: %w", err)
	}

	startID := fmt.Sprintf("v%d", maxID)
	slog.Info("incremental mode", "starting_after", startID)
	return startID, nil
}

func (s *syncer) run(ctx context.Context, full bool) error {
	if err := s.loadExistingData(); err != nil {
		return err
	}

	afterID, err := s.getStartID(full)
	if err != nil {
		return err
	}
	batch := 0

	for {
		// Honor cancellation at batch boundaries (graceful shutdown /
		// run timeout). VNDB calls already self-limit; this bounds the
		// stop latency to one batch.
		if err := ctx.Err(); err != nil {
			return err
		}

		batch++
		resp, err := s.client.fetchVNs(afterID)
		if err != nil {
			return fmt.Errorf("batch %d: %w", batch, err)
		}

		if len(resp.Results) == 0 {
			break
		}

		newCount, skipCount, cancelCount := s.processBatch(resp.Results)
		lastID := resp.Results[len(resp.Results)-1].ID

		fmt.Printf("Batch %d: fetched %d VNs (after %s → %s), %d new, %d existing, %d cancelled\n",
			batch, len(resp.Results), afterID, lastID, newCount, skipCount, cancelCount)

		afterID = lastID

		if !resp.More {
			break
		}
	}

	fmt.Printf("\nSync complete:\n")
	fmt.Printf("  Created:       %d galgames\n", s.stats.created)
	fmt.Printf("  Skipped:       %d (already exist)\n", s.stats.skipped)
	fmt.Printf("  Cancelled:     %d (devstatus=2)\n", s.stats.cancelled)
	fmt.Printf("  New tags:      %d\n", s.resolver.NewTags())
	fmt.Printf("  New officials: %d\n", s.resolver.NewOfficials())

	return nil
}

func (s *syncer) processBatch(vns []vndbVN) (newCount, skipCount, cancelCount int) {
	for i := range vns {
		vn := &vns[i]

		if s.existingVNDBIDs[vn.ID] {
			skipCount++
			s.stats.skipped++
			continue
		}

		if vn.Devstatus == 2 { // cancelled
			cancelCount++
			s.stats.cancelled++
			continue
		}

		if err := s.insertVN(vn); err != nil {
			slog.Error("failed to insert VN", "vndb_id", vn.ID, "error", err)
			continue
		}

		s.existingVNDBIDs[vn.ID] = true
		newCount++
		s.stats.created++
	}
	return
}

func (s *syncer) insertVN(vn *vndbVN) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		galgame := s.buildGalgame(vn)
		if err := tx.Create(galgame).Error; err != nil {
			return fmt.Errorf("create galgame: %w", err)
		}

		// Aliases
		for _, alias := range vn.Aliases {
			alias = strings.TrimSpace(alias)
			if alias == "" {
				continue
			}
			if err := tx.Create(&model.GalgameAlias{GalgameID: galgame.ID, Name: alias}).Error; err != nil {
				return fmt.Errorf("create alias: %w", err)
			}
		}

		// Tag relations — marked source="vndb" so the enrichment recognizes them
		// once the draft is claimed (status 2→0).
		for _, tag := range vn.Tags {
			rt := vndbresolve.Tag{ID: tag.ID, Name: tag.Name, Category: tag.Category, Rating: tag.Rating, Spoiler: tag.Spoiler, Lie: tag.Lie}
			if vndbresolve.TagSkipped(rt) {
				continue
			}
			tagID, err := s.resolver.ResolveTag(tx, rt)
			if err != nil {
				return fmt.Errorf("resolve tag %q: %w", rt.Name, err)
			}
			if err := tx.Exec(
				"INSERT INTO galgame_tag_relation (galgame_id, tag_id, spoiler_level, source, created, updated) VALUES (?, ?, ?, 'vndb', NOW(), NOW()) ON CONFLICT DO NOTHING",
				galgame.ID, tagID, tag.Spoiler,
			).Error; err != nil {
				return fmt.Errorf("insert tag relation: %w", err)
			}
		}

		// Developer/Official relations — source="vndb".
		for _, dev := range vn.Developers {
			officialID, err := s.resolver.ResolveOfficial(tx, vndbresolve.Developer{ID: dev.ID, Name: dev.Name, Original: dev.Original, Type: dev.Type, Lang: dev.Lang})
			if err != nil {
				return fmt.Errorf("resolve official %q: %w", dev.Name, err)
			}
			if err := tx.Exec(
				"INSERT INTO galgame_official_relation (galgame_id, official_id, source, created, updated) VALUES (?, ?, 'vndb', NOW(), NOW()) ON CONFLICT DO NOTHING",
				galgame.ID, officialID,
			).Error; err != nil {
				return fmt.Errorf("insert official relation: %w", err)
			}
		}

		return nil
	})
}

func (s *syncer) buildGalgame(vn *vndbVN) *model.Galgame {
	g := &model.Galgame{
		VNDBID:           vn.ID,
		Status:           2, // draft
		UserID:           1, // system user
		OriginalLanguage: mapLang(vn.Olang),
		ContentLimit:     "sfw",
		AgeLimit:         "r18",
	}

	// Titles — VNDB guarantees titles[lang=olang] exists (main title in original script)
	for _, t := range vn.Titles {
		switch t.Lang {
		case "en":
			g.NameEnUS = t.Title
		case "ja":
			g.NameJaJP = t.Title
		case "zh-Hans":
			g.NameZhCN = t.Title
		case "zh-Hant":
			g.NameZhTW = t.Title
		}
	}
	// Fallback: a VN whose olang isn't en/ja/zh (e.g. ko/ru) would otherwise get
	// no name at all. Put the always-present main title on the original-language
	// slot so we never insert a nameless draft.
	if g.NameEnUS == "" && g.NameJaJP == "" && g.NameZhCN == "" && g.NameZhTW == "" && vn.Title != "" {
		switch g.OriginalLanguage {
		case "ja-jp":
			g.NameJaJP = vn.Title
		case "zh-cn":
			g.NameZhCN = vn.Title
		case "zh-tw":
			g.NameZhTW = vn.Title
		default:
			g.NameEnUS = vn.Title
		}
	}

	// Released → typed date + TBA flag, via the shared parser (model.ParseLegacyReleased).
	// VNDB conventions: nil/""=unknown, "tba"=date pending, "YYYY[-MM[-DD]]"=parsed.
	// ParseLegacyReleased returns *time.Time (also used by snapshot builders);
	// wrap into the model's Date type for the typed column.
	if vn.Released != nil {
		t, tba := model.ParseLegacyReleased(*vn.Released)
		g.ReleaseDate = model.NewDate(t)
		g.ReleaseDateTBA = tba
	}

	// Description → intro_en_us
	if vn.Description != nil {
		g.IntroEnUS = *vn.Description
	}

	// Image
	if vn.Image != nil {
		g.Banner = vn.Image.URL
		if vn.Image.Sexual >= 1 {
			g.ContentLimit = "nsfw"
		}
	}

	return g
}

// ---------------------------------------------------------------------------
// Mappings
// ---------------------------------------------------------------------------

func mapLang(olang string) string {
	switch olang {
	case "ja":
		return "ja-jp"
	case "en":
		return "en-us"
	case "zh-Hans":
		return "zh-cn"
	case "zh-Hant":
		return "zh-tw"
	case "ko":
		return "ko-kr"
	default:
		return olang
	}
}

// ---------------------------------------------------------------------------
// Entrypoint
// ---------------------------------------------------------------------------

// Opts mirrors the original cmd/sync-vndb flags.
type Opts struct {
	Full       bool   // full sync from v0 (default: incremental)
	TagMapPath string // path to tagMap.ts
}

// DefaultTagMapPath is re-exported from vndbresolve so cmd/sync-vndb and the
// jobs adapter don't import the resolver subpackage directly.
func DefaultTagMapPath() string { return vndbresolve.DefaultTagMapPath() }

// Run executes the VNDB sync. Returns a summary map (for job_run) and an
// error. No os.Exit — caller decides exit/recording.
func Run(ctx context.Context, cfg *config.Config, opts Opts) (map[string]any, error) {
	if opts.TagMapPath == "" {
		opts.TagMapPath = DefaultTagMapPath()
	}

	wikiDB, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		return nil, fmt.Errorf("connect galgame wiki db: %w", err)
	}
	defer wikiDB.Close()
	db := wikiDB.DB()

	tagMap, err := vndbresolve.ParseTagMap(opts.TagMapPath)
	if err != nil {
		return nil, fmt.Errorf("parse tagMap %q: %w", opts.TagMapPath, err)
	}
	slog.Info("parsed tagMap", "entries", len(tagMap))
	if len(tagMap) < 100 {
		return nil, fmt.Errorf("tagMap too small (%d entries), check path %q", len(tagMap), opts.TagMapPath)
	}

	mode := "incremental"
	if opts.Full {
		mode = "full"
	}
	slog.Info("starting VNDB sync", "mode", mode)

	resolver := vndbresolve.New(tagMap)
	s := newSyncer(db, resolver)
	if err := s.run(ctx, opts.Full); err != nil {
		return nil, fmt.Errorf("sync: %w", err)
	}

	// Reset sequences (unchanged from original)
	for _, table := range []string{"galgame", "galgame_alias", "galgame_tag", "galgame_official"} {
		db.Exec(fmt.Sprintf("SELECT setval(pg_get_serial_sequence('%s', 'id'), COALESCE((SELECT MAX(id) FROM %s), 1))", table, table))
	}

	return map[string]any{
		"mode":          mode,
		"created":       s.stats.created,
		"skipped":       s.stats.skipped,
		"cancelled":     s.stats.cancelled,
		"new_tags":      resolver.NewTags(),
		"new_officials": resolver.NewOfficials(),
	}, nil
}
