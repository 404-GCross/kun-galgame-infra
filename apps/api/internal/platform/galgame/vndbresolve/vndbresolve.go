// Package vndbresolve maps VNDB tags / developers to wiki galgame_tag /
// galgame_official ids, creating them when missing. It is the single source of
// truth for that resolution, shared by the draft sync (internal/jobs/vndbsync)
// and the published-game enrichment (service.ReconcileVndbTags/Officials), so
// both create entities with identical naming and never diverge (the two had
// drifted: draft sync created English tag names, the old relations bypass
// created Chinese).
//
// Tag naming: tagMap (English→Chinese) when mapped, English original otherwise —
// the wiki is Chinese-facing and tagMap is its localization layer. To avoid
// adding duplicates to the existing mixed set, ResolveTag REUSES any existing
// match (Chinese OR English) before creating the canonical (Chinese) row.
// Official naming: VNDB `original` (native script) preferred, `name` (romaji)
// fallback; reuse by either.
package vndbresolve

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"api/internal/platform/galgame/model"

	"gorm.io/gorm"
)

// TagRatingMin is the minimum VNDB tag rating to keep (drops noise tags).
const TagRatingMin = 1.0

// Tag / Developer are the minimal VNDB inputs the resolver needs. Callers copy
// from their own fetched DTOs, decoupling this package from any HTTP client.
type Tag struct {
	ID       string
	Name     string
	Category string // VNDB: cont / ero / tech
	Rating   float64
	Spoiler  int
	Lie      bool
}

type Developer struct {
	ID       string
	Name     string
	Original string
	Type     string // VNDB: co / in / ng
	Lang     string
}

// TagSkipped reports VNDB tags that must not be applied: fabrications (`lie`) or
// below the rating floor.
func TagSkipped(t Tag) bool { return t.Lie || t.Rating < TagRatingMin }

// Resolver caches name→id lookups and creates missing entities. Not safe for
// concurrent use — create one per sync run; scope DB writes through the tx
// passed to each call.
type Resolver struct {
	tagMap        map[string]string // english → chinese
	tagCache      map[string]int    // tag name (zh or en) → id
	officialCache map[string]int    // official name (name or original) → id
	newTags       int
	newOfficials  int
}

func New(tagMap map[string]string) *Resolver {
	return &Resolver{
		tagMap:        tagMap,
		tagCache:      make(map[string]int),
		officialCache: make(map[string]int),
	}
}

// LoadCaches preloads existing tags/officials so ResolveTag/ResolveOfficial
// reuse them instead of creating duplicates.
func (r *Resolver) LoadCaches(db *gorm.DB) error {
	var tags []model.GalgameTag
	if err := db.Find(&tags).Error; err != nil {
		return err
	}
	for _, t := range tags {
		r.tagCache[t.Name] = t.ID
	}
	var officials []model.GalgameOfficial
	if err := db.Find(&officials).Error; err != nil {
		return err
	}
	for _, o := range officials {
		r.officialCache[o.Name] = o.ID
		if o.Original != "" {
			r.officialCache[o.Original] = o.ID
		}
	}
	return nil
}

func (r *Resolver) NewTags() int      { return r.newTags }
func (r *Resolver) NewOfficials() int { return r.newOfficials }

// LookupTag / LookupOfficial resolve against the loaded caches only — no
// creation. Used by the enrichment's dry-run so a preview never writes new
// entities (a missing tag/official simply isn't counted).
func (r *Resolver) LookupTag(t Tag) (int, bool) {
	if zh, ok := r.tagMap[t.Name]; ok {
		if id, ok := r.tagCache[zh]; ok {
			return id, true
		}
	}
	id, ok := r.tagCache[t.Name]
	return id, ok
}

func (r *Resolver) LookupOfficial(d Developer) (int, bool) {
	if id, ok := r.officialCache[d.Name]; ok {
		return id, true
	}
	if d.Original != "" {
		if id, ok := r.officialCache[d.Original]; ok {
			return id, true
		}
	}
	return 0, false
}

