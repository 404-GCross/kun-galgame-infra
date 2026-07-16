package service

import (
	"context"
	stderrors "errors"
	"sort"
	"strings"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// sourceKeyDlsite is the catalog_source registry key for DLsite anchors (the
// product side keys its doujin rows on the DLsite workno).
const sourceKeyDlsite = "dlsite"

// ReadService backs the S2S read face (step 18, D-01): anchor read-through and
// credits-by-work. Pure reads over the catalog DB; transport-agnostic (the
// handler maps these to DTOs).
type ReadService struct{ db *gorm.DB }

func NewReadService(db *gorm.DB) *ReadService { return &ReadService{db: db} }

// ErrWorkNotFound is returned when an anchor resolves to no work.
var ErrWorkNotFound = stderrors.New("catalog: no work for anchor")

// WorkDetail is the anchor read-through result.
type WorkDetail struct {
	Work     model.CatalogWork
	Titles   []model.CatalogWorkTitle
	Releases []ReleaseDetail
	Labels   []LabelAttribution
	// Refs is the flat exact-only external-ref projection (work- and
	// release-level in one list) — the cross-source identity chain.
	Refs []RefDetail
	// Characters is the merged roster (step 46): roster edges UNIONed with
	// voice-credited characters. Loaded by loadWorkDetail so the by-anchor /
	// by-id read-through bundles it.
	Characters []WorkCharacterRow
}

// WorkCharacterRow is one character on a work's roster, merging the roster edge
// (kind) with the voice credits that name it (va).
type WorkCharacterRow struct {
	CharacterID int64
	DisplayName string
	Latin       *string
	Gender      *int16
	Kind        int16
	ImageHash   *string
	Va          []WorkCharacterVARow
}

// WorkCharacterVARow is one credited name that voiced a character on a work.
type WorkCharacterVARow struct {
	CreditNameID int64
	Name         string
}

// RefDetail is one exact external anchor of a work, with its level. ReleaseID
// is set (non-zero) only for release-level refs.
type RefDetail struct {
	Source     string
	ExternalID string
	EntityType int16 // model.EntityTypeWork or model.EntityTypeRelease
	ReleaseID  int64
}

// ReleaseDetail is a release plus its anchors.
type ReleaseDetail struct {
	Release model.CatalogRelease
	Anchors []AnchorDetail
}

// AnchorDetail is one external anchor with the source key resolved. MatchedBy
// is the rule string that asserted it — the provenance the internal browser
// surfaces verbatim.
type AnchorDetail struct {
	Source     string
	ExternalID string
	LinkKind   int16
	MatchedBy  string
}

// LabelAttribution is one work↔label edge with the label denormalized.
type LabelAttribution struct {
	LabelID     int64
	DisplayName string
	LabelKind   int16
	Kind        int16 // attribution edge kind
}

// WorkByAnchor resolves a work via any of its external anchors (work- or
// release-level) and loads its titles, releases (with anchors) and label
// attributions. A release anchor traces back to its work; a lower link_kind
// (exact before probable) and a work-level anchor win ties.
func (s *ReadService) WorkByAnchor(ctx context.Context, sourceKey, externalID string) (*WorkDetail, error) {
	db := s.db.WithContext(ctx)

	var srcID int16
	if err := db.Raw(`SELECT id FROM catalog_source WHERE key = ?`, sourceKey).Scan(&srcID).Error; err != nil {
		return nil, err
	}
	if srcID == 0 {
		return nil, ErrWorkNotFound // unknown source key == no such anchor
	}

	var ref struct {
		EntityType int16
		EntityID   int64
	}
	if err := db.Raw(`SELECT entity_type, entity_id FROM catalog_external_ref
		WHERE source_id = ? AND external_id = ? AND entity_type IN (?, ?)
		ORDER BY link_kind ASC, entity_type ASC LIMIT 1`,
		srcID, externalID, model.EntityTypeWork, model.EntityTypeRelease).Scan(&ref).Error; err != nil {
		return nil, err
	}
	if ref.EntityID == 0 {
		return nil, ErrWorkNotFound
	}

	workID := ref.EntityID
	if ref.EntityType == model.EntityTypeRelease {
		if err := db.Raw(`SELECT work_id FROM catalog_release WHERE id = ?`, ref.EntityID).Scan(&workID).Error; err != nil {
			return nil, err
		}
	}
	return s.loadWorkDetail(ctx, workID)
}

// WorkByID loads the same bundle as WorkByAnchor, addressed by catalog work id
// (the internal browser's drill-down entry). 404 semantics identical.
func (s *ReadService) WorkByID(ctx context.Context, workID int64) (*WorkDetail, error) {
	return s.loadWorkDetail(ctx, workID)
}

// loadWorkDetail assembles a work's titles, releases (with anchors) and label
// attributions. Returns ErrWorkNotFound if the work does not exist.
func (s *ReadService) loadWorkDetail(ctx context.Context, workID int64) (*WorkDetail, error) {
	db := s.db.WithContext(ctx)
	var work model.CatalogWork
	if err := db.First(&work, workID).Error; err != nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrWorkNotFound
		}
		return nil, err
	}

	detail := &WorkDetail{Work: work}
	if err := db.Where("work_id = ?", workID).Order("kind, lang").Find(&detail.Titles).Error; err != nil {
		return nil, err
	}

	var releases []model.CatalogRelease
	if err := db.Where("work_id = ?", workID).Order("id").Find(&releases).Error; err != nil {
		return nil, err
	}
	anchorsByRelease := map[int64][]AnchorDetail{}
	if len(releases) > 0 {
		relIDs := make([]int64, len(releases))
		for i, r := range releases {
			relIDs[i] = r.ID
		}
		var arows []struct {
			EntityID   int64
			Source     string
			ExternalID string
			LinkKind   int16
			MatchedBy  string
		}
		if err := db.Raw(`SELECT r.entity_id, s.key AS source, r.external_id, r.link_kind, r.matched_by
			FROM catalog_external_ref r JOIN catalog_source s ON s.id = r.source_id
			WHERE r.entity_type = ? AND r.entity_id IN ?
			ORDER BY r.link_kind, s.key`, model.EntityTypeRelease, relIDs).Scan(&arows).Error; err != nil {
			return nil, err
		}
		for _, a := range arows {
			anchorsByRelease[a.EntityID] = append(anchorsByRelease[a.EntityID],
				AnchorDetail{Source: a.Source, ExternalID: a.ExternalID, LinkKind: a.LinkKind, MatchedBy: a.MatchedBy})
		}
	}
	for _, r := range releases {
		detail.Releases = append(detail.Releases, ReleaseDetail{Release: r, Anchors: anchorsByRelease[r.ID]})
	}

	if err := db.Raw(`SELECT wl.label_id, l.display_name, l.kind AS label_kind, wl.kind AS kind
		FROM catalog_work_label wl JOIN catalog_label l ON l.id = wl.label_id
		WHERE wl.work_id = ? ORDER BY wl.kind, l.display_name`, workID).Scan(&detail.Labels).Error; err != nil {
		return nil, err
	}

	// Refs block: EXACT-only cross-source identity, work-level + release-level
	// flattened into one list. Work-level refs come from a dedicated query;
	// release-level refs are the exact subset of the anchors already loaded
	// above (no second scan of the ref table).
	var workRefs []struct {
		Source     string
		ExternalID string
	}
	if err := db.Raw(`SELECT s.key AS source, r.external_id
		FROM catalog_external_ref r JOIN catalog_source s ON s.id = r.source_id
		WHERE r.entity_type = ? AND r.entity_id = ? AND r.link_kind = ?
		ORDER BY s.key`, model.EntityTypeWork, workID, model.LinkKindExact).Scan(&workRefs).Error; err != nil {
		return nil, err
	}
	for _, wr := range workRefs {
		detail.Refs = append(detail.Refs, RefDetail{Source: wr.Source, ExternalID: wr.ExternalID, EntityType: model.EntityTypeWork})
	}
	for _, rd := range detail.Releases {
		for _, a := range rd.Anchors {
			if a.LinkKind == model.LinkKindExact {
				detail.Refs = append(detail.Refs, RefDetail{
					Source: a.Source, ExternalID: a.ExternalID,
					EntityType: model.EntityTypeRelease, ReleaseID: rd.Release.ID,
				})
			}
		}
	}

	chars, err := s.loadWorkCharacters(ctx, workID)
	if err != nil {
		return nil, err
	}
	detail.Characters = chars
	return detail, nil
}

