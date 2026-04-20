// sync-vndb-relations backfills galgame_tag_relation / galgame_official_relation
// for existing galgames whose relations are missing.
//
// Design doc: docs/galgame_wiki/04-vndb-relations-sync-design.md
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
	"strings"
	"time"

	"api/internal/infrastructure/database"
	"api/internal/platform/galgame/model"
	"api/pkg/config"
	"api/pkg/logger"

	"gorm.io/gorm"
)

const (
	vndbAPI      = "https://api.vndb.org/kana"
	batchSize    = 100
	requestDelay = 2 * time.Second
)

const vndbFields = "id, " +
	"tags.id, tags.name, tags.category, tags.rating, tags.spoiler, tags.lie, " +
	"developers.id, developers.name, developers.original, developers.type, developers.lang"

// ---------------------------------------------------------------------------
// VNDB API types
// ---------------------------------------------------------------------------

type vndbRequest struct {
	Filters any    `json:"filters,omitempty"`
	Fields  string `json:"fields"`
	Results int    `json:"results"`
}

type vndbResponse struct {
	Results []vndbVN `json:"results"`
}

type vndbVN struct {
	ID         string          `json:"id"`
	Tags       []vndbTag       `json:"tags"`
	Developers []vndbDeveloper `json:"developers"`
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
	Type     string `json:"type"`
	Lang     string `json:"lang"`
}

// ---------------------------------------------------------------------------
// VNDB client
// ---------------------------------------------------------------------------

type vndbClient struct {
	http        *http.Client
	lastRequest time.Time
}

func newVNDBClient() *vndbClient {
	return &vndbClient{http: &http.Client{Timeout: 30 * time.Second}}
}

