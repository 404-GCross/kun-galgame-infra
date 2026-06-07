package dto

import "api/internal/platform/galgame/model"

// ListRevisionRequest represents a revision list query
type ListRevisionRequest struct {
	Page         int  `query:"page" validate:"min=1"`
	Limit        int  `query:"limit" validate:"min=1,max=50"`
	IncludeMinor bool `query:"include_minor"`
}

// RecentRevisionsRequest is the query of GET /galgame/revisions/recent — the
// since-id edit-activity feed consumed by downstream crons (kungal/moyu).
type RecentRevisionsRequest struct {
	SinceID int64 `query:"since_id" validate:"omitempty,min=0"`
	Limit   int   `query:"limit" validate:"omitempty,min=1,max=5000"`
}

// RevisionFeedItem is one "edit landed" event for downstream activity
// timelines. Minimal by design — who (UserID = the editor), which (GalgameID),
// when (Created). ID is galgame_revision.id, used as the feed cursor.
type RevisionFeedItem struct {
	ID        int64  `json:"id"`
	GalgameID int    `json:"galgame_id"`
	UserID    int    `json:"user_id"`
	Action    string `json:"action"`
	Created   string `json:"created"`
}

// RevisionFeedResponse wraps the feed with has_more for cursor paging
// (mirrors MessageFeedResponse).
type RevisionFeedResponse struct {
	Items   []RevisionFeedItem `json:"items"`
	HasMore bool               `json:"has_more"`
}

// RevertRequest represents a revert request
type RevertRequest struct {
	Revision int `json:"revision" validate:"required,min=1"`
}

// SubmitPRRequest represents a PR submission
type SubmitPRRequest struct {
	VNDBID           *string              `json:"vndb_id"`
	NameEnUS         *string              `json:"name_en_us"`
	NameJaJP         *string              `json:"name_ja_jp"`
	NameZhCN         *string              `json:"name_zh_cn"`
	NameZhTW         *string              `json:"name_zh_tw"`
	Banner           *string              `json:"banner"`
	// PromoteCoverHash — see CreateGalgameRequest.PromoteCoverHash.
	// PR submit handler sets this from a multipart-uploaded banner file.
	PromoteCoverHash string               `json:"-"`
	IntroEnUS        *string              `json:"intro_en_us"`
	IntroJaJP        *string              `json:"intro_ja_jp"`
	IntroZhCN        *string              `json:"intro_zh_cn"`
	IntroZhTW        *string              `json:"intro_zh_tw"`
	ContentLimit     *string              `json:"content_limit"`
	OriginalLanguage *string              `json:"original_language"`
	AgeLimit         *string              `json:"age_limit"`
	// ReleaseDate / ReleaseDateTBA: pointer-presence like other scalars.
	// nil = field omitted = inherit base; non-nil overwrites in the PR.
	ReleaseDate      *string              `json:"release_date" validate:"omitempty,datetime=2006-01-02"`
	ReleaseDateTBA   *bool                `json:"release_date_tba"`
	SeriesID         *int                 `json:"series_id"`
	Aliases          []string             `json:"aliases"`
	TagIDs           []int                `json:"tag_ids"`
	OfficialIDs     []int                `json:"official_ids"`
	EngineIDs        []int                `json:"engine_ids"`
	Links            []model.SnapshotLink `json:"links"`
	Covers           []model.SnapshotCover      `json:"covers"`
	Screenshots      []model.SnapshotScreenshot `json:"screenshots"`
	// PR description: the wiki UI presents two inputs ("PR 标题" + "变更说明")
	// and reads pr.title / pr.message. The legacy single `note` field had no
	// binding target so the user's text was silently dropped.
	Title            string               `json:"title"`
	Message          string               `json:"message"`
}

