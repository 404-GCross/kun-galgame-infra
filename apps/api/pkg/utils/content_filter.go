package utils

// ParseContentLimit normalizes the `content_limit` query parameter into the
// canonical value that repository / search code expects in their WHERE
// clause:
//
//   - "sfw"  → "sfw"  (SQL: WHERE content_limit = 'sfw')
//   - "nsfw" → "nsfw" (SQL: WHERE content_limit = 'nsfw')
//   - "all"  → ""     (no WHERE clause — explicit opt-in to include both)
//   - ""     → defaultWhenEmpty (caller-chosen default for missing param)
//   - other  → defaultWhenEmpty (unknown values fall back to default,
//                                avoids accidental "ssfw" leakage)
//
// Default selection is the handler's call, not this helper's:
//
//   - Browse / search / aggregate endpoints pass "sfw" as default, giving
//     safe-by-default behavior (callers must opt into NSFW with
//     `?content_limit=all` or `?content_limit=nsfw`).
//   - Direct / batch endpoints where the caller already knows specific IDs
//     pass "" as default (no filter), so e.g. a favorites batch returns
//     every game the user explicitly favorited.
//
// See docs/integration/galgame_wiki/00-handbook-for-downstream.md §NSFW
// for the wire-level contract.
func ParseContentLimit(raw, defaultWhenEmpty string) string {
	switch raw {
	case "sfw", "nsfw":
		return raw
	case "all":
		return ""
	default:
		return defaultWhenEmpty
	}
}