// loadWorkCharacters assembles a work's roster (step 46) as the UNION of the
// roster edge table (catalog_work_character, which carries the appearance kind)
// and the voice credits (catalog_credit.character_id, which carry the voicing
// name). A character with a roster edge takes its kind from the edge; a
// credit-only character (voiced but with no edge — e.g. VNDB-only VA credits)
// still surfaces with kind=0 (unknown appearance strength). The two tables are
// deliberately independent (step 45): this read is the one place they merge.
// Only the credit NAME is exposed per va entry — no person expansion (the
// link-visibility doctrine gates person aggregation, never the nominal).
func (s *ReadService) loadWorkCharacters(ctx context.Context, workID int64) ([]WorkCharacterRow, error) {
	db := s.db.WithContext(ctx)

	// Roster edges: the appearance fact + its kind.
	var edges []struct {
		CharacterID int64   `gorm:"column:character_id"`
		DisplayName string  `gorm:"column:display_name"`
		Latin       *string `gorm:"column:latin"`
		Gender      *int16  `gorm:"column:gender"`
		ImageHash   *string `gorm:"column:image_hash"`
		Kind        int16   `gorm:"column:kind"`
	}
	if err := db.Raw(`SELECT wc.character_id, ch.display_name, ch.latin, ch.gender, ch.image_hash, wc.kind
		FROM catalog_work_character wc JOIN catalog_character ch ON ch.id = wc.character_id
		WHERE wc.work_id = ? AND ch.deleted_at IS NULL`, workID).Scan(&edges).Error; err != nil {
		return nil, err
	}

	// Voice credits: who voiced whom. DISTINCT collapses per-release duplicate
	// credit rows to one (name, character) pair.
	var creds []struct {
		CharacterID  int64   `gorm:"column:character_id"`
		DisplayName  string  `gorm:"column:display_name"`
		Latin        *string `gorm:"column:latin"`
		Gender       *int16  `gorm:"column:gender"`
		ImageHash    *string `gorm:"column:image_hash"`
		CreditNameID int64   `gorm:"column:credit_name_id"`
		Name         string  `gorm:"column:name"`
	}
	if err := db.Raw(`SELECT DISTINCT c.character_id, ch.display_name, ch.latin, ch.gender, ch.image_hash,
		cn.id AS credit_name_id, cn.name
		FROM catalog_credit c
		JOIN catalog_character ch ON ch.id = c.character_id
		JOIN catalog_credit_name cn ON cn.id = c.credit_name_id
		WHERE c.work_id = ? AND c.character_id IS NOT NULL AND ch.deleted_at IS NULL`, workID).Scan(&creds).Error; err != nil {
		return nil, err
	}

	byID := make(map[int64]*WorkCharacterRow, len(edges)+len(creds))
	for _, e := range edges {
		byID[e.CharacterID] = &WorkCharacterRow{
			CharacterID: e.CharacterID, DisplayName: e.DisplayName, Latin: e.Latin,
			Gender: e.Gender, Kind: e.Kind, ImageHash: e.ImageHash,
		}
	}
	for _, c := range creds {
		row, ok := byID[c.CharacterID]
		if !ok {
			row = &WorkCharacterRow{
				CharacterID: c.CharacterID, DisplayName: c.DisplayName, Latin: c.Latin,
				Gender: c.Gender, Kind: model.WorkCharacterKindUnknown, ImageHash: c.ImageHash,
			}
			byID[c.CharacterID] = row
		}
		row.Va = append(row.Va, WorkCharacterVARow{CreditNameID: c.CreditNameID, Name: c.Name})
	}

	out := make([]WorkCharacterRow, 0, len(byID))
	for _, r := range byID {
		va := r.Va
		sort.Slice(va, func(i, j int) bool {
			if va[i].Name != va[j].Name {
				return va[i].Name < va[j].Name
			}
			return va[i].CreditNameID < va[j].CreditNameID
		})
		out = append(out, *r)
	}
	// Sort main-first (task §1: "kind 升序(main 先)"): the meaningful kinds
	// 1/2/3 lead in order, then unknown(0) last — a credit-only / EG-unclassified
	// character sorts after the classified cast rather than jumping to the top of
	// the roster. Ties broken by display name, then character id (determinism).
	sort.Slice(out, func(i, j int) bool {
		ri, rj := kindRank(out[i].Kind), kindRank(out[j].Kind)
		if ri != rj {
			return ri < rj
		}
		if out[i].DisplayName != out[j].DisplayName {
			return out[i].DisplayName < out[j].DisplayName
		}
		return out[i].CharacterID < out[j].CharacterID
	})
	return out, nil
}

