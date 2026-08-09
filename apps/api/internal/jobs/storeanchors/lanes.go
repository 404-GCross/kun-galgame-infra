package storeanchors

// The lane table and its normalizers. Everything here is pure — no database,
// no context — so the id-shape rules are unit-testable on their own.

import (
	"regexp"
	"strings"
)

// Lane names accepted by --only, and the order the lanes run in.
const (
	LaneSteam    = "steam"
	LaneDmm      = "dmm"
	LaneDlsite   = "dlsite"
	LaneDlsiteEN = "dlsite-en"
)

// lane binds one VNDB extlink vocabulary key to the catalog source it anchors
// into, the provenance tag every row carries, and the rule that turns VNDB's
// stored value into this source's own external_id form.
//
// site and sourceKey are NOT the same axis: VNDB distinguishes 'dlsite' (the
// Japanese storefront's RJ/VJ ids) from 'dlsiteen' (DLsite English's RE ids),
// but both are DLsite product ids and share one catalog_source. The prefixes
// are disjoint, so the shared external_id space cannot collide.
type lane struct {
	name      string
	site      string // src_vndb.extlinks.site
	sourceKey string // catalog_source.key
	matchedBy string
	// normalize returns the external_id to write, or "" when the value is not
	// a product identifier at all. It never guesses.
	normalize func(string) string
}

var lanes = []lane{
	{
		name: LaneSteam, site: "steam", sourceKey: "steam",
		matchedBy: "rule:vndb-extlink-steam", normalize: normalizeSteam,
	},
	{
		name: LaneDmm, site: "dmm", sourceKey: "dmm",
		matchedBy: "rule:vndb-extlink-dmm", normalize: normalizeDmm,
	},
	{
		name: LaneDlsite, site: "dlsite", sourceKey: "dlsite",
		matchedBy: "rule:vndb-extlink-dlsite", normalize: normalizeDlsiteJP,
	},
	{
		name: LaneDlsiteEN, site: "dlsiteen", sourceKey: "dlsite",
		matchedBy: "rule:vndb-extlink-dlsite-en", normalize: normalizeDlsiteEN,
	},
}

// selectedLanes resolves --only into the ordered lane list.
func selectedLanes(only string) ([]lane, error) {
	if only == "" {
		return lanes, nil
	}
	for _, l := range lanes {
		if l.name == only {
			return []lane{l}, nil
		}
	}
	return nil, unknownLaneError(only)
}

var (
	reSteamAppID = regexp.MustCompile(`^[0-9]+$`)
	// DMM stores the link as a URL path, not a bare id. Two page shapes carry
	// a product id, in this precedence order:
	//   www.dmm.co.jp/…/detail/=/[shop=…/]cid=<cid>/   (mono / dc / digital)
	//   dlsoft.dmm.co.jp[/en]/detail/<cid>/            (the DL-soft storefront)
	// Everything else — /original/, /feature/, /serial/, /list/ — is a landing
	// or listing page and identifies no product.
	reDmmCID    = regexp.MustCompile(`(?:^|[/=&])cid=([0-9A-Za-z_]+)`)
	reDmmDetail = regexp.MustCompile(`/detail/([0-9A-Za-z_]+)`)
	reDlsiteJP  = regexp.MustCompile(`^(?:RJ|VJ)[0-9]+$`)
	reDlsiteEN  = regexp.MustCompile(`^RE[0-9]+$`)
)

// normalizeSteam keeps VNDB's bare numeric appid, which is the same form the
// existing work-level steam refs use.
func normalizeSteam(v string) string {
	v = strings.TrimSpace(v)
	if reSteamAppID.MatchString(v) {
		return v
	}
	return ""
}

// normalizeDmm extracts the DMM content id. The extracted form was validated
// against the work-level EG cids already in the catalog: 8,698 of 13,904 EG
// cids reappear byte-identical here, so both writers share one id space.
func normalizeDmm(v string) string {
	v = strings.TrimSpace(v)
	if m := reDmmCID.FindStringSubmatch(v); m != nil {
		return m[1]
	}
	if m := reDmmDetail.FindStringSubmatch(v); m != nil {
		return m[1]
	}
	return ""
}

func normalizeDlsiteJP(v string) string {
	v = strings.TrimSpace(v)
	if reDlsiteJP.MatchString(v) {
		return v
	}
	return ""
}

func normalizeDlsiteEN(v string) string {
	v = strings.TrimSpace(v)
	if reDlsiteEN.MatchString(v) {
		return v
	}
	return ""
}
