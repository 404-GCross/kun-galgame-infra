package wikirescue

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"

	"gorm.io/gorm"
)

// matchedByLink is the provenance rule stamped on every ref this step writes.
const matchedByLink = "wiki:link"

// Link-parsing patterns. Each recognizes ONE source's canonical URL shape and
// yields that source's native external_id; anything unrecognized degrades to
// the generic `web` source carrying the full URL, so no link is ever dropped
// for want of a parser.
var (
	reArchiveWrap = regexp.MustCompile(`(?i)web\.archive\.org/web/[^/]+/(https?://.+)$`)
	reTwitter     = regexp.MustCompile(`(?i)^https?://(?:www\.|mobile\.)?(?:twitter\.com|x\.com)/@?([A-Za-z0-9_]{1,30})(?:[/?#]|$)`)
	reCien        = regexp.MustCompile(`(?i)^https?://ci-en\.dlsite\.com/creator/(\d+)`)
	reSteam       = regexp.MustCompile(`(?i)^https?://store\.steampowered\.com/app/(\d+)`)
	rePixivUsers  = regexp.MustCompile(`(?i)^https?://(?:www\.)?pixiv\.net/(?:en/)?users/(\d+)`)
	rePixivMember = regexp.MustCompile(`(?i)^https?://(?:www\.)?pixiv\.net/member\.php\?id=(\d+)`)
	reDlsiteHost  = regexp.MustCompile(`(?i)^https?://(?:www\.)?dlsite\.com/`)
	reDlsiteWork  = regexp.MustCompile(`(?i)\b((?:RJ|VJ|BJ)\d{5,10})\b`)
	reDMMHost     = regexp.MustCompile(`(?i)^https?://(?:[a-z0-9-]+\.)*dmm\.(?:co\.jp|com)/`)
	// DMM product codes appear in two URL shapes — the storefront's cid= query
	// parameter and dlsoft's /detail/<code>/ path — and BOTH carry the same
	// namespace the existing source-15 refs use (clear_0017, d_186489,
	// 587apc13913, imported from the EG mirror's typed dmm column). Matching
	// both keeps this step inside that namespace instead of minting a second one.
	// DMM writes cid= as a PATH segment (/-/detail/=/cid=xxx/) as often as a
	// query parameter, so the delimiter class has to include '/'.
	reDMMCid    = regexp.MustCompile(`(?i)[?&/]cid=([a-z0-9_]+)`)
	reDMMDetail = regexp.MustCompile(`(?i)/detail/([a-z0-9_]+)/?`)
)

// twitterReserved are x.com paths that are site features, not handles. Taking
// them as a handle would mint a nonsense identity, so they degrade to `web`.
var twitterReserved = map[string]bool{
	"i": true, "home": true, "intent": true, "share": true,
	"search": true, "hashtag": true, "explore": true, "messages": true,
}

// parkedLink is one user-entered link whose work never got an anchor.
type parkedLink struct {
	LinkID    int64  `json:"link_id"`
	GalgameID int64  `json:"galgame_id"`
	Name      string `json:"name"`
	Link      string `json:"link"`
}

// classifyLink maps a raw wiki link onto (source key, external_id). The second
// return is false only for input that is not a URL at all.
func classifyLink(raw string) (string, string, bool) {
	u := strings.TrimSpace(raw)
	if m := reArchiveWrap.FindStringSubmatch(u); m != nil {
		u = strings.TrimSpace(m[1])
	}
	if !strings.HasPrefix(strings.ToLower(u), "http://") && !strings.HasPrefix(strings.ToLower(u), "https://") {
		return "", "", false
	}
	if m := reTwitter.FindStringSubmatch(u); m != nil {
		handle := strings.ToLower(m[1])
		if !twitterReserved[handle] {
			return "twitter", handle, true
		}
	}
	if m := reCien.FindStringSubmatch(u); m != nil {
		return "cien", m[1], true
	}
	if m := reSteam.FindStringSubmatch(u); m != nil {
		return "steam", m[1], true
	}
	if m := rePixivUsers.FindStringSubmatch(u); m != nil {
		return "pixiv", m[1], true
	}
	if m := rePixivMember.FindStringSubmatch(u); m != nil {
		return "pixiv", m[1], true
	}
	if reDlsiteHost.MatchString(u) {
		if m := reDlsiteWork.FindStringSubmatch(u); m != nil {
			return "dlsite", strings.ToUpper(m[1]), true
		}
		// No workno in the URL (a circle/announce page) — degrade rather than
		// invent an id, exactly as the charter's recipe specifies.
		return "web", u, true
	}
	if reDMMHost.MatchString(u) {
		if m := reDMMCid.FindStringSubmatch(u); m != nil {
			return "dmm", strings.ToLower(m[1]), true
		}
		if m := reDMMDetail.FindStringSubmatch(u); m != nil {
			return "dmm", strings.ToLower(m[1]), true
		}
		return "web", u, true
	}
	return "web", u, true
}