// kindRank orders roster kinds for display: main(1) → secondary(2) → appears(3)
// → unknown(0). Unknown is a meaningful value (EG has no main/supporting split;
// credit-only characters have no edge) but belongs after the classified cast.
func kindRank(k int16) int16 {
	if k == model.WorkCharacterKindUnknown {
		return 99
	}
	return k
}

// LabelWork is one work attributed to a label (circle→works reverse lookup).
type LabelWork struct {
	WorkID        int64  `gorm:"column:work_id"`
	DisplayName   string `gorm:"column:display_name"`
	MediumID      int16  `gorm:"column:medium_id"`
	ContentRating int16  `gorm:"column:content_rating"`
	Status        int16  `gorm:"column:status"`
	Kind          int16  `gorm:"column:kind"` // attribution edge kind
}

// LabelHead is a label's own identity (returned alongside its works so the
// reverse-lookup page is self-sufficient on direct navigation).
type LabelHead struct {
	ID          int64  `gorm:"column:id"`
	DisplayName string `gorm:"column:display_name"`
	Kind        int16  `gorm:"column:kind"`
}

// LabelWorks returns a label's own identity plus the works attributed to it
// (via the attribution edge), offset-paginated. head is nil if the label does
// not exist; total is the full count for the label.
func (s *ReadService) LabelWorks(ctx context.Context, labelID int64, limit, offset int) (head *LabelHead, items []LabelWork, total int64, err error) {
	db := s.db.WithContext(ctx)
	var h LabelHead
	if err = db.Raw(`SELECT id, display_name, kind FROM catalog_label WHERE id = ?`, labelID).Scan(&h).Error; err != nil {
		return nil, nil, 0, err
	}
	if h.ID != 0 {
		head = &h
	}
	if err = db.Raw(`SELECT count(*) FROM catalog_work_label WHERE label_id = ?`, labelID).Scan(&total).Error; err != nil {
		return nil, nil, 0, err
	}
	err = db.Raw(`SELECT w.id AS work_id, w.display_name, w.medium_id, w.content_rating, w.status, wl.kind
		FROM catalog_work_label wl JOIN catalog_work w ON w.id = wl.work_id
		WHERE wl.label_id = ? ORDER BY w.id LIMIT ? OFFSET ?`, labelID, limit, offset).Scan(&items).Error
	return head, items, total, err
}

