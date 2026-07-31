package editspec

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	catmodel "api/internal/platform/catalog/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// catalog.work.links — the work's external links, as a bare array of URLs.
//
// The {name, link} PAIR IS DROPPED (03 定案 §6-2). The wiki modelled links as
// named download/official entries; the catalog models external refs — an
// identity registry keyed (source, external_id) with a link_kind grade. A
// recognized URL becomes a ref to the source it identifies, and everything else
// becomes a generic `web` ref carrying the full URL, so no link is lost, but
// the operator-chosen label is not preserved and cannot be: there is no column
// for it, and inventing one would recreate the free-form list this design
// retires.
//
// TWO GRADES, and the difference matters:
//
//   - an identity URL (VNDB / Bangumi) becomes a CANDIDATE (link_kind =
//     probable). Human-entered identity claims join the registrar's confirm
//     queue rather than becoming anchors on the spot — the QA track's rule, and
//     the reason a mistyped id cannot silently hijack another work's identity
//     (the vndb_id squatting class);
//   - every other URL becomes RELATED. A related ref never participates in
//     identity resolution, which is also why this Apply can never write an
//     exact ref — minting anchors belongs to the importer lanes.
//
// The lane: external_ref has no source-lane column (a curated VNDB candidate
// and the vndb importer's exact anchor are both source 2), so the full replace
// is scoped by matched_by = curated. Importer rows and confirmed anchors are
// invisible to it.

var (
	reVNDB    = regexp.MustCompile(`(?i)^https?://(?:www\.)?vndb\.org/(v\d+)(?:[/?#]|$)`)
	reBangumi = regexp.MustCompile(`(?i)^https?://(?:www\.)?(?:bgm\.tv|bangumi\.tv|chii\.in)/subject/(\d+)`)

	reLinkArchive     = regexp.MustCompile(`(?i)web\.archive\.org/web/[^/]+/(https?://.+)$`)
	reLinkTwitter     = regexp.MustCompile(`(?i)^https?://(?:www\.|mobile\.)?(?:twitter\.com|x\.com)/@?([A-Za-z0-9_]{1,30})(?:[/?#]|$)`)
	reLinkCien        = regexp.MustCompile(`(?i)^https?://ci-en\.dlsite\.com/creator/(\d+)`)
	reLinkSteam       = regexp.MustCompile(`(?i)^https?://store\.steampowered\.com/app/(\d+)`)
	reLinkPixivUsers  = regexp.MustCompile(`(?i)^https?://(?:www\.)?pixiv\.net/(?:en/)?users/(\d+)`)
	reLinkPixivMember = regexp.MustCompile(`(?i)^https?://(?:www\.)?pixiv\.net/member\.php\?id=(\d+)`)
	reLinkDlsiteHost  = regexp.MustCompile(`(?i)^https?://(?:www\.)?dlsite\.com/`)
	reLinkDlsiteWork  = regexp.MustCompile(`(?i)\b((?:RJ|VJ|BJ)\d{5,10})\b`)
)

// linkURLTemplates rebuilds a ref's canonical URL from its external id — the
// round-trip half of classifyWorkLink, and the reason the classifier only ever
// resolves a URL onto a source it can render BACK.
//
// A full-replace list field must round-trip: LoadSnapshot has to return the
// same value a patch would submit, or a revert replays a value that silently
// dropped rows. catalog_source.url_template is empty for every source today,
// so the templates live here, next to the patterns they invert, and a source
// with no canonical shape simply never gets classified.
//
// DMM is the concrete case: one cid is reachable through several storefront
// paths (dc/doujin, digital/pcgame, the dlsoft /detail/ form), so there is no
// single URL to rebuild and a DMM link is kept verbatim as a `web` ref instead
// of being turned into a ref this face could not show the user again.
var linkURLTemplates = map[string]string{
	"vndb":    "https://vndb.org/%s",
	"bangumi": "https://bgm.tv/subject/%s",
	"twitter": "https://x.com/%s",
	"cien":    "https://ci-en.dlsite.com/creator/%s",
	"steam":   "https://store.steampowered.com/app/%s",
	"pixiv":   "https://www.pixiv.net/users/%s",
	"dlsite":  "https://www.dlsite.com/home/work/=/product_id/%s.html",
}

