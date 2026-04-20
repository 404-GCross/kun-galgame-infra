package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"api/internal/infrastructure/database"
	"api/internal/platform/galgame/model"
	"api/pkg/config"
	"api/pkg/logger"

	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const (
	vndbAPI      = "https://api.vndb.org/kana"
	batchSize    = 100
	requestDelay = 2 * time.Second
	tagRatingMin = 1.0 // minimum tag rating to include
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
// tagMap parser
// ---------------------------------------------------------------------------

var tagMapLineRegex = regexp.MustCompile(`^\s*(?:'([^']+)'|(\w[\w\s./()\-]*\w|\w+))\s*:\s*'([^']+)'`)

func parseTagMap(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	result := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		m := tagMapLineRegex.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key := m[1]
		if key == "" {
			key = m[2]
		}
		value := m[3]
		if key != "" && value != "" {
			result[key] = value
		}
	}

	return result, nil
}

// ---------------------------------------------------------------------------
// Syncer
// ---------------------------------------------------------------------------

type syncer struct {
	db     *gorm.DB
	client *vndbClient
	tagMap map[string]string // english → chinese

	existingVNDBIDs map[string]bool
	tagCache        map[string]int // tag name (zh or en) → id
	officialCache   map[string]int // official name → id

	stats struct {
		created      int
		skipped      int
		cancelled    int
		newTags      int
		newOfficials int
	}
}

