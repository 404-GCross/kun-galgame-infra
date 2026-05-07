package model

import (
	"encoding/json"
	"sort"
)

// Snapshot represents the complete editable state of a galgame at a point in time
type Snapshot struct {
	VNDBID           string         `json:"vndb_id"`
	BangumiID        *int           `json:"bid,omitempty"`
	Released         string         `json:"released"`
	NameEnUS         string         `json:"name_en_us"`
	NameJaJP         string         `json:"name_ja_jp"`
	NameZhCN         string         `json:"name_zh_cn"`
	NameZhTW         string         `json:"name_zh_tw"`
	Banner           string         `json:"banner"`
	BannerImageHash  string         `json:"banner_image_hash"`
	IntroEnUS        string         `json:"intro_en_us"`
	IntroJaJP        string         `json:"intro_ja_jp"`
	IntroZhCN        string         `json:"intro_zh_cn"`
	IntroZhTW        string         `json:"intro_zh_tw"`
	ContentLimit     string         `json:"content_limit"`
	OriginalLanguage string         `json:"original_language"`
	AgeLimit         string         `json:"age_limit"`
	SeriesID         *int           `json:"series_id"`
	Aliases          []string       `json:"aliases"`
	TagIDs           []int          `json:"tag_ids"`
	OfficialIDs      []int          `json:"official_ids"`
	EngineIDs        []int          `json:"engine_ids"`
	Links            []SnapshotLink `json:"links"`
}

// SnapshotLink represents an external link in a snapshot
type SnapshotLink struct {
	Name string `json:"name"`
	Link string `json:"link"`
}

// ToJSON serializes the snapshot to JSON bytes
func (s *Snapshot) ToJSON() ([]byte, error) {
	return json.Marshal(s)
}

