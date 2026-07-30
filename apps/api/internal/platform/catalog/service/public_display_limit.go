// public_display_limit.go — the whole EDITORIAL DISPLAY axis on the service
// side (A2-R5): the closed content_limit= vocabulary, the SQL twin of
// model.DisplayLimitKey, and the wiki-body loader the read face projects with.
//
// model.DisplayLimitKey is the single semantic source (sfw|nsfw). It cannot run
// inside a WHERE clause, though, so the faces that filter IN THE DATABASE — the
// works list and the calendar buckets — need the same projection expressed as a
// predicate. This file is that one expression, kept beside the loader so there
// is a single place to audit, and pinned row-for-row against the Go projection
// by TestDisplayLimitWhereMatchesProjection — the claim_state axis's
// TestClaimStateWhereMatchesProjection, applied to the second axis.
//
// The claimed branch reads the WIKI body (galgame.content_limit). galgame lives
// in the same database as the registry, so the predicate is a plain correlated
// EXISTS and the loader a plain batched IN — the same same-pool posture
// read_titles.go's claimed title bridge already relies on.
package service

import (
	"context"
	"strings"

	"api/internal/platform/catalog/model"
)

// DisplayLimitTokens is the CLOSED content_limit= vocabulary in the order the
// handler quotes it and the spec documents it — model.DisplayLimitKey's whole
// range, so a caller filters on the same two words a record renders.
var DisplayLimitTokens = []string{model.DisplayLimitKeySFW, model.DisplayLimitKeyNSFW}

// IsDisplayLimit reports whether a token is in that vocabulary. A token outside
// it is a LOUD 400 at the handler, never a silently-ignored filter: a consumer
// asking for `content_limit=sfw` and getting a 200 full of adult display
// material is the failure this axis was added to prevent.
func IsDisplayLimit(tok string) bool {
	for _, v := range DisplayLimitTokens {
		if tok == v {
			return true
		}
	}
	return false
}

// wikiNSFWSQL is "the wiki body says this work's display material is NSFW",
// over the catalog_work alias w — the claimed branch of model.DisplayLimitKey.
//
// The site check is load-bearing, not decoration: product_work_id is an id in
// the CLAIMING site's own space, so joining it to galgame is only meaningful
// for a galgame_wiki claim. A claim from any other site therefore has no wiki
// body to read and falls to sfw, exactly as the Go projection does when its
// caller (which resolves the body through the same site test) hands it "".
const wikiNSFWSQL = `(w.site = '` + siteGalgameWiki + `' AND EXISTS (
	SELECT 1 FROM galgame g WHERE g.id = w.product_work_id AND g.content_limit = '` + model.WikiContentLimitNSFW + `'))`

// displayLimitWhere compiles a content_limit= filter into ONE SQL predicate plus
// its bind args; an empty list returns an empty predicate, i.e. no gate at all
// (absent means both values, so pre-existing callers stay byte-identical).
//
// The two branches mirror model.DisplayLimitKey exactly:
//
//	nsfw → (claimed AND the wiki body says nsfw) OR (bodyless AND content_rating = r18)
//	sfw  → (claimed AND it does not)             OR (bodyless AND content_rating <> r18)
//
// They are complements of one another over the whole table, so the two are a
// PARTITION: each row satisfies exactly one, and a caller naming both gets the
// ungated set back. Note that the claimed half never consults content_rating and
// the bodyless half never consults the wiki — that asymmetry IS the axis (a
// claimed r18 game with editorially safe cover art is `sfw` here, which is the
// 5,568-work majority the age axis was mis-hiding).
//
// Several values OR into one parenthesized group that ANDs with every other
// filter — the same one-door shape claimStateWhere and the search face's
// Meilisearch expression have, so counts and rows can never end up behind
// different gates.
func displayLimitWhere(limits []string) (string, []any) {
	if len(limits) == 0 {
		return "", nil
	}
	ors := make([]string, 0, len(limits))
	args := make([]any, 0, len(limits))
	for _, lim := range limits {
		switch lim {
		case model.DisplayLimitKeyNSFW:
			ors = append(ors, "("+claimedSQL+" AND "+wikiNSFWSQL+") OR (NOT "+claimedSQL+" AND w.content_rating = ?)")
			args = append(args, model.ContentRatingR18)
		case model.DisplayLimitKeySFW:
			ors = append(ors, "("+claimedSQL+" AND NOT "+wikiNSFWSQL+") OR (NOT "+claimedSQL+" AND w.content_rating <> ?)")
			args = append(args, model.ContentRatingR18)
		default:
			// Unreachable — the handler 400s every token outside the vocabulary
			// before it gets here. Match nothing rather than widen the gate.
			ors = append(ors, "false")
		}
	}
	return "((" + strings.Join(ors, ") OR (") + "))", args
}

// loadWikiContentLimits batch-loads galgame.content_limit for the CLAIMED works
// among subjects, keyed by CATALOG work id (a bodyless work, or a claimed work
// whose body is gone, is simply absent — "" is what model.DisplayLimitKey wants
// for both). ONE query for the whole set, never one per work: this runs on every
// works-list / search / calendar page and on every work detail.
//
// It reuses partitionClaimSubjects, so "which works have a wiki body" is decided
// by the same split every other bridged facet (titles / intros / covers) uses —
// the display axis cannot end up reading a body the title bridge would not.
func (s *ReadService) loadWikiContentLimits(ctx context.Context, subjects []claimSubject) (map[int64]string, error) {
	out := make(map[int64]string, len(subjects))
	galgameIDs, galgameToWork, _ := partitionClaimSubjects(subjects)
	if len(galgameIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		ID           int64  `gorm:"column:id"`
		ContentLimit string `gorm:"column:content_limit"`
	}
	if err := s.db.WithContext(ctx).Raw(
		`SELECT id, coalesce(content_limit, '') AS content_limit FROM galgame WHERE id IN ?`,
		galgameIDs).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		if workID, ok := galgameToWork[r.ID]; ok {
			out[workID] = r.ContentLimit
		}
	}
	return out, nil
}