func newSyncer(db *gorm.DB, tagMap map[string]string) *syncer {
	return &syncer{
		db:              db,
		client:          newVNDBClient(),
		tagMap:          tagMap,
		existingVNDBIDs: make(map[string]bool),
		tagCache:        make(map[string]int),
		officialCache:   make(map[string]int),
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

	// Load existing tags
	var tags []model.GalgameTag
	if err := s.db.Find(&tags).Error; err != nil {
		return fmt.Errorf("load tags: %w", err)
	}
	for _, t := range tags {
		s.tagCache[t.Name] = t.ID
	}
	slog.Info("loaded tags", "count", len(s.tagCache))

	// Load existing officials
	var officials []model.GalgameOfficial
	if err := s.db.Find(&officials).Error; err != nil {
		return fmt.Errorf("load officials: %w", err)
	}
	for _, o := range officials {
		s.officialCache[o.Name] = o.ID
	}
	slog.Info("loaded officials", "count", len(s.officialCache))

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

	// Max vndb_id in DB, capped at VNDB's real max so we skip garbage.
	var maxID int
	s.db.Raw(`
		SELECT COALESCE(MAX(CAST(SUBSTRING(vndb_id FROM 2) AS INTEGER)), 0)
		FROM galgame
		WHERE vndb_id ~ '^v[0-9]+$'
		  AND CAST(SUBSTRING(vndb_id FROM 2) AS INTEGER) <= ?
	`, vndbMax).Scan(&maxID)

	startID := fmt.Sprintf("v%d", maxID)
	slog.Info("incremental mode", "starting_after", startID)
	return startID, nil
}

func (s *syncer) run(full bool) error {
	if err := s.loadExistingData(); err != nil {
		return err
	}

	afterID, err := s.getStartID(full)
	if err != nil {
		return err
	}
	batch := 0

	for {
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
	fmt.Printf("  New tags:      %d\n", s.stats.newTags)
	fmt.Printf("  New officials: %d\n", s.stats.newOfficials)

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
			tx.Create(&model.GalgameAlias{GalgameID: galgame.ID, Name: alias})
		}

		// Tag relations
		for _, tag := range vn.Tags {
			if tag.Lie || tag.Rating < tagRatingMin {
				continue
			}
			tagID := s.resolveTag(tx, &tag)
			if tagID == 0 {
				continue
			}
			tx.Exec(
				"INSERT INTO galgame_tag_relation (galgame_id, tag_id, spoiler_level, created, updated) VALUES (?, ?, ?, NOW(), NOW()) ON CONFLICT DO NOTHING",
				galgame.ID, tagID, tag.Spoiler,
			)
		}

		// Developer/Official relations
		for _, dev := range vn.Developers {
			officialID := s.resolveOfficial(tx, &dev)
			if officialID == 0 {
				continue
			}
			tx.Exec(
				"INSERT INTO galgame_official_relation (galgame_id, official_id, created, updated) VALUES (?, ?, NOW(), NOW()) ON CONFLICT DO NOTHING",
				galgame.ID, officialID,
			)
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

	// Released
	if vn.Released != nil && *vn.Released != "" && *vn.Released != "tba" {
		g.Released = *vn.Released
	} else {
		g.Released = "unknown"
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

func (s *syncer) resolveTag(tx *gorm.DB, tag *vndbTag) int {
	// 1. Check tagMap for Chinese name
	if zhName, ok := s.tagMap[tag.Name]; ok {
		if id, ok := s.tagCache[zhName]; ok {
			return id
		}
	}

	// 2. Check if tag already exists by English name
	if id, ok := s.tagCache[tag.Name]; ok {
		return id
	}

	// 3. Create new tag with English name
	category := mapTagCategory(tag.Category)
	newTag := model.GalgameTag{
		Name:     tag.Name,
		Category: category,
	}
	if err := tx.Create(&newTag).Error; err != nil {
		// Might be unique constraint violation (concurrent insert)
		var existing model.GalgameTag
		if tx.Where("name = ?", tag.Name).First(&existing).Error == nil {
			s.tagCache[tag.Name] = existing.ID
			return existing.ID
		}
		return 0
	}

	s.tagCache[tag.Name] = newTag.ID
	s.stats.newTags++
	return newTag.ID
}

func (s *syncer) resolveOfficial(tx *gorm.DB, dev *vndbDeveloper) int {
	// Check by name
	if id, ok := s.officialCache[dev.Name]; ok {
		return id
	}

	// Check by original name (Japanese etc.)
	if dev.Original != "" {
		if id, ok := s.officialCache[dev.Original]; ok {
			return id
		}
	}

	// Create new official
	official := model.GalgameOfficial{
		Name:     dev.Name,
		Category: mapDevType(dev.Type),
		Lang:     dev.Lang,
	}
	if err := tx.Create(&official).Error; err != nil {
		var existing model.GalgameOfficial
		if tx.Where("name = ?", dev.Name).First(&existing).Error == nil {
			s.officialCache[dev.Name] = existing.ID
			return existing.ID
		}
		return 0
	}

	s.officialCache[dev.Name] = official.ID
	s.stats.newOfficials++
	return official.ID
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

func mapTagCategory(cat string) string {
	switch cat {
	case "cont":
		return "content"
	case "ero":
		return "sexual"
	case "tech":
		return "technical"
	default:
		return "content"
	}
}

func mapDevType(t string) string {
	switch t {
	case "co":
		return "company"
	case "in":
		return "individual"
	case "ng":
		return "amateur"
	default:
		return "company"
	}
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

func main() {
	full := flag.Bool("full", false, "Full sync from v0 (default: incremental)")
	tagMapPath := flag.String("tagmap", "../../docs/tagMap.ts", "Path to tagMap.ts")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	wikiDB, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		slog.Error("failed to connect to galgame wiki database", "error", err)
		os.Exit(1)
	}
	db := wikiDB.DB()

	// Parse tagMap
	tagMap, err := parseTagMap(*tagMapPath)
	if err != nil {
		slog.Error("failed to parse tagMap", "error", err)
		os.Exit(1)
	}
	slog.Info("parsed tagMap", "entries", len(tagMap))

	// Verify tagMap sanity
	if len(tagMap) < 100 {
		slog.Error("tagMap seems too small, check path", "entries", len(tagMap))
		os.Exit(1)
	}

	mode := "incremental"
	if *full {
		mode = "full"
	}
	fmt.Printf("Starting VNDB sync (mode=%s)\n\n", mode)

	s := newSyncer(db, tagMap)
	if err := s.run(*full); err != nil {
		slog.Error("sync failed", "error", err)
		os.Exit(1)
	}

	// Reset sequences
	fmt.Println("\nResetting sequences...")
	for _, table := range []string{"galgame", "galgame_alias", "galgame_tag", "galgame_official"} {
		db.Exec(fmt.Sprintf("SELECT setval(pg_get_serial_sequence('%s', 'id'), COALESCE((SELECT MAX(id) FROM %s), 1))", table, table))
	}
	fmt.Println("Done.")
}