// ResolveTag returns the wiki galgame_tag.id for a VNDB tag, reusing an existing
// Chinese or English row, else creating the canonical (Chinese when mapped) row.
// Returns a non-nil error only on a genuine create failure (NOT a unique-name
// race, which is recovered by re-read) — callers MUST treat that as fatal for the
// galgame, because silently dropping a tag would let a reconcile delete a valid
// existing relation (the desired set is authoritative).
func (r *Resolver) ResolveTag(tx *gorm.DB, t Tag) (int, error) {
	zh, mapped := r.tagMap[t.Name]
	// Reuse existing first — Chinese (canonical) then English — so a tag already
	// present under either name is never duplicated.
	if mapped {
		if id, ok := r.tagCache[zh]; ok {
			return id, nil
		}
	}
	if id, ok := r.tagCache[t.Name]; ok {
		return id, nil
	}
	name := t.Name
	if mapped {
		name = zh
	}
	newTag := model.GalgameTag{Name: name, Category: mapTagCategory(t.Category)}
	if err := tx.Create(&newTag).Error; err != nil {
		// Unique-constraint race / pre-existing row — re-read. If the re-read
		// itself errors (e.g. the tx is already aborted), surface the original.
		var existing model.GalgameTag
		if e := tx.Where("name = ?", name).Limit(1).Find(&existing).Error; e == nil && existing.ID != 0 {
			r.tagCache[name] = existing.ID
			return existing.ID, nil
		}
		return 0, fmt.Errorf("create tag %q: %w", name, err)
	}
	r.tagCache[name] = newTag.ID
	r.newTags++
	return newTag.ID, nil
}

// ResolveOfficial returns the wiki galgame_official.id for a VNDB developer,
// matching by name or original, else creating one (original-preferred name).
// Error semantics match ResolveTag.
func (r *Resolver) ResolveOfficial(tx *gorm.DB, d Developer) (int, error) {
	if id, ok := r.officialCache[d.Name]; ok {
		return id, nil
	}
	if d.Original != "" {
		if id, ok := r.officialCache[d.Original]; ok {
			return id, nil
		}
	}
	primary := d.Original
	if primary == "" {
		primary = d.Name
	}
	off := model.GalgameOfficial{Name: primary, Original: d.Original, Category: mapDevType(d.Type), Lang: d.Lang}
	if err := tx.Create(&off).Error; err != nil {
		var existing model.GalgameOfficial
		if e := tx.Where("name = ?", primary).Limit(1).Find(&existing).Error; e == nil && existing.ID != 0 {
			r.cacheOfficial(d, existing.ID)
			return existing.ID, nil
		}
		return 0, fmt.Errorf("create official %q: %w", primary, err)
	}
	r.cacheOfficial(d, off.ID)
	r.newOfficials++
	return off.ID, nil
}

func (r *Resolver) cacheOfficial(d Developer, id int) {
	if d.Name != "" {
		r.officialCache[d.Name] = id
	}
	if d.Original != "" {
		r.officialCache[d.Original] = id
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

// DefaultTagMapPath resolves the tagMap path: env override, else repo-root
// default (consistent with other path-style config defaults).
func DefaultTagMapPath() string {
	if p := os.Getenv("KUN_VNDB_TAGMAP_PATH"); p != "" {
		return p
	}
	return "docs/tagMap.ts"
}

// ParseTagMap reads docs/tagMap.ts into an english→chinese map. It must tolerate
// every shape the file actually uses, or the resolver misses a key's Chinese
// mapping and the sync keeps recreating that tag under its English name:
//   - keys single-quoted, double-quoted (when the key has an apostrophe, e.g.
//     "Protagonist's Pronoun Choice"), or bareword incl. non-ASCII (Pokémon);
//   - long entries Prettier-wraps onto two lines (`key:\n    'value'`).
var (
	// tagMapWrapRegex rejoins a Prettier-wrapped `key:\n    'value'` so the
	// line-oriented match below sees `key: 'value'`. It only fires on a `:` that
	// is followed (after whitespace/newline) by the opening value quote, so it
	// never merges two separate single-line entries (those have `,` before the
	// newline, not `:`).
	tagMapWrapRegex = regexp.MustCompile(`:[ \t]*\r?\n[ \t]*'`)
	tagMapLineRegex = regexp.MustCompile(`^\s*(?:'([^']+)'|"([^"]+)"|([\pL\pN_][\pL\pN_\s./()\-]*[\pL\pN_]|[\pL\pN_]))\s*:\s*'([^']+)'`)
)

func ParseTagMap(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	joined := tagMapWrapRegex.ReplaceAllString(string(data), ": '")
	result := make(map[string]string)
	for _, line := range strings.Split(joined, "\n") {
		m := tagMapLineRegex.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key := m[1]
		if key == "" {
			key = m[2]
		}
		if key == "" {
			key = m[3]
		}
		if value := m[4]; key != "" && value != "" {
			result[key] = value
		}
	}
	return result, nil
}