// WorkSearchHit is one title-search hit: work identity + claim state + its
// first DLsite anchor (empty when it has none).
type WorkSearchHit struct {
	WorkID        int64  `gorm:"column:work_id"`
	DisplayName   string `gorm:"column:display_name"`
	MediumID      int16  `gorm:"column:medium_id"`
	ContentRating int16  `gorm:"column:content_rating"`
	Status        int16  `gorm:"column:status"`
	Site          string `gorm:"column:site"` // "" = unclaimed (COALESCEd)
	DlsiteID      string `gorm:"-"`           // filled by a second query, not the title scan
}

// SearchWorks finds works by a case/width-insensitive title SUBSTRING match,
// optionally filtered to one medium, for the product-side upstream-first create
// picker (step 18). The match reuses catalog_work_title.title_norm — the STORED
// generated column lower(normalize(title, NFKC)) — so the fold is byte-identical
// to the importer's, and folds the query the same way in-query. mediumID <= 0
// means no medium filter. v1 has NO trigram index: this is a plain ILIKE over
// ~190k title rows, acceptable for a low-frequency staff picker; add a pg_trgm
// index on title_norm if the call volume grows (docs/proj/18). Each hit is
// annotated with its first DLsite anchor (work- or release-level, exact first).
func (s *ReadService) SearchWorks(ctx context.Context, q string, mediumID int16, limit int) ([]WorkSearchHit, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	db := s.db.WithContext(ctx)

	// EXISTS keeps one row per work even when several of its titles match. The
	// merged tombstones (status=2) never surface — their identity lives on a
	// survivor. The bind param is NFKC-folded in-query to match title_norm.
	var hits []WorkSearchHit
	if err := db.Raw(`
		SELECT w.id AS work_id, w.display_name, w.medium_id, w.content_rating,
		       w.status, COALESCE(w.site, '') AS site
		FROM catalog_work w
		WHERE w.deleted_at IS NULL
		  AND w.status <> ?
		  AND (? <= 0 OR w.medium_id = ?)
		  AND EXISTS (
		      SELECT 1 FROM catalog_work_title t
		      WHERE t.work_id = w.id
		        AND t.title_norm LIKE '%' || lower(normalize(?, NFKC)) || '%'
		  )
		ORDER BY w.id
		LIMIT ?`,
		model.WorkStatusMerged, mediumID, mediumID, q, limit).Scan(&hits).Error; err != nil {
		return nil, err
	}
	if len(hits) == 0 {
		return hits, nil
	}

	// Annotate each hit with its first DLsite anchor in one batched query over
	// the matched work ids (work-level refs, plus release-level refs traced back
	// to their work). Lowest link_kind first → exact wins the "first" slot.
	workIDs := make([]int64, len(hits))
	for i := range hits {
		workIDs[i] = hits[i].WorkID
	}
	var refs []struct {
		WorkID     int64  `gorm:"column:work_id"`
		ExternalID string `gorm:"column:external_id"`
	}
	if err := db.Raw(`
		SELECT x.work_id, x.external_id FROM (
			SELECT r.entity_id AS work_id, r.external_id, r.link_kind
			FROM catalog_external_ref r JOIN catalog_source s ON s.id = r.source_id
			WHERE s.key = ? AND r.entity_type = ? AND r.entity_id IN ?
			UNION ALL
			SELECT rel.work_id, r.external_id, r.link_kind
			FROM catalog_external_ref r JOIN catalog_source s ON s.id = r.source_id
			  JOIN catalog_release rel ON rel.id = r.entity_id
			WHERE s.key = ? AND r.entity_type = ? AND rel.work_id IN ?
		) x ORDER BY x.work_id, x.link_kind`,
		sourceKeyDlsite, model.EntityTypeWork, workIDs,
		sourceKeyDlsite, model.EntityTypeRelease, workIDs).Scan(&refs).Error; err != nil {
		return nil, err
	}
	firstDlsite := make(map[int64]string, len(refs))
	for _, r := range refs {
		if _, ok := firstDlsite[r.WorkID]; !ok {
			firstDlsite[r.WorkID] = r.ExternalID
		}
	}
	for i := range hits {
		hits[i].DlsiteID = firstDlsite[hits[i].WorkID]
	}
	return hits, nil
}

