package model

import (
	"encoding/json"
	"sort"
	"time"
)

// Snapshot represents the complete editable state of a galgame at a point in time
type Snapshot struct {
	VNDBID    string `json:"vndb_id"`
	BangumiID *int   `json:"bid,omitempty"`
	// ReleaseDate is the canonical release date as "YYYY-MM-DD" (string,
	// not *time.Time, to keep JSON output stable across time zones and to
	// produce identical byte-for-byte snapshots for ChangedKeys diffing).
	// nil = unknown. ReleaseDateTBA = "announced but exact date pending".
	// The two are independent — a scheduled game can have (date=2026-03-01,
	// tba=true) meaning "approximate target".
	ReleaseDate      *string              `json:"release_date"`
	ReleaseDateTBA   bool                 `json:"release_date_tba"`
	NameEnUS         string               `json:"name_en_us"`
	NameJaJP         string               `json:"name_ja_jp"`
	NameZhCN         string               `json:"name_zh_cn"`
	NameZhTW         string               `json:"name_zh_tw"`
	Banner           string               `json:"banner"`
	IntroEnUS        string               `json:"intro_en_us"`
	IntroJaJP        string               `json:"intro_ja_jp"`
	IntroZhCN        string               `json:"intro_zh_cn"`
	IntroZhTW        string               `json:"intro_zh_tw"`
	ContentLimit     string               `json:"content_limit"`
	OriginalLanguage string               `json:"original_language"`
	AgeLimit         string               `json:"age_limit"`
	SeriesID         *int                 `json:"series_id"`
	Aliases          []string             `json:"aliases"`
	TagIDs           []int                `json:"tag_ids"`
	OfficialIDs      []int                `json:"official_ids"`
	EngineIDs        []int                `json:"engine_ids"`
	Links            []SnapshotLink       `json:"links"`
	Covers           []SnapshotCover      `json:"covers"`
	Screenshots      []SnapshotScreenshot `json:"screenshots"`
}

// SnapshotLink represents an external link in a snapshot
type SnapshotLink struct {
	Name      string `json:"name"`
	Link      string `json:"link"`
	Source    string `json:"source"`
	SourceKey string `json:"source_key"`
}

// SnapshotCover is one cover candidate in a Snapshot. Mirrors
// model.GalgameCover, minus the GalgameID (set by ApplySnapshot when
// writing) and Created (regenerated on write).
type SnapshotCover struct {
	ImageHash string `json:"image_hash"`
	SortOrder int    `json:"sort_order"`
	Sexual    int16  `json:"sexual"`
	Violence  int16  `json:"violence"`
	Source    string `json:"source"`
	SourceKey string `json:"source_key"`
	Kind      string `json:"kind"`
}

