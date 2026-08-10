package storeanchors

import (
	"regexp"
	"strings"
)

const (
	LaneSteam    = "steam"
	LaneDmm      = "dmm"
	LaneDlsite   = "dlsite"
	LaneDlsiteEN = "dlsite-en"
)

type lane struct {
	name      string
	site      string
	sourceKey string
	matchedBy string
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
	reDmmCID     = regexp.MustCompile(`(?:^|[/=&])cid=([0-9A-Za-z_]+)`)
	reDmmDetail  = regexp.MustCompile(`/detail/([0-9A-Za-z_]+)`)
	reDlsiteJP   = regexp.MustCompile(`^(?:RJ|VJ)[0-9]+$`)
	reDlsiteEN   = regexp.MustCompile(`^RE[0-9]+$`)
)

func normalizeSteam(v string) string {
	v = strings.TrimSpace(v)
	if reSteamAppID.MatchString(v) {
		return v
	}
	return ""
}

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