// stepLink rescues the non-VNDB links (charter ruling 3) into catalog_external_ref
// as entity_type=work rows.
//
// Every row is link_kind=RELATED. That is a hard boundary: a related ref must
// never participate in identity resolution, and this step is forbidden from
// writing link_kind=exact — minting identity anchors belongs to the importer
// lanes, which grade their candidates. The partial unique index that guards
// exact refs only covers link_kind=0, so these rows are unconstrained by it.
func (r *Runner) stepLink(ctx context.Context) (Stats, error) {
	st := Stats{Step: "c"}

	sourceIDs := map[string]int16{}
	for _, key := range []string{"twitter", "cien", "steam", "pixiv", "dlsite", "dmm", "web"} {
		id, err := resolveSourceID(r.catalog, key)
		if err != nil {
			return st, err
		}
		sourceIDs[key] = id
	}

	type link struct {
		ID        int64
		GalgameID int64
		Name      string
		Link      string
	}
	var links []link
	if err := r.galgame.WithContext(ctx).Raw(
		`SELECT id, galgame_id, coalesce(name, '') AS name, coalesce(link, '') AS link
		 FROM galgame_link WHERE coalesce(source, '') <> 'vndb' ORDER BY id`).Scan(&links).Error; err != nil {
		return st, fmt.Errorf("read galgame_link: %w", err)
	}
	st.Source = len(links)

	anchors, err := r.anchorMap(ctx)
	if err != nil {
		return st, err
	}

	now := time.Now().UTC()
	rows := make([][]any, 0, len(links))
	parked := make([]parkedLink, 0)
	seen := make(map[[3]any]struct{}, len(links))
	buckets := map[string]int{}
	unparsed := 0
	for _, l := range links {
		workID, ok := anchors[l.GalgameID]
		if !ok {
			parked = append(parked, parkedLink{LinkID: l.ID, GalgameID: l.GalgameID, Name: l.Name, Link: l.Link})
			continue
		}
		key, extID, ok := classifyLink(l.Link)
		if !ok {
			unparsed++
			parked = append(parked, parkedLink{LinkID: l.ID, GalgameID: l.GalgameID, Name: l.Name, Link: l.Link})
			continue
		}
		buckets[key]++
		dedup := [3]any{workID, sourceIDs[key], extID}
		if _, dup := seen[dedup]; dup {
			continue
		}
		seen[dedup] = struct{}{}
		rows = append(rows, []any{
			model.EntityTypeWork, workID, sourceIDs[key], extID,
			model.LinkKindRelated, matchedByLink, now,
		})
	}
	st.Anchored = len(links) - len(parked)
	st.Parked = len(parked)
	st.Planned = len(rows)
	st.Note = fmt.Sprintf("buckets %v; in-batch duplicates collapsed=%d; non-URL rows=%d",
		buckets, st.Anchored-len(rows), unparsed)

	if err := r.park("c-links", parked); err != nil {
		return st, err
	}
	if !r.opts.Apply {
		return st, nil
	}

	// entity_id IS the work id for entity_type=work rows, so RETURNING it gives
	// exactly the works that gained a ref — the set TouchWorks must see, since
	// a work's external refs are part of its public face.
	err = r.catalog.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		landed, err := insertReturning(tx, "catalog_external_ref",
			[]string{"entity_type", "entity_id", "source_id", "external_id", "link_kind", "matched_by", "created_at"},
			"entity_id", rows)
		if err != nil {
			return err
		}
		st.Written = len(landed)
		if len(landed) > 0 {
			if err := repository.TouchWorks(ctx, tx, landed); err != nil {
				return fmt.Errorf("touch works: %w", err)
			}
			st.Touched = distinctCount(landed)
		}
		return nil
	})
	return st, err
}