// SnapshotScreenshot is one gallery/CG entry in a Snapshot. Mirrors
// model.GalgameScreenshot in the same way as SnapshotCover.
type SnapshotScreenshot struct {
	ImageHash string `json:"image_hash"`
	SortOrder int    `json:"sort_order"`
	Caption   string `json:"caption"`
	Sexual    int16  `json:"sexual"`
	Violence  int16  `json:"violence"`
	Source    string `json:"source"`
	SourceKey string `json:"source_key"`
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
		ReleaseDate:      formatReleaseDate(g.ReleaseDate),
		ReleaseDateTBA:   g.ReleaseDateTBA,
		NameEnUS:         g.NameEnUS,
		NameJaJP:         g.NameJaJP,
		NameZhCN:         g.NameZhCN,
		NameZhTW:         g.NameZhTW,
		Banner:           g.Banner,
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
		Covers:           make([]SnapshotCover, 0),
		Screenshots:      make([]SnapshotScreenshot, 0),
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
		s.Links = append(s.Links, SnapshotLink{Name: l.Name, Link: l.Link, Source: l.Source, SourceKey: l.SourceKey})
	}
	for _, c := range g.Cover {
		s.Covers = append(s.Covers, SnapshotCover{
			ImageHash: c.ImageHash, SortOrder: c.SortOrder,
			Sexual: c.Sexual, Violence: c.Violence,
			Source: c.Source, SourceKey: c.SourceKey, Kind: c.Kind,
		})
	}
	for _, sh := range g.Screenshot {
		s.Screenshots = append(s.Screenshots, SnapshotScreenshot{
			ImageHash: sh.ImageHash, SortOrder: sh.SortOrder,
			Caption: sh.Caption,
			Sexual:  sh.Sexual, Violence: sh.Violence,
			Source: sh.Source, SourceKey: sh.SourceKey,
		})
	}

	// Sort for deterministic comparison. Image relations canonical-sort
	// by ImageHash — sort_order/caption/etc are row-level data that does
	// NOT influence the ordering key (so a sort-only re-order still shows
	// up as a change via row-level diff, not as a positional swap).
	sort.Strings(s.Aliases)
	sort.Ints(s.TagIDs)
	sort.Ints(s.OfficialIDs)
	sort.Ints(s.EngineIDs)
	sort.Slice(s.Covers, func(i, j int) bool { return s.Covers[i].ImageHash < s.Covers[j].ImageHash })
	sort.Slice(s.Screenshots, func(i, j int) bool { return s.Screenshots[i].ImageHash < s.Screenshots[j].ImageHash })

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
	if !strPtrEqual(old.ReleaseDate, new.ReleaseDate) {
		keys["release_date"] = true
	}
	if old.ReleaseDateTBA != new.ReleaseDateTBA {
		keys["release_date_tba"] = true
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
	if !coversEqual(old.Covers, new.Covers) {
		keys["covers"] = true
	}
	if !screenshotsEqual(old.Screenshots, new.Screenshots) {
		keys["screenshots"] = true
	}

	return keys
}

