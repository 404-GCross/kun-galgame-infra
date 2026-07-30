// public_display_limit.go — the whole EDITORIAL DISPLAY axis on the service
// side (A2-R5): the closed content_limit= vocabulary, the SQL twin of
// model.DisplayLimitKey, and the loader the read face projects with.
//
// model.DisplayLimitKey is the single semantic source (sfw|nsfw). It cannot run
// inside a WHERE clause, though, so the faces that filter IN THE DATABASE — the
// works list and the calendar buckets — need the same projection expressed as a
// predicate. This file is that one expression, kept beside the loader so there
// is a single place to audit, and pinned row-for-row against the Go projection
// by TestDisplayLimitWhereMatchesProjection — the claim_state axis's
// TestClaimStateWhereMatchesProjection, applied to the second axis.
//
// The claimed branch read the WIKI body (galgame.content_limit) until the W1-pre
// nativization (refs/proj/140 §5b) mirrored that editorial flag onto
// catalog_work.display_nsfw. Both halves of this file therefore read ONE table
// now: the predicate is a plain column test instead of a correlated EXISTS, and
// the loader a plain batched IN over the registry itself.
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

// displayNSFWSQL is "an editor declared this work's display material NSFW", over
// the catalog_work alias w — the claimed branch of model.DisplayLimitKey, which is
// now just the column.
//
// No site test any more, and none needed: display_nsfw is only ever written for a
// galgame_wiki claim (the mirror step's ownership scope) and every other row keeps
// the migration's false, so a claim from another site still falls to sfw exactly as
// the Go projection does. The predicate that used to carry that test read the wiki
// body through a correlated EXISTS.
const displayNSFWSQL = `w.display_nsfw`

// displayLimitWhere compiles a content_limit= filter into ONE SQL predicate plus
// its bind args; an empty list returns an empty predicate, i.e. no gate at all
// (absent means both values, so pre-existing callers stay byte-identical).
//
// The two branches mirror model.DisplayLimitKey exactly:
//
//	nsfw → (claimed AND display_nsfw)     OR (bodyless AND content_rating = r18)
//	sfw  → (claimed AND NOT display_nsfw) OR (bodyless AND content_rating <> r18)
//
// They are complements of one another over the whole table, so the two are a
// PARTITION: each row satisfies exactly one, and a caller naming both gets the
// ungated set back. Note that the claimed half never consults content_rating and
// the bodyless half never consults the editorial flag — that asymmetry IS the axis (a
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
			ors = append(ors, "("+claimedSQL+" AND "+displayNSFWSQL+") OR (NOT "+claimedSQL+" AND w.content_rating = ?)")
			args = append(args, model.ContentRatingR18)
		case model.DisplayLimitKeySFW:
			ors = append(ors, "("+claimedSQL+" AND NOT "+displayNSFWSQL+") OR (NOT "+claimedSQL+" AND w.content_rating <> ?)")
			args = append(args, model.ContentRatingR18)
		default:
			// Unreachable — the handler 400s every token outside the vocabulary
			// before it gets here. Match nothing rather than widen the gate.
			ors = append(ors, "false")
		}
	}
	return "((" + strings.Join(ors, ") OR (") + "))", args
}

// loadDisplayNSFW batch-loads the editorial display flag for a set of works, keyed
// by work id. Works whose flag is FALSE are simply absent from the map, which reads
// false — the absent-means-false shape tagSexualFor uses, and the same thing the
// wiki-body loader this replaced produced for a work with no body.
//
// ONE query for the whole set, never one per work: this runs on every works-list /
// search / calendar page and on every work detail. No claim split: only a claimed
// row is ever true (the mirror's ownership scope) and only a claimed row's value is
// ever consulted (model.DisplayLimitKey reads the age axis for a bodyless one), so
// the flag can be loaded by work id and let the projection decide.
func (s *ReadService) loadDisplayNSFW(ctx context.Context, subjects []claimSubject) (map[int64]bool, error) {
	out := make(map[int64]bool, len(subjects))
	if len(subjects) == 0 {
		return out, nil
	}
	workIDs := make([]int64, 0, len(subjects))
	for _, sub := range subjects {
		workIDs = append(workIDs, sub.WorkID)
	}
	var ids []int64
	if err := s.db.WithContext(ctx).Raw(
		`SELECT id FROM catalog_work WHERE id IN ? AND display_nsfw`, workIDs).Scan(&ids).Error; err != nil {
		return nil, err
	}
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}