// WorkBriefRow is the lightweight work projection shared by the entity
// reverse-lookups (name→works, character→works): identity + claim state.
type WorkBriefRow struct {
	WorkID        int64  `gorm:"column:work_id"`
	DisplayName   string `gorm:"column:display_name"`
	MediumID      int16  `gorm:"column:medium_id"`
	ContentRating int16  `gorm:"column:content_rating"`
	Status        int16  `gorm:"column:status"`
	Site          string `gorm:"column:site"` // "" = unclaimed (COALESCEd)
}

// workBriefs loads the brief for a set of work ids, keyed by id. Raw SQL
// bypasses GORM's soft-delete scope, so deleted_at is filtered explicitly (a
// merged work is soft-deleted and its credits repointed to the survivor, so it
// never reaches here in practice — this is belt-and-suspenders).
func (s *ReadService) workBriefs(ctx context.Context, ids []int64) (map[int64]WorkBriefRow, error) {
	var rows []WorkBriefRow
	if err := s.db.WithContext(ctx).Raw(`
		SELECT id AS work_id, display_name, medium_id, content_rating, status, COALESCE(site, '') AS site
		FROM catalog_work WHERE id IN ? AND deleted_at IS NULL`, ids).Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[int64]WorkBriefRow, len(rows))
	for _, r := range rows {
		m[r.WorkID] = r
	}
	return m, nil
}

// --- name → works (step 19: what a credited name worked on) ---

// NameHeadRow is a credit name's own identity plus its (visibility-gated)
// person link. LinkVisibility is used internally to gate exposure and is not
// carried to the wire.
type NameHeadRow struct {
	ID             int64  `gorm:"column:id"`
	Name           string `gorm:"column:name"`
	Lang           string `gorm:"column:lang"`
	Latin          *string
	PersonID       *int64 `gorm:"column:person_id"`
	LinkVisibility int16  `gorm:"column:link_visibility"`
}

// SiblingNameRow is another credit name of the same person.
type SiblingNameRow struct {
	ID    int64  `gorm:"column:id"`
	Name  string `gorm:"column:name"`
	Lang  string `gorm:"column:lang"`
	Latin *string
}

// NameWorkRoleRow is one role a name holds on a work, with the voiced character
// (set only for voice credits).
type NameWorkRoleRow struct {
	WorkID      int64   `gorm:"column:work_id"`
	RoleID      int64   `gorm:"column:role_id"`
	RoleKey     string  `gorm:"column:role_key"`
	RoleNameCN  string  `gorm:"column:role_name_cn"`
	RoleNameJA  string  `gorm:"column:role_name_ja"`
	CharacterID *int64  `gorm:"column:character_id"`
	CharacterNM *string `gorm:"column:character_nm"`
}

