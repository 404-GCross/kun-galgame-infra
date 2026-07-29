package wikirescue

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"api/internal/platform/catalog/model"
)

// matchedByOid is the provenance rule stamped on every official mapping row.
const matchedByOid = "wiki:oid"

// parkedOfficial records an official whose label pointer never got filled.
type parkedOfficial struct {
	OfficialID int64  `json:"official_id"`
	Name       string `json:"name"`
}

// stepOfficialMap lands the official id → catalog_label address map (A2-0,
// refs/proj/127).
//
// Today the ONLY thing that turns a wiki official id into a catalog label is the
// bridge column galgame_official.catalog_label_id. That column dies with the
// table, and with it 23k+ live /galgame/official/<id> URLs and every downstream
// row still holding an oid. The mapping therefore has to move into
// catalog_external_ref, which is where every other id space already lives — the
// exact argument step I made for gids.
//
// link_kind is EXACT: an oid IS the label's first-party identity in the wiki id
// space, not a reference to some other page. Both sides are 1:1 (galgame_official.id
// is a primary key), so the partial unique index on (source_id, external_id,
// entity_type) WHERE link_kind=0 is satisfied by construction.
//
// Deliberately does NOT touch anything. The changes feed is a WORKS stream
// (doc 106 G2: entity_type=work only), and a label is not in it — step I touches
// because a work's refs[] are part of the work's public face, whereas these rows
// land on labels, which the feed does not carry. Touching the labels' works
// would be a fabricated work-level event for a row no work-facing response
// changed.
func (r *Runner) stepOfficialMap(ctx context.Context) (Stats, error) {
	st := Stats{Step: "j"}

	var existing int64
	if err := r.catalog.WithContext(ctx).Raw(
		`SELECT count(*) FROM catalog_external_ref WHERE entity_type = ? AND source_id = ?`,
		model.EntityTypeLabel, r.wikiSrc).Scan(&existing).Error; err != nil {
		return st, fmt.Errorf("probe existing oid refs: %w", err)
	}

	type official struct {
		ID             int64
		Name           string
		CatalogLabelID int64
	}
	var officials []official
	if err := r.galgame.WithContext(ctx).Raw(
		`SELECT id, coalesce(name, '') AS name, coalesce(catalog_label_id, 0) AS catalog_label_id
		 FROM galgame_official ORDER BY id`).Scan(&officials).Error; err != nil {
		return st, fmt.Errorf("read galgame_official: %w", err)
	}
	st.Source = len(officials)

	now := time.Now().UTC()
	rows := make([][]any, 0, len(officials))
	parked := make([]parkedOfficial, 0)
	for _, o := range officials {
		// Step G projects the whole tail, so after W0 an unmapped official is an
		// anomaly, not the normal case — park it with its name and report the count.
		if o.CatalogLabelID == 0 {
			parked = append(parked, parkedOfficial{OfficialID: o.ID, Name: o.Name})
			continue
		}
		rows = append(rows, []any{
			model.EntityTypeLabel, o.CatalogLabelID, r.wikiSrc, strconv.FormatInt(o.ID, 10),
			model.LinkKindExact, matchedByOid, now,
		})
	}
	st.Anchored = len(rows)
	st.Parked = len(parked)
	st.Planned = len(rows)
	st.Note = fmt.Sprintf("pre-existing entity_type=label source=galgame_wiki refs: %d; officials with no catalog_label_id: %d",
		existing, len(parked))

	if err := r.park("j-officials-unmapped", parked); err != nil {
		return st, err
	}
	if !r.opts.Apply {
		return st, nil
	}

	landed, err := insertReturning(r.catalog.WithContext(ctx), "catalog_external_ref",
		[]string{"entity_type", "entity_id", "source_id", "external_id", "link_kind", "matched_by", "created_at"},
		"entity_id", rows)
	if err != nil {
		return st, err
	}
	st.Written = len(landed)
	return st, nil
}