// linkReservedHandles are x.com paths that are site features, not handles.
var linkReservedHandles = map[string]bool{
	"i": true, "home": true, "intent": true, "share": true,
	"search": true, "hashtag": true, "explore": true, "messages": true,
}

// classifiedLink is one URL resolved onto the registry's coordinates.
type classifiedLink struct {
	SourceKey  string
	ExternalID string
	LinkKind   int16
}

// classifyWorkLink maps a URL onto (source key, external id, grade). ok=false
// only for input that is not an http(s) URL at all.
//
// The pattern table intentionally MIRRORS the retiring wikirescue step c's
// (internal/jobs/wikirescue/link.go): that step is a one-way rescue of the wiki
// body and dies with the family at N5, while this is the permanent home of the
// same knowledge. They are not shared because the retiring job must not be
// touched; when it goes, this copy is simply the only one left.
func classifyWorkLink(raw string) (classifiedLink, bool) {
	u := strings.TrimSpace(raw)
	if m := reLinkArchive.FindStringSubmatch(u); m != nil {
		u = strings.TrimSpace(m[1])
	}
	lower := strings.ToLower(u)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return classifiedLink{}, false
	}
	// Identity sources first — these are candidates, not related links.
	if m := reVNDB.FindStringSubmatch(u); m != nil {
		return classifiedLink{"vndb", strings.ToLower(m[1]), catmodel.LinkKindProbable}, true
	}
	if m := reBangumi.FindStringSubmatch(u); m != nil {
		return classifiedLink{"bangumi", m[1], catmodel.LinkKindProbable}, true
	}
	related := func(source, id string) (classifiedLink, bool) {
		return classifiedLink{source, id, catmodel.LinkKindRelated}, true
	}
	if m := reLinkTwitter.FindStringSubmatch(u); m != nil {
		if handle := strings.ToLower(m[1]); !linkReservedHandles[handle] {
			return related("twitter", handle)
		}
	}
	if m := reLinkCien.FindStringSubmatch(u); m != nil {
		return related("cien", m[1])
	}
	if m := reLinkSteam.FindStringSubmatch(u); m != nil {
		return related("steam", m[1])
	}
	if m := reLinkPixivUsers.FindStringSubmatch(u); m != nil {
		return related("pixiv", m[1])
	}
	if m := reLinkPixivMember.FindStringSubmatch(u); m != nil {
		return related("pixiv", m[1])
	}
	if reLinkDlsiteHost.MatchString(u) {
		if m := reLinkDlsiteWork.FindStringSubmatch(u); m != nil {
			return related("dlsite", strings.ToUpper(m[1]))
		}
		return related("web", u) // circle / announce page — degrade, never invent an id
	}
	return related("web", u)
}

// canonicalLinkURL renders a classified link back into its canonical URL. A
// `web` ref stores the whole URL as its external id, so it renders as itself.
func canonicalLinkURL(cl classifiedLink) string {
	tpl, ok := linkURLTemplates[cl.SourceKey]
	if !ok {
		return cl.ExternalID
	}
	return fmt.Sprintf(tpl, cl.ExternalID)
}

// parseLinks validates the payload and CANONICALIZES every URL through the
// classifier: twitter.com/x.com, a mobile host, a web.archive.org wrapper and a
// trailing query all collapse onto the one form this field stores and reads
// back. That is what makes the value round-trip exactly — a submitted patch and
// the snapshot that follows it are the same JSON, so a no-op edit is detected
// as one and a revert replays what it recorded.
func parseLinks(v any) ([]string, error) {
	arr, err := asArray(v, "URL strings")
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(arr))
	seen := make(map[string]struct{}, len(arr))
	for i, el := range arr {
		s, ok := el.(string)
		if !ok {
			return nil, fmt.Errorf("element %d: must be a URL string", i)
		}
		s = strings.TrimSpace(s)
		if len([]rune(s)) > maxURLRunes {
			return nil, fmt.Errorf("element %d: URL must be at most %d characters", i, maxURLRunes)
		}
		cl, ok := classifyWorkLink(s)
		if !ok {
			return nil, fmt.Errorf("element %d: must be an http:// or https:// URL", i)
		}
		canonical := canonicalLinkURL(cl)
		if _, dup := seen[canonical]; dup {
			return nil, fmt.Errorf("element %d: duplicate URL", i)
		}
		seen[canonical] = struct{}{}
		out = append(out, canonical)
	}
	return out, nil
}