// NameWorkDetail is one work a name is credited on, with every role it holds.
type NameWorkDetail struct {
	Brief WorkBriefRow
	Roles []NameWorkRoleRow
}

// NameWorksResult is the assembled name→works read.
type NameWorksResult struct {
	Head     *NameHeadRow
	Siblings []SiblingNameRow
	Works    []NameWorkDetail
	Total    int64
}

// NameWorks loads a credit name's self-description (with its person's other
// PUBLIC-linked names) plus the works it is credited on, offset-paginated by
// work. Head is nil when the name does not exist. The reverse lookup rides
// idx_catalog_credit_credit_name_id (no new index needed).
//
// Link-visibility doctrine (model.LinkVisibility, and the note in
// search/doc.go that defers this filter to "person-page assembly"): a hidden
// credit_name→person link never surfaces in same-person grouping. So when the
// queried name's own link is hidden, its person id and siblings are withheld —
// the name reads as an independent identity — and siblings are always filtered
// to public-linked names.
func (s *ReadService) NameWorks(ctx context.Context, nameID int64, limit, offset int) (*NameWorksResult, error) {
	db := s.db.WithContext(ctx)

	var head NameHeadRow
	if err := db.Raw(`SELECT id, name, lang, latin, person_id, link_visibility
		FROM catalog_credit_name WHERE id = ?`, nameID).Scan(&head).Error; err != nil {
		return nil, err
	}
	if head.ID == 0 {
		return &NameWorksResult{}, nil // caller maps head==nil to 404
	}
	res := &NameWorksResult{Head: &head}

	if head.LinkVisibility != model.LinkVisibilityPublic {
		head.PersonID = nil // hidden link: appear as an independent identity
	} else if head.PersonID != nil {
		if err := db.Raw(`SELECT id, name, lang, latin FROM catalog_credit_name
			WHERE person_id = ? AND id <> ? AND link_visibility = ?
			ORDER BY id`, *head.PersonID, nameID, model.LinkVisibilityPublic).Scan(&res.Siblings).Error; err != nil {
			return nil, err
		}
	}

	if err := db.Raw(`SELECT count(DISTINCT work_id) FROM catalog_credit WHERE credit_name_id = ?`,
		nameID).Scan(&res.Total).Error; err != nil {
		return nil, err
	}
	var workIDs []int64
	if err := db.Raw(`SELECT DISTINCT work_id FROM catalog_credit WHERE credit_name_id = ?
		ORDER BY work_id LIMIT ? OFFSET ?`, nameID, limit, offset).Scan(&workIDs).Error; err != nil {
		return nil, err
	}
	if len(workIDs) == 0 {
		return res, nil
	}
	briefs, err := s.workBriefs(ctx, workIDs)
	if err != nil {
		return nil, err
	}
	var roleRows []NameWorkRoleRow
	if err := db.Raw(`SELECT c.work_id, c.role_id, ro.key AS role_key,
		ro.name_cn AS role_name_cn, ro.name_ja AS role_name_ja,
		c.character_id, ch.display_name AS character_nm
		FROM catalog_credit c
		JOIN catalog_role ro ON ro.id = c.role_id
		LEFT JOIN catalog_character ch ON ch.id = c.character_id
		WHERE c.credit_name_id = ? AND c.work_id IN ?
		ORDER BY c.work_id, c.role_id, character_nm NULLS FIRST`, nameID, workIDs).Scan(&roleRows).Error; err != nil {
		return nil, err
	}
	rolesByWork := make(map[int64][]NameWorkRoleRow, len(workIDs))
	for _, r := range roleRows {
		rolesByWork[r.WorkID] = append(rolesByWork[r.WorkID], r)
	}
	for _, wid := range workIDs {
		b, ok := briefs[wid]
		if !ok {
			continue // soft-deleted work (see workBriefs) — keep total honest, drop the row
		}
		res.Works = append(res.Works, NameWorkDetail{Brief: b, Roles: rolesByWork[wid]})
	}
	return res, nil
}

// --- character → works (step 19: what a character appears in, and who voiced it) ---

// CharacterHeadRow is a character's own identity.
type CharacterHeadRow struct {
	ID          int64  `gorm:"column:id"`
	DisplayName string `gorm:"column:display_name"`
	Lang        string `gorm:"column:lang"`
	Latin       *string
}