// fetchVNsByIDs batches up to 100 vndb_ids per request via the `or` filter.
func (c *vndbClient) fetchVNsByIDs(ids []string) (*vndbResponse, error) {
	if len(ids) == 0 {
		return &vndbResponse{}, nil
	}

	since := time.Since(c.lastRequest)
	if since < requestDelay {
		time.Sleep(requestDelay - since)
	}

	orFilter := []any{"or"}
	for _, id := range ids {
		orFilter = append(orFilter, []any{"id", "=", id})
	}

	req := vndbRequest{
		Filters: orFilter,
		Fields:  vndbFields,
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
		slog.Warn("rate limited, waiting 60s")
		time.Sleep(60 * time.Second)
		return c.fetchVNsByIDs(ids)
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
// tagMap parser (duplicated from sync-vndb — same logic, no shared pkg yet)
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
	tagMap map[string]string

	// per-galgame flags
	vndbToGalgameID map[string]int
	needTag         map[string]bool // vndb_id → missing tag relations
	needOff         map[string]bool // vndb_id → missing official relations

	// caches
	tagCache      map[string]int // tag name (zh or en) → id
	officialCache map[string]int // official name → id

	stats struct {
		tagRelations     int
		officialRelations int
		newTags          int
		newOfficials     int
		backfilledOriginal int
		vnNotFound       int
	}
}

func newSyncer(db *gorm.DB, tagMap map[string]string) *syncer {
	return &syncer{
		db:              db,
		client:          newVNDBClient(),
		tagMap:          tagMap,
		vndbToGalgameID: make(map[string]int),
		needTag:         make(map[string]bool),
		needOff:         make(map[string]bool),
		tagCache:        make(map[string]int),
		officialCache:   make(map[string]int),
	}
}

// loadCoverage computes per-galgame flags: has_tag_rel / has_off_rel
// via two aggregate queries; then populates needTag / needOff.
func (s *syncer) loadCoverage() error {
	type row struct {
		ID      int
		VNDBID  string
		HasTag  bool
		HasOff  bool
	}
	var rows []row

	// Single query: LEFT JOIN + EXISTS is cheaper than per-galgame checks
	if err := s.db.Raw(`
		SELECT
			g.id AS id,
			g.vndb_id AS vndb_id,
			EXISTS(SELECT 1 FROM galgame_tag_relation WHERE galgame_id = g.id) AS has_tag,
			EXISTS(SELECT 1 FROM galgame_official_relation WHERE galgame_id = g.id) AS has_off
		FROM galgame g
		WHERE g.vndb_id ~ '^v[0-9]+$'
	`).Scan(&rows).Error; err != nil {
		return fmt.Errorf("load coverage: %w", err)
	}

	missTag, missOff := 0, 0
	for _, r := range rows {
		s.vndbToGalgameID[r.VNDBID] = r.ID
		if !r.HasTag {
			s.needTag[r.VNDBID] = true
			missTag++
		}
		if !r.HasOff {
			s.needOff[r.VNDBID] = true
			missOff++
		}
	}

	slog.Info("loaded galgame", "records", len(rows))
	slog.Info("relations coverage", "tag_missing", missTag, "off_missing", missOff)
	return nil
}

func (s *syncer) loadCaches() error {
	var tags []model.GalgameTag
	if err := s.db.Find(&tags).Error; err != nil {
		return fmt.Errorf("load tags: %w", err)
	}
	for _, t := range tags {
		s.tagCache[t.Name] = t.ID
	}
	slog.Info("loaded tags", "count", len(s.tagCache))

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

// queuedIDs returns vndb_ids that need either tag or official sync, deduplicated.
func (s *syncer) queuedIDs() []string {
	set := make(map[string]struct{})
	for id := range s.needTag {
		set[id] = struct{}{}
	}
	for id := range s.needOff {
		set[id] = struct{}{}
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	return ids
}

func (s *syncer) run() error {
	if err := s.loadCoverage(); err != nil {
		return err
	}
	if err := s.loadCaches(); err != nil {
		return err
	}

	queue := s.queuedIDs()
	slog.Info("queued", "unique_vns", len(queue))
	if len(queue) == 0 {
		fmt.Println("Nothing to sync.")
		return nil
	}

	totalBatches := (len(queue) + batchSize - 1) / batchSize
	foundSet := make(map[string]struct{})

	for i := 0; i < len(queue); i += batchSize {
		end := min(i+batchSize, len(queue))
		batchIDs := queue[i:end]
		batchNum := i/batchSize + 1

		resp, err := s.client.fetchVNsByIDs(batchIDs)
		if err != nil {
			return fmt.Errorf("batch %d: %w", batchNum, err)
		}

		beforeTag := s.stats.tagRelations
		beforeOff := s.stats.officialRelations
		beforeNewT := s.stats.newTags
		beforeNewO := s.stats.newOfficials

		for i := range resp.Results {
			vn := &resp.Results[i]
			foundSet[vn.ID] = struct{}{}
			s.processVN(vn)
		}

		fmt.Printf("Batch %d/%d fetched=%d tag_rels=+%d off_rels=+%d new_tags=+%d new_officials=+%d\n",
			batchNum, totalBatches, len(resp.Results),
			s.stats.tagRelations-beforeTag,
			s.stats.officialRelations-beforeOff,
			s.stats.newTags-beforeNewT,
			s.stats.newOfficials-beforeNewO,
		)
	}

	// VN IDs queued but not returned (deleted/merged on VNDB)
	for _, id := range queue {
		if _, ok := foundSet[id]; !ok {
			s.stats.vnNotFound++
		}
	}

	fmt.Printf("\nSync complete:\n")
	fmt.Printf("  Tag relations added:       %d\n", s.stats.tagRelations)
	fmt.Printf("  Official relations added:  %d\n", s.stats.officialRelations)
	fmt.Printf("  New tags:                  %d\n", s.stats.newTags)
	fmt.Printf("  New officials:             %d\n", s.stats.newOfficials)
	fmt.Printf("  Original backfilled:       %d\n", s.stats.backfilledOriginal)
	fmt.Printf("  VNDB not found (skipped):  %d\n", s.stats.vnNotFound)
	return nil
}

func (s *syncer) processVN(vn *vndbVN) {
	galgameID, ok := s.vndbToGalgameID[vn.ID]
	if !ok {
		return
	}

	s.db.Transaction(func(tx *gorm.DB) error {
		if s.needTag[vn.ID] {
			for _, t := range vn.Tags {
				if t.Lie {
					continue
				}
				tagID := s.resolveTag(tx, &t)
				if tagID == 0 {
					continue
				}
				res := tx.Exec(
					"INSERT INTO galgame_tag_relation (galgame_id, tag_id, spoiler_level, created, updated) VALUES (?, ?, ?, NOW(), NOW()) ON CONFLICT DO NOTHING",
					galgameID, tagID, t.Spoiler,
				)
				if res.RowsAffected > 0 {
					s.stats.tagRelations++
				}
			}
		}

		if s.needOff[vn.ID] {
			for _, d := range vn.Developers {
				officialID := s.resolveOfficial(tx, &d)
				if officialID == 0 {
					continue
				}
				res := tx.Exec(
					"INSERT INTO galgame_official_relation (galgame_id, official_id, created, updated) VALUES (?, ?, NOW(), NOW()) ON CONFLICT DO NOTHING",
					galgameID, officialID,
				)
				if res.RowsAffected > 0 {
					s.stats.officialRelations++
				}
			}
		}
		return nil
	})
}

// resolveTag returns the galgame_tag.id for a VNDB tag, creating one if needed.
// Naming strategy (做法 γ):
//   - tagMap hit → use Chinese name (create if missing)
//   - tagMap miss → use English original name
func (s *syncer) resolveTag(tx *gorm.DB, tag *vndbTag) int {
	name := tag.Name
	if zh, ok := s.tagMap[tag.Name]; ok {
		name = zh
	}
	if id, ok := s.tagCache[name]; ok {
		return id
	}
	newTag := model.GalgameTag{
		Name:     name,
		Category: mapTagCategory(tag.Category),
	}
	if err := tx.Create(&newTag).Error; err != nil {
		// Concurrent insert race or pre-existing row with different casing — re-read
		var existing model.GalgameTag
		res := tx.Where("name = ?", name).Limit(1).Find(&existing)
		if res.RowsAffected > 0 {
			s.tagCache[name] = existing.ID
			return existing.ID
		}
		slog.Warn("create tag failed", "name", name, "error", err)
		return 0
	}
	s.tagCache[name] = newTag.ID
	s.stats.newTags++
	return newTag.ID
}

// resolveOfficial returns the galgame_official.id for a VNDB developer.
// Dedup strategy (question 1 of round 2):
//   - query by name IN (dev.original, dev.name); either match reuses
//   - if existing has empty original and dev.original is non-empty → backfill
//   - if no match → insert with name = dev.original ?? dev.name
func (s *syncer) resolveOfficial(tx *gorm.DB, d *vndbDeveloper) int {
	primary := d.Original
	if primary == "" {
		primary = d.Name
	}

	// Fast path: in-memory cache hits
	if id, ok := s.officialCache[primary]; ok {
		return id
	}
	if d.Original != "" && d.Name != d.Original {
		if id, ok := s.officialCache[d.Name]; ok {
			// Cache hit on fallback name — remember primary → same id
			s.officialCache[primary] = id
			s.maybeBackfillOriginal(tx, id, d.Original)
			return id
		}
	}

	// DB lookup with both candidates (IN handles the dup case).
	// Using Limit+Find instead of First to avoid GORM's noisy
	// ErrRecordNotFound log on the common "not in DB yet" path.
	candidates := []string{d.Name}
	if d.Original != "" && d.Original != d.Name {
		candidates = append(candidates, d.Original)
	}
	var existing model.GalgameOfficial
	res := tx.Where("name IN ?", candidates).Limit(1).Find(&existing)
	if res.RowsAffected > 0 {
		s.officialCache[existing.Name] = existing.ID
		s.officialCache[primary] = existing.ID
		if d.Original != "" {
			s.maybeBackfillOriginal(tx, existing.ID, d.Original)
		}
		return existing.ID
	}

	// Create new
	off := model.GalgameOfficial{
		Name:     primary,
		Original: d.Original,
		Category: mapDevType(d.Type),
		Lang:     d.Lang,
	}
	if err := tx.Create(&off).Error; err != nil {
		// Race fallback (concurrent insert hit unique index)
		var e model.GalgameOfficial
		r := tx.Where("name = ?", primary).Limit(1).Find(&e)
		if r.RowsAffected > 0 {
			s.officialCache[primary] = e.ID
			return e.ID
		}
		slog.Warn("create official failed", "name", primary, "error", err)
		return 0
	}
	s.officialCache[primary] = off.ID
	s.stats.newOfficials++
	return off.ID
}

func (s *syncer) maybeBackfillOriginal(tx *gorm.DB, id int, original string) {
	if original == "" {
		return
	}
	res := tx.Exec(
		"UPDATE galgame_official SET original = ? WHERE id = ? AND (original IS NULL OR original = '')",
		original, id,
	)
	if res.RowsAffected > 0 {
		s.stats.backfilledOriginal++
	}
}

// ---------------------------------------------------------------------------
// Mappings
// ---------------------------------------------------------------------------

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

	tagMap, err := parseTagMap(*tagMapPath)
	if err != nil {
		slog.Error("failed to parse tagMap", "error", err)
		os.Exit(1)
	}
	slog.Info("parsed tagMap", "entries", len(tagMap))
	if len(tagMap) < 100 {
		slog.Error("tagMap seems too small, check path", "entries", len(tagMap))
		os.Exit(1)
	}

	fmt.Println("Starting VNDB relations backfill")
	fmt.Println()

	s := newSyncer(db, tagMap)
	if err := s.run(); err != nil {
		slog.Error("sync failed", "error", err)
		os.Exit(1)
	}
}