// ApplyToSnapshot applies PR changes to a base snapshot, returning the proposed snapshot
func (r *SubmitPRRequest) ApplyToSnapshot(base *model.Snapshot) *model.Snapshot {
	s := *base // copy
	if r.VNDBID != nil {
		s.VNDBID = *r.VNDBID
	}
	if r.NameEnUS != nil {
		s.NameEnUS = *r.NameEnUS
	}
	if r.NameJaJP != nil {
		s.NameJaJP = *r.NameJaJP
	}
	if r.NameZhCN != nil {
		s.NameZhCN = *r.NameZhCN
	}
	if r.NameZhTW != nil {
		s.NameZhTW = *r.NameZhTW
	}
	if r.Banner != nil {
		s.Banner = *r.Banner
	}
	if r.IntroEnUS != nil {
		s.IntroEnUS = *r.IntroEnUS
	}
	if r.IntroJaJP != nil {
		s.IntroJaJP = *r.IntroJaJP
	}
	if r.IntroZhCN != nil {
		s.IntroZhCN = *r.IntroZhCN
	}
	if r.IntroZhTW != nil {
		s.IntroZhTW = *r.IntroZhTW
	}
	if r.ContentLimit != nil {
		s.ContentLimit = *r.ContentLimit
	}
	if r.OriginalLanguage != nil {
		s.OriginalLanguage = *r.OriginalLanguage
	}
	if r.AgeLimit != nil {
		s.AgeLimit = *r.AgeLimit
	}
	if r.ReleaseDate != nil {
		// "" clears the date; non-empty must be valid YYYY-MM-DD (validated upstream).
		if *r.ReleaseDate == "" {
			s.ReleaseDate = nil
		} else {
			v := *r.ReleaseDate
			s.ReleaseDate = &v
		}
	}
	if r.ReleaseDateTBA != nil {
		s.ReleaseDateTBA = *r.ReleaseDateTBA
	}
	if r.SeriesID != nil {
		s.SeriesID = r.SeriesID
	}
	if r.Aliases != nil {
		s.Aliases = r.Aliases
	}
	if r.TagIDs != nil {
		s.TagIDs = r.TagIDs
	}
	if r.OfficialIDs != nil {
		s.OfficialIDs = r.OfficialIDs
	}
	if r.EngineIDs != nil {
		s.EngineIDs = r.EngineIDs
	}
	if r.Links != nil {
		s.Links = r.Links
	}
	if r.Covers != nil {
		s.Covers = r.Covers
	}
	if r.Screenshots != nil {
		s.Screenshots = r.Screenshots
	}
	// Multipart-uploaded banner from the PR submit handler promotes to
	// pinned cover (sort_order=0). Inlined so dto stays decoupled from
	// service helpers; mirrors promoteCoverHashInPlace's contract.
	if r.PromoteCoverHash != "" {
		s.Covers = promoteSnapshotCoverHash(s.Covers, r.PromoteCoverHash)
	}
	return &s
}

// promoteSnapshotCoverHash is dto-layer twin of the service's
// promoteCoverHashInPlace. Kept here so SubmitPRRequest.ApplyToSnapshot
// (which lives in dto/) doesn't reach into service/. Behaviour is
// identical: ensure exactly one row has sort_order=0 = hash.
func promoteSnapshotCoverHash(covers []model.SnapshotCover, hash string) []model.SnapshotCover {
	if hash == "" {
		return covers
	}
	promoteIdx := -1
	for i, c := range covers {
		if c.ImageHash == hash {
			promoteIdx = i
			break
		}
	}
	for i := range covers {
		if covers[i].SortOrder == 0 && (promoteIdx < 0 || i != promoteIdx) {
			covers[i].SortOrder = 1
		}
	}
	if promoteIdx >= 0 {
		covers[promoteIdx].SortOrder = 0
		return covers
	}
	return append([]model.SnapshotCover{{ImageHash: hash, SortOrder: 0}}, covers...)
}

// ListPRRequest represents a PR list query
type ListPRRequest struct {
	Page  int `query:"page" validate:"min=1"`
	Limit int `query:"limit" validate:"min=1,max=50"`
}

// SnapshotEntityNames maps the IDs referenced inside one or more
// Snapshots (tag / official / engine / series) to their human-readable
// names.
//
// Returned alongside `changed_keys` on GET /galgame/:gid/revisions/:rev/diff
// and GET /galgame/:gid/prs/:id so the frontend can render
// "tag added: 校园, 治愈" instead of "tag added: 42, 87" without
// N+1 follow-up requests. Missing IDs (entity deleted since the
// revision was taken) are omitted; callers should fall back to
// "已删除 #ID" rendering.
//
// Performance: union of all IDs across old + new snapshots, one
// indexed `WHERE id IN (...)` SELECT per entity type (4 total). On
// typical galgames (≤30 tags, ≤5 officials, ≤2 engines, ≤1 series)
// the added DB cost is ~5ms; response size grows ~5KB. Cheap given
// diff is requested per page-load of revision / PR detail views.
type SnapshotEntityNames struct {
	Tags      map[int]string `json:"tags"`
	Officials map[int]string `json:"officials"`
	Engines   map[int]string `json:"engines"`
	Series    map[int]string `json:"series"`
}