// VoiceNameRow is one credited name that voiced a character on a work.
type VoiceNameRow struct {
	CreditNameID int64  `gorm:"column:credit_name_id"`
	Name         string `gorm:"column:name"`
	Lang         string `gorm:"column:lang"`
	Latin        *string
}

// CharacterWorkDetail is one work a character appears in (roster edge or voice
// credit, step 46), with the roster kind, whether it was voiced, and its voice
// names.
type CharacterWorkDetail struct {
	Brief  WorkBriefRow
	Kind   int16 // roster edge appearance strength; 0 when reached only via a credit
	Voiced bool  // a voice credit names the character on this work
	Voices []VoiceNameRow
}

// CharacterWorksResult is the assembled character→works read.
type CharacterWorksResult struct {
	Head  *CharacterHeadRow
	Works []CharacterWorkDetail
	Total int64
}

// CharacterWorks loads a character's self-description plus the works it appears
// in — the UNION (step 46) of the roster edge table (catalog_work_character,
// the appearance fact + kind) and the voice credits (catalog_credit.character_id,
// who voiced it). Each work carries its roster kind (0 when reached only via a
// credit), a voiced flag, and the voicing name(s). Offset-paginated by work.
// Head is nil when the character does not exist. Rides
// uq_catalog_work_character (character_id member) + idx_catalog_credit_character_id.
func (s *ReadService) CharacterWorks(ctx context.Context, characterID int64, limit, offset int) (*CharacterWorksResult, error) {
	db := s.db.WithContext(ctx)

	var head CharacterHeadRow
	if err := db.Raw(`SELECT id, display_name, lang, latin FROM catalog_character
		WHERE id = ? AND deleted_at IS NULL`, characterID).Scan(&head).Error; err != nil {
		return nil, err
	}
	if head.ID == 0 {
		return &CharacterWorksResult{}, nil // caller maps head==nil to 404
	}
	res := &CharacterWorksResult{Head: &head}

	// The work set is the union of roster-edge works and voice-credit works.
	const unionWorks = `SELECT work_id FROM catalog_work_character WHERE character_id = ?
		UNION SELECT work_id FROM catalog_credit WHERE character_id = ?`
	if err := db.Raw(`SELECT count(*) FROM (`+unionWorks+`) u`,
		characterID, characterID).Scan(&res.Total).Error; err != nil {
		return nil, err
	}
	var workIDs []int64
	if err := db.Raw(`SELECT work_id FROM (`+unionWorks+`) u ORDER BY work_id LIMIT ? OFFSET ?`,
		characterID, characterID, limit, offset).Scan(&workIDs).Error; err != nil {
		return nil, err
	}
	if len(workIDs) == 0 {
		return res, nil
	}
	briefs, err := s.workBriefs(ctx, workIDs)
	if err != nil {
		return nil, err
	}

	// Roster kind per work (edge present only for the roster subset).
	var kindRows []struct {
		WorkID int64 `gorm:"column:work_id"`
		Kind   int16 `gorm:"column:kind"`
	}
	if err := db.Raw(`SELECT work_id, kind FROM catalog_work_character
		WHERE character_id = ? AND work_id IN ?`, characterID, workIDs).Scan(&kindRows).Error; err != nil {
		return nil, err
	}
	kindByWork := make(map[int64]int16, len(kindRows))
	for _, k := range kindRows {
		kindByWork[k.WorkID] = k.Kind
	}

	// DISTINCT (work, name): the same name may hold several credit rows for one
	// character on one work (e.g. per release) — collapse to one voice entry.
	var voiceRows []struct {
		WorkID       int64  `gorm:"column:work_id"`
		CreditNameID int64  `gorm:"column:credit_name_id"`
		Name         string `gorm:"column:name"`
		Lang         string `gorm:"column:lang"`
		Latin        *string
	}
	if err := db.Raw(`SELECT DISTINCT c.work_id, cn.id AS credit_name_id, cn.name, cn.lang, cn.latin
		FROM catalog_credit c JOIN catalog_credit_name cn ON cn.id = c.credit_name_id
		WHERE c.character_id = ? AND c.work_id IN ?
		ORDER BY c.work_id, cn.id`, characterID, workIDs).Scan(&voiceRows).Error; err != nil {
		return nil, err
	}
	voicesByWork := make(map[int64][]VoiceNameRow, len(workIDs))
	for _, v := range voiceRows {
		voicesByWork[v.WorkID] = append(voicesByWork[v.WorkID], VoiceNameRow{
			CreditNameID: v.CreditNameID, Name: v.Name, Lang: v.Lang, Latin: v.Latin,
		})
	}
	for _, wid := range workIDs {
		b, ok := briefs[wid]
		if !ok {
			continue
		}
		voices := voicesByWork[wid]
		res.Works = append(res.Works, CharacterWorkDetail{
			Brief: b, Kind: kindByWork[wid], Voiced: len(voices) > 0, Voices: voices,
		})
	}
	return res, nil
}