func validateLinks(v any) error { _, err := parseLinks(v); return err }

// applyLinks is catalog.work's links Apply; applyLinksFor is the same machine
// for any entity family that registers a links field (catalog.label does).
func applyLinks(ctx context.Context, tx *gorm.DB, entityID int64, value any) error {
	if err := assertWorkExists(ctx, tx, entityID); err != nil {
		return err
	}
	return applyLinksFor(ctx, tx, catmodel.EntityTypeWork, entityID, value)
}

func applyLinksFor(ctx context.Context, tx *gorm.DB, entityType int16, entityID int64, value any) error {
	urls, err := parseLinks(value)
	if err != nil {
		return fmt.Errorf("editspec: links: %w", err)
	}
	sources, err := sourceIDsByKey(ctx, tx)
	if err != nil {
		return err
	}
	if err := tx.WithContext(ctx).
		Where("entity_type = ? AND entity_id = ? AND matched_by = ?",
			entityType, entityID, curatedMatchedBy).
		Delete(&catmodel.CatalogExternalRef{}).Error; err != nil {
		return err
	}
	if len(urls) == 0 {
		return nil
	}
	rows := make([]catmodel.CatalogExternalRef, 0, len(urls))
	for _, u := range urls {
		cl, _ := classifyWorkLink(u)
		srcID, ok := sources[cl.SourceKey]
		if !ok {
			return fmt.Errorf("editspec: links: source %q is not registered", cl.SourceKey)
		}
		rows = append(rows, catmodel.CatalogExternalRef{
			EntityType: entityType, EntityID: entityID,
			SourceID: srcID, ExternalID: cl.ExternalID,
			LinkKind: cl.LinkKind, MatchedBy: curatedMatchedBy,
		})
	}
	// DO NOTHING on the ref primary key: the coordinate may already be held by
	// a confirmed anchor or another lane's row, and a curated link must never
	// downgrade one (an exact anchor rewritten as probable is an identity
	// regression, not an edit).
	return tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error
}

// loadLinks reads the curated lane back as canonical URLs. Every row this face
// writes is renderable by construction (the classifier only resolves onto
// sources canonicalLinkURL can invert), so nothing is dropped here — the guard
// exists for rows a future source key would leave unrenderable.
func loadLinks(ctx context.Context, db *gorm.DB, workID int64) ([]any, error) {
	return loadLinksFor(ctx, db, catmodel.EntityTypeWork, workID)
}

func loadLinksFor(ctx context.Context, db *gorm.DB, entityType int16, entityID int64) ([]any, error) {
	var rows []struct {
		SourceKey  string `gorm:"column:key"`
		ExternalID string `gorm:"column:external_id"`
	}
	if err := db.WithContext(ctx).Raw(`
		SELECT s.key, r.external_id
		FROM catalog_external_ref r
		JOIN catalog_source s ON s.id = r.source_id
		WHERE r.entity_type = ? AND r.entity_id = ? AND r.matched_by = ?
		ORDER BY r.source_id, r.external_id`,
		entityType, entityID, curatedMatchedBy).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]any, 0, len(rows))
	for _, r := range rows {
		if r.SourceKey == "web" {
			out = append(out, r.ExternalID)
			continue
		}
		if _, ok := linkURLTemplates[r.SourceKey]; ok {
			out = append(out, canonicalLinkURL(classifiedLink{SourceKey: r.SourceKey, ExternalID: r.ExternalID}))
		}
	}
	return out, nil
}

// sourceIDsByKey loads the source registry as key→id. It is a 16-row table, so
// one read per Apply is cheaper than any caching scheme's invalidation bug.
func sourceIDsByKey(ctx context.Context, tx *gorm.DB) (map[string]int16, error) {
	var rows []catmodel.CatalogSource
	if err := tx.WithContext(ctx).Select("id", "key").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]int16, len(rows))
	for _, r := range rows {
		out[r.Key] = r.ID
	}
	return out, nil
}
