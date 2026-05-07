package main

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

// applyTwoPass runs both passes on a content string and returns the final
// state. Mirrors what the migration's transactional loop does for a single
// row: pass 1 (shift to +offset), then pass 2 (shift to new ids).
func applyTwoPass(content, urlPrefix string, mapping map[uint]uint, offset uint) string {
	pattern := regexp.MustCompile(regexp.QuoteMeta(urlPrefix) + `(\d+)/`)
	out, _ := rewriteMentionsInString(content, urlPrefix, pattern, mapping, true, offset)
	out, _ = rewriteMentionsInString(out, urlPrefix, pattern, mapping, false, offset)
	return out
}

const offset uint = 100_000_000

func TestMention_BasicReplace(t *testing.T) {
	mapping := map[uint]uint{30: 2}
	in := "[@鲲](/user/30/resource) 你好"
	want := "[@鲲](/user/2/resource) 你好"
	assert.Equal(t, want, applyTwoPass(in, "/user/", mapping, offset))
}

func TestMention_MultipleInOneRow(t *testing.T) {
	mapping := map[uint]uint{30: 2, 100: 5}
	in := "[@a](/user/30/profile) 回复 [@b](/user/100/comment)"
	want := "[@a](/user/2/profile) 回复 [@b](/user/5/comment)"
	assert.Equal(t, want, applyTwoPass(in, "/user/", mapping, offset))
}

// THE collision case: without two-pass offset, a single-pass implementation
// would chain-rewrite — first 5→2, then see the resulting 2 and rewrite
// to 8 — corrupting both refs.
func TestMention_CollisionResistance(t *testing.T) {
	// User A: old id 5 → new id 2
	// User B: old id 2 → new id 8
	// Same comment references BOTH users by their old ids.
	mapping := map[uint]uint{5: 2, 2: 8}
	in := "[@A](/user/5/x) and [@B](/user/2/y)"
	want := "[@A](/user/2/x) and [@B](/user/8/y)"
	assert.Equal(t, want, applyTwoPass(in, "/user/", mapping, offset))
}

func TestMention_UnmappedIDsLeftAlone(t *testing.T) {
	mapping := map[uint]uint{30: 2}
	// id=999 isn't in the mapping (e.g. a deleted user the script didn't migrate)
	in := "[@kun](/user/30/x) [@ghost](/user/999/y)"
	want := "[@kun](/user/2/x) [@ghost](/user/999/y)"
	assert.Equal(t, want, applyTwoPass(in, "/user/", mapping, offset))
}

func TestMention_NoMatchesNoChange(t *testing.T) {
	mapping := map[uint]uint{30: 2}
	in := "no mentions here, just text"
	assert.Equal(t, in, applyTwoPass(in, "/user/", mapping, offset))
}

func TestMention_PrefixDistinguishedFromOtherURLs(t *testing.T) {
	mapping := map[uint]uint{30: 2}
	// `/avatar/user_30/...` shouldn't be touched — it's not /user/<id>/
	in := "see [@kun](/user/30/x), avatar https://cdn/avatar/user_30/avatar.webp"
	want := "see [@kun](/user/2/x), avatar https://cdn/avatar/user_30/avatar.webp"
	assert.Equal(t, want, applyTwoPass(in, "/user/", mapping, offset))
}

// rewriteMentionsInString must be idempotent on a content that already went
// through both passes — running pass1 again on final content should be a
// no-op (since the new ids aren't in the mapping's keyset… for THIS test
// case where keys and values don't overlap).
func TestMention_NotIdempotentWhenKeyValueOverlaps(t *testing.T) {
	// Documenting the constraint: this case is exactly why the two passes
	// MUST run in the same transaction. If pass1+pass2 commit, then a
	// third (re-run) pass1 on `/user/2/...` would re-shift it because
	// 2 IS in mapping.keys here.
	mapping := map[uint]uint{5: 2, 2: 8}
	once := applyTwoPass("[@A](/user/5/x)", "/user/", mapping, offset)
	assert.Equal(t, "[@A](/user/2/x)", once)

	twice := applyTwoPass(once, "/user/", mapping, offset)
	// On the SECOND apply, /user/2/ matches mapping[2]=8 and gets rewritten.
	// This is a known property — the migration runs both passes inside one
	// transaction so re-runs after commit don't see the post-state.
	assert.Equal(t, "[@A](/user/8/x)", twice,
		"two-pass is not safe to re-run on already-migrated data; protect with backup + transactional execution")
}

func TestMention_PassOnePassTwoSeparately(t *testing.T) {
	// Verify the intermediate state between passes is what we expect:
	// pass1 shifts to offset, pass2 finalizes.
	mapping := map[uint]uint{30: 2}
	pattern := regexp.MustCompile(`/user/(\d+)/`)

	pass1, changed := rewriteMentionsInString("[@k](/user/30/x)", "/user/", pattern, mapping, true, offset)
	assert.True(t, changed)
	assert.Equal(t, "[@k](/user/100000030/x)", pass1)

	pass2, changed := rewriteMentionsInString(pass1, "/user/", pattern, mapping, false, offset)
	assert.True(t, changed)
	assert.Equal(t, "[@k](/user/2/x)", pass2)
}
