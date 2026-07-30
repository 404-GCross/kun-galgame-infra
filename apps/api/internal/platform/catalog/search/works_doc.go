// works_doc.go — how a registry row becomes a works / tags index document.
//
// This lives next to the index SETTINGS on purpose: the filterable and sortable
// attribute lists above are a promise about which fields exist on a document,
// and the builder below is what keeps it. cmd/reindex-catalog does the data
// plumbing (which rows, which joins, which batches); the projection itself is
// here so a test can build the exact document production builds instead of a
// hand-rolled look-alike that is free to drift.
package search

import (
	"math"
	"strconv"
)

// GuessLang buckets a bare name (catalog_work.display_name and catalog_tag.name
// carry no lang column) for the CJK-locale index fields: kana → ja; han-only →
// zh (a kanji-only Japanese title tokenizes acceptably under cmn — CJK
// ideographs segment alike); else latin/other. A wrong guess between zh and ja
// costs tokenization nuance, never a miss.
func GuessLang(s string) string {
	hasHan := false
	for _, r := range s {
		if (r >= 'ぁ' && r <= 'ん') || (r >= 'ァ' && r <= 'ヶ') || r == 'ー' {
			return "ja"
		}
		if r >= 0x4E00 && r <= 0x9FFF {
			hasHan = true
		}
	}
	if hasHan {
		return "zh"
	}
	return "en"
}

// WorkDocTitle is one catalog_work_title row as the projection needs it,
// supplied in (kind, lang) order so the official title wins its language
// bucket and the rest become aliases.
type WorkDocTitle struct {
	Lang  string
	Title string
	Latin string
}

// WorkDocIntro is one synopsis row as the projection needs it: the language
// tag (BCP-47, the same values the read face merges on) and the body.
type WorkDocIntro struct {
	Lang string
	Text string
}

// WorkDocInput is a whole works document's worth of registry state.
type WorkDocInput struct {
	ID            int64
	DisplayName   string
	OLang         string
	ContentRating int16
	// Claimed is `site <> ''` on the registry row — a product site owns it.
	Claimed bool
	// ClaimState is the public claim vocabulary value (none|live|draft|hidden),
	// supplied by the caller through model.ClaimStateKey so the index and the
	// read face project the same registry columns exactly alike (A2-R1 区 C).
	ClaimState string
	// ReleasedOrd is the composed ordinal of the earliest year-carrying
	// release; 0 = the work has no dated release (the field is then omitted).
	ReleasedOrd int64
	UpdatedTS   int64
	Popularity  float64
	Sources     []string
	SourceKeys  []string
	Titles      []WorkDocTitle
	TagIDs      []int64
	LabelIDs    []int64
	EngineIDs   []int64
	SeriesIDs   []int64
	// Intros are the work's synopses (A2-1f), already merged to one row per
	// language by the caller — the same set the read face would serve, so
	// "searchable" and "readable" cannot describe different text.
	Intros []WorkDocIntro
}

// BuildWorkDoc projects one registry work to its works-index document.
//
// display_name claims its language bucket first, then each title row claims a
// still-empty bucket or becomes an alias; a title already seen verbatim is
// skipped so a work whose display_name IS its official title does not carry a
// duplicate alias. The first non-empty latin transliteration wins.
func BuildWorkDoc(in WorkDocInput) EntityDoc {
	rating, claimed := in.ContentRating, in.Claimed
	d := EntityDoc{
		ID: WorkDocID(in.ID), EntityType: "work",
		Sources: in.Sources, SourceKeys: in.SourceKeys,
		ContentRating: &rating, Popularity: in.Popularity,
		Claimed: &claimed, ClaimState: in.ClaimState, OLang: in.OLang,
		TagIDs: in.TagIDs, LabelIDs: in.LabelIDs,
		EngineIDs: in.EngineIDs, SeriesIDs: in.SeriesIDs,
		ReleasedOrd: in.ReleasedOrd, UpdatedTS: in.UpdatedTS,
	}
	seen := map[string]bool{in.DisplayName: true}
	d.SetNameOrAlias(GuessLang(in.DisplayName), in.DisplayName)
	for _, t := range in.Titles {
		if d.Latin == "" && t.Latin != "" {
			d.Latin = t.Latin
		}
		if seen[t.Title] {
			continue
		}
		seen[t.Title] = true
		lang := t.Lang
		if lang == "" {
			lang = GuessLang(t.Title)
		}
		d.SetNameOrAlias(lang, t.Title)
	}
	for _, in := range in.Intros {
		d.SetIntro(in.Lang, in.Text)
	}
	d.TruncateIntros()
	return d
}

// TagDocInput is a canonical-tag document's worth of registry state. WorkCount
// is the raw number of works carrying the tag; the projection log-damps it into
// the popularity tiebreaker, exactly as a credit count is damped for entities.
type TagDocInput struct {
	ID         int64
	Name       string
	Tier       int16
	Kind       int16
	WorkCount  int
	Sources    []string
	SourceKeys []string
}

// BuildTagDoc projects one canonical tag to its tags-index document.
func BuildTagDoc(in TagDocInput) EntityDoc {
	tier, kind := in.Tier, in.Kind
	d := EntityDoc{
		ID: TagDocID(in.ID), EntityType: "tag",
		Sources: in.Sources, SourceKeys: in.SourceKeys,
		Tier: &tier, Kind: &kind,
		Popularity: math.Log1p(float64(in.WorkCount)),
	}
	d.SetName(GuessLang(in.Name), in.Name)
	return d
}

// TagDocID renders a canonical tag id as its tags-index primary key.
func TagDocID(tagID int64) string { return "t" + strconv.FormatInt(tagID, 10) }