// KeysOf flattens a ChangedKeys-style set into a sorted string slice,
// suitable for GalgameRevision.SetChangedFields. Sorted so the stored
// changed_fields jsonb is deterministic (stable across re-runs / tests).
func KeysOf(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
	if changedKeys["release_date"] {
		target.ReleaseDate = source.ReleaseDate
	}
	if changedKeys["release_date_tba"] {
		target.ReleaseDateTBA = source.ReleaseDateTBA
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
	if changedKeys["covers"] {
		target.Covers = source.Covers
	}
	if changedKeys["screenshots"] {
		target.Screenshots = source.Screenshots
	}
}

// derefStr returns *p if p is non-nil, else "".
func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// formatReleaseDate renders the column value as a stable "YYYY-MM-DD"
// snapshot string. nil column → nil snapshot pointer (= unknown). The
// pointer-string form keeps JSON snapshots byte-stable across time zones
// (a *time.Time roundtrip can shift by hours and break ChangedKeys).
func formatReleaseDate(d *Date) *string {
	if d == nil {
		return nil
	}
	s := d.Time().UTC().Format("2006-01-02")
	return &s
}

// ParseSnapshotReleaseDate is the inverse of formatReleaseDate, used by
// repository.ApplySnapshot to write the date column from snapshot data.
// Returns nil for nil or empty input. Any non-empty string MUST already
// be a valid "YYYY-MM-DD" (validated upstream at the DTO layer).
func ParseSnapshotReleaseDate(s *string) *time.Time {
	if s == nil || *s == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02", *s)
	if err != nil {
		return nil
	}
	return &t
}

// ParseLegacyReleased maps a legacy `released` string (free-form, used by
// VNDB / Bangumi imports and the now-retired galgame.released column) into the
// typed (date, precision) pair. Single source of truth for migration, VNDB
// sync, and any backfill script. The date is normalized (day-unknown → 1st of
// month, month-unknown → Jan 1) and MUST be read together with the precision.
// Accepted inputs:
//
//	"" / "unknown":      unknown        → (nil, PrecisionUnknown)
//	"tba":               date pending   → (nil, PrecisionTBA)
//	"YYYY":              year only      → (Jan 1 YYYY, PrecisionYear)
//	"YYYY-MM":           year+month     → (1st of month, PrecisionMonth)
//	"YYYY-MM-DD":        full date      → (parsed, PrecisionDay)
//
// Years outside [1900, 2100] and anything else map to (nil, PrecisionUnknown).
// An out-of-range month/day degrades precision to the last fully-parsed level
// (e.g. "YYYY-13" → year, "YYYY-MM-45" → month). See
// docs/galgame_wiki/06-release-calendar-design.md §2.
func ParseLegacyReleased(s string) (*time.Time, ReleasePrecision) {
	v := s
	// trim spaces without pulling in strings package — micro-helper
	for len(v) > 0 && (v[0] == ' ' || v[0] == '\t') {
		v = v[1:]
	}
	for len(v) > 0 && (v[len(v)-1] == ' ' || v[len(v)-1] == '\t') {
		v = v[:len(v)-1]
	}
	if v == "" || v == "unknown" {
		return nil, PrecisionUnknown
	}
	if v == "tba" {
		return nil, PrecisionTBA
	}
	y, m, d := 0, 1, 1
	prec := PrecisionYear
	i, n := 0, len(v)
	for i < n && v[i] >= '0' && v[i] <= '9' {
		y = y*10 + int(v[i]-'0')
		i++
	}
	if i == 0 || y < 1900 || y > 2100 {
		return nil, PrecisionUnknown
	}
	if i < n && v[i] == '-' {
		i++
		mm := 0
		for i < n && v[i] >= '0' && v[i] <= '9' {
			mm = mm*10 + int(v[i]-'0')
			i++
		}
		if mm >= 1 && mm <= 12 {
			m = mm
			prec = PrecisionMonth
		}
		if i < n && v[i] == '-' {
			i++
			dd := 0
			for i < n && v[i] >= '0' && v[i] <= '9' {
				dd = dd*10 + int(v[i]-'0')
				i++
			}
			if dd >= 1 && dd <= 31 {
				d = dd
				prec = PrecisionDay
			}
		}
	}
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	return &t, prec
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

func strPtrEqual(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// stringSliceEqual / intSliceEqual compare as SETS (order-independent): they
// sort copies before comparing so an order-only reordering of the same
// elements is not flagged as a change. TakeSnapshot canonical-sorts these
// fields, but the PR (ApplyToSnapshot) and direct-edit (overlayUpdate) paths
// do not — without this, a PR that sends tag_ids in a different order than the
// base would register a spurious diff and could cause a false 字段冲突 on merge.
func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as := append([]string(nil), a...)
	bs := append([]string(nil), b...)
	sort.Strings(as)
	sort.Strings(bs)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

func intSliceEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	as := append([]int(nil), a...)
	bs := append([]int(nil), b...)
	sort.Ints(as)
	sort.Ints(bs)
	for i := range as {
		if as[i] != bs[i] {
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

// coversEqual compares two cover slices for ChangedKeys diffing, order-
// independently: it sorts copies by ImageHash first (matching TakeSnapshot's
// canonical key) so order-only reorderings aren't flagged. Element equality
// then covers every row-level field — change any of sort_order / sexual /
// violence / source / source_key and the snapshot key flips.
func coversEqual(a, b []SnapshotCover) bool {
	if len(a) != len(b) {
		return false
	}
	as := append([]SnapshotCover(nil), a...)
	bs := append([]SnapshotCover(nil), b...)
	sort.Slice(as, func(i, j int) bool { return as[i].ImageHash < as[j].ImageHash })
	sort.Slice(bs, func(i, j int) bool { return bs[i].ImageHash < bs[j].ImageHash })
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

// screenshotsEqual mirrors coversEqual (order-independent by ImageHash);
// Caption is included in the comparison because it is editable galgame
// metadata (per docs/galgame_wiki/09 §4.4).
func screenshotsEqual(a, b []SnapshotScreenshot) bool {
	if len(a) != len(b) {
		return false
	}
	as := append([]SnapshotScreenshot(nil), a...)
	bs := append([]SnapshotScreenshot(nil), b...)
	sort.Slice(as, func(i, j int) bool { return as[i].ImageHash < as[j].ImageHash })
	sort.Slice(bs, func(i, j int) bool { return bs[i].ImageHash < bs[j].ImageHash })
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}