// SnapshotFromJSON deserializes a snapshot from JSON bytes
func SnapshotFromJSON(data []byte) (*Snapshot, error) {
	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// TakeSnapshot builds a snapshot from a galgame and its loaded relations
func TakeSnapshot(g *Galgame) *Snapshot {
	s := &Snapshot{
		VNDBID:           g.VNDBID,
		BangumiID:        g.BangumiID,
		Released:         g.Released,
		NameEnUS:         g.NameEnUS,
		NameJaJP:         g.NameJaJP,
		NameZhCN:         g.NameZhCN,
		NameZhTW:         g.NameZhTW,
		Banner:           g.Banner,
		BannerImageHash:  derefStr(g.BannerImageHash),
		IntroEnUS:        g.IntroEnUS,
		IntroJaJP:        g.IntroJaJP,
		IntroZhCN:        g.IntroZhCN,
		IntroZhTW:        g.IntroZhTW,
		ContentLimit:     g.ContentLimit,
		OriginalLanguage: g.OriginalLanguage,
		AgeLimit:         g.AgeLimit,
		SeriesID:         g.SeriesID,
		Aliases:          make([]string, 0),
		TagIDs:           make([]int, 0),
		OfficialIDs:      make([]int, 0),
		EngineIDs:        make([]int, 0),
		Links:            make([]SnapshotLink, 0),
	}

	for _, a := range g.Alias {
		s.Aliases = append(s.Aliases, a.Name)
	}
	for _, t := range g.Tag {
		s.TagIDs = append(s.TagIDs, t.TagID)
	}
	for _, o := range g.Official {
		s.OfficialIDs = append(s.OfficialIDs, o.OfficialID)
	}
	for _, e := range g.Engine {
		s.EngineIDs = append(s.EngineIDs, e.EngineID)
	}
	for _, l := range g.Link {
		s.Links = append(s.Links, SnapshotLink{Name: l.Name, Link: l.Link})
	}

	// Sort for deterministic comparison
	sort.Strings(s.Aliases)
	sort.Ints(s.TagIDs)
	sort.Ints(s.OfficialIDs)
	sort.Ints(s.EngineIDs)

	return s
}

// ChangedKeys returns the set of top-level keys that differ between two snapshots
func ChangedKeys(old, new *Snapshot) map[string]bool {
	keys := map[string]bool{}

	if old.VNDBID != new.VNDBID {
		keys["vndb_id"] = true
	}
	if !intPtrEqual(old.BangumiID, new.BangumiID) {
		keys["bid"] = true
	}
	if old.Released != new.Released {
		keys["released"] = true
	}
	if old.NameEnUS != new.NameEnUS {
		keys["name_en_us"] = true
	}
	if old.NameJaJP != new.NameJaJP {
		keys["name_ja_jp"] = true
	}
	if old.NameZhCN != new.NameZhCN {
		keys["name_zh_cn"] = true
	}
	if old.NameZhTW != new.NameZhTW {
		keys["name_zh_tw"] = true
	}
	if old.Banner != new.Banner {
		keys["banner"] = true
	}
	if old.BannerImageHash != new.BannerImageHash {
		keys["banner_image_hash"] = true
	}
	if old.IntroEnUS != new.IntroEnUS {
		keys["intro_en_us"] = true
	}
	if old.IntroJaJP != new.IntroJaJP {
		keys["intro_ja_jp"] = true
	}
	if old.IntroZhCN != new.IntroZhCN {
		keys["intro_zh_cn"] = true
	}
	if old.IntroZhTW != new.IntroZhTW {
		keys["intro_zh_tw"] = true
	}
	if old.ContentLimit != new.ContentLimit {
		keys["content_limit"] = true
	}
	if old.OriginalLanguage != new.OriginalLanguage {
		keys["original_language"] = true
	}
	if old.AgeLimit != new.AgeLimit {
		keys["age_limit"] = true
	}
	if !intPtrEqual(old.SeriesID, new.SeriesID) {
		keys["series_id"] = true
	}
	if !stringSliceEqual(old.Aliases, new.Aliases) {
		keys["aliases"] = true
	}
	if !intSliceEqual(old.TagIDs, new.TagIDs) {
		keys["tag_ids"] = true
	}
	if !intSliceEqual(old.OfficialIDs, new.OfficialIDs) {
		keys["official_ids"] = true
	}
	if !intSliceEqual(old.EngineIDs, new.EngineIDs) {
		keys["engine_ids"] = true
	}
	if !linksEqual(old.Links, new.Links) {
		keys["links"] = true
	}

	return keys
}

// ApplyChanges merges changed fields from source into target.
// Only fields present in changedKeys are copied.
func ApplyChanges(target *Snapshot, source *Snapshot, changedKeys map[string]bool) {
	if changedKeys["vndb_id"] {
		target.VNDBID = source.VNDBID
	}
	if changedKeys["bid"] {
		target.BangumiID = source.BangumiID
	}
	if changedKeys["released"] {
		target.Released = source.Released
	}
	if changedKeys["name_en_us"] {
		target.NameEnUS = source.NameEnUS
	}
	if changedKeys["name_ja_jp"] {
		target.NameJaJP = source.NameJaJP
	}
	if changedKeys["name_zh_cn"] {
		target.NameZhCN = source.NameZhCN
	}
	if changedKeys["name_zh_tw"] {
		target.NameZhTW = source.NameZhTW
	}
	if changedKeys["banner"] {
		target.Banner = source.Banner
	}
	if changedKeys["banner_image_hash"] {
		target.BannerImageHash = source.BannerImageHash
	}
	if changedKeys["intro_en_us"] {
		target.IntroEnUS = source.IntroEnUS
	}
	if changedKeys["intro_ja_jp"] {
		target.IntroJaJP = source.IntroJaJP
	}
	if changedKeys["intro_zh_cn"] {
		target.IntroZhCN = source.IntroZhCN
	}
	if changedKeys["intro_zh_tw"] {
		target.IntroZhTW = source.IntroZhTW
	}
	if changedKeys["content_limit"] {
		target.ContentLimit = source.ContentLimit
	}
	if changedKeys["original_language"] {
		target.OriginalLanguage = source.OriginalLanguage
	}
	if changedKeys["age_limit"] {
		target.AgeLimit = source.AgeLimit
	}
	if changedKeys["series_id"] {
		target.SeriesID = source.SeriesID
	}
	if changedKeys["aliases"] {
		target.Aliases = source.Aliases
	}
	if changedKeys["tag_ids"] {
		target.TagIDs = source.TagIDs
	}
	if changedKeys["official_ids"] {
		target.OfficialIDs = source.OfficialIDs
	}
	if changedKeys["engine_ids"] {
		target.EngineIDs = source.EngineIDs
	}
	if changedKeys["links"] {
		target.Links = source.Links
	}
}

// derefStr returns *p if p is non-nil, else "".
func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func intPtrEqual(a, b *int) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func intSliceEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func linksEqual(a, b []SnapshotLink) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