// CharacterDetail is a character's full self-description (step 46): identity
// fields + aliases.
type CharacterDetail struct {
	ID          int64
	DisplayName string
	Latin       *string
	Lang        string
	Gender      *int16
	Description string
	InstanceOf  *int64
	ImageHash   *string
	Aliases     []CharacterAliasRow
}

// CharacterAliasRow is one writing-variant of a character's name.
type CharacterAliasRow struct {
	ID                 int64   `gorm:"column:id"`
	Name               string  `gorm:"column:name"`
	Latin              *string `gorm:"column:latin"`
	Lang               string  `gorm:"column:lang"`
	Kind               int16   `gorm:"column:kind"`
	IsPrimaryForLocale bool    `gorm:"column:is_primary_for_locale"`
}

// CharacterByID loads a character's identity fields plus its aliases. Returns
// (nil, nil) when the character does not exist (caller maps to 404), aligning
// with the labels/{id} miss semantics (step 20, 85f7f08).
func (s *ReadService) CharacterByID(ctx context.Context, characterID int64) (*CharacterDetail, error) {
	db := s.db.WithContext(ctx)

	var head struct {
		ID          int64   `gorm:"column:id"`
		DisplayName string  `gorm:"column:display_name"`
		Latin       *string `gorm:"column:latin"`
		Lang        string  `gorm:"column:lang"`
		Gender      *int16  `gorm:"column:gender"`
		Description string  `gorm:"column:description"`
		InstanceOf  *int64  `gorm:"column:instance_of"`
		ImageHash   *string `gorm:"column:image_hash"`
	}
	if err := db.Raw(`SELECT id, display_name, latin, lang, gender, description, instance_of, image_hash
		FROM catalog_character WHERE id = ? AND deleted_at IS NULL`, characterID).Scan(&head).Error; err != nil {
		return nil, err
	}
	if head.ID == 0 {
		return nil, nil // caller maps nil to 404
	}
	detail := &CharacterDetail{
		ID: head.ID, DisplayName: head.DisplayName, Latin: head.Latin, Lang: head.Lang,
		Gender: head.Gender, Description: head.Description, InstanceOf: head.InstanceOf, ImageHash: head.ImageHash,
	}
	if err := db.Raw(`SELECT id, name, latin, lang, kind, is_primary_for_locale
		FROM catalog_character_alias WHERE character_id = ? ORDER BY id`, characterID).Scan(&detail.Aliases).Error; err != nil {
		return nil, err
	}
	return detail, nil
}

// CreditRow is one credit joined with its role, name, character and source.
type CreditRow struct {
	RoleID       int64
	RoleKey      string
	RoleNameCN   string
	RoleNameJA   string
	CreditNameID int64
	Name         string
	Lang         string
	Latin        *string
	CharacterID  *int64
	CharacterNM  *string
	Note         string
	SourceKey    *string
}

// WorkCredits loads a work's credits ordered by role then source then name.
// Orphan credit names are returned as-is (no person layer). The caller groups
// consecutive rows by role.
func (s *ReadService) WorkCredits(ctx context.Context, workID int64) ([]CreditRow, error) {
	var rows []CreditRow
	err := s.db.WithContext(ctx).Raw(`SELECT
		c.role_id, ro.key AS role_key, ro.name_cn AS role_name_cn, ro.name_ja AS role_name_ja,
		cn.id AS credit_name_id, cn.name, cn.lang, cn.latin,
		c.character_id, ch.display_name AS character_nm, c.note, src.key AS source_key
		FROM catalog_credit c
		JOIN catalog_role ro ON ro.id = c.role_id
		JOIN catalog_credit_name cn ON cn.id = c.credit_name_id
		LEFT JOIN catalog_character ch ON ch.id = c.character_id
		LEFT JOIN catalog_source src ON src.id = c.source_id
		WHERE c.work_id = ?
		ORDER BY c.role_id ASC, src.key ASC NULLS LAST, cn.id ASC`, workID).Scan(&rows).Error
	return rows, err
}
