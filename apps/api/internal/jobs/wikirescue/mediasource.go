package wikirescue

import (
	"context"
	"fmt"
)

// The galgame media tables carry provenance as a free-text `source` column.
// These are the catalog_source keys those strings map onto.
const (
	mediaSourceVNDB    = "vndb"
	mediaSourceBangumi = "bangumi"
	mediaSourceUpscale = "upscale"
)

// mediaSourceKey maps galgame_cover.source / galgame_screenshot.source onto a
// catalog_source key. It is the SAME mapping the catalog read face applies when
// it bridges those very rows (service.galgameMediaSourceKey), so a projected row
// keeps exactly the attribution a reader sees today.
//
// The empty string maps to galgame_wiki, and so does any value missing from this
// table. That is not a fallback of convenience: an unsourced cover row is the
// June banner migration's output, i.e. wiki curation, which IS the galgame_wiki
// source. Returning 0 instead would violate the NOT NULL source_id and, worse,
// silently mis-attribute the row if the column ever gained a default.
var mediaSourceKey = map[string]string{
	"":                 sourceKeyWiki,
	mediaSourceVNDB:    mediaSourceVNDB,
	mediaSourceBangumi: mediaSourceBangumi,
	mediaSourceUpscale: mediaSourceUpscale,
}

// mediaSourceIDs resolves every catalog_source key mediaSourceKey can produce to
// its id, up front and in one place. Resolving eagerly turns an unseeded source
// into one clear error before the first INSERT instead of a foreign-key abort
// halfway through a 110k-row batch.
func (r *Runner) mediaSourceIDs(ctx context.Context) (map[string]int16, error) {
	db := r.catalog.WithContext(ctx)
	ids := make(map[string]int16, len(mediaSourceKey))
	for _, key := range mediaSourceKey {
		if _, done := ids[key]; done {
			continue
		}
		id, err := resolveSourceID(db, key)
		if err != nil {
			return nil, fmt.Errorf("media source %q: %w", key, err)
		}
		ids[key] = id
	}
	return ids, nil
}

// mediaSourceID picks the catalog_source id for one raw `source` value.
func mediaSourceID(ids map[string]int16, raw string) int16 {
	key, known := mediaSourceKey[raw]
	if !known {
		key = sourceKeyWiki
	}
	return ids[key]
}
