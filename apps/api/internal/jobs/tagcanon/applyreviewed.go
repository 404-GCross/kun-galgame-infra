package tagcanon

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// singleCoreUsageFloor is the deterministic tier gate for single-source
// admissions (doc 90 ruling 6, codifying the 87 closure calibration): an
// LLM-proposed core only sticks when the name's usage clears this floor;
// otherwise it lands longtail. Cross-source exact groups are not gated (74
// convention: cross-source = core).
const singleCoreUsageFloor = 1000

// ApplyReviewedOpts configures apply-reviewed (doc 87 P2 --apply-reviewed): it
// consumes the decisions JSONL (make-review output, human-edited for the medium
// batch) and writes the approved decisions into the canonical layer. Single
// catalog DSN — no staging, no LLM (the verdicts are already made).
type ApplyReviewedOpts struct {
	DSN       string
	Decisions string
	Apply     bool // dry (default) decides everything but writes nothing
}

// ApplyReviewedStats reports a run. The plan counters (Groups/SingleRows) are
// identical in dry and apply; the *Created/*Conflict/TierUpdated are apply-only.
type ApplyReviewedStats struct {
	ApprovedPairs int // exact pairs with Approve=true consumed
	Groups        int // canonical groups formed by union-find over approved exact pairs
	SingleRows    int // single-source admission rows (approved, not absorbed by a group)
	TagsCreated   int
	TagsConflict  int
	MapsCreated   int
	MapsConflict  int
	TierUpdated   int // explicit tier/kind UPDATE path fired (70b rows only)
	Errors        int
}

// ApplyReviewed builds the canonical layer from the approved decisions. Order:
//  1. union-find over approved exact pairs → cross-source groups (tier=core per
//     74 convention; kind=meta if any member norm is hand-pinned meta);
//  2. approved single-source rows (LLM-proposed tier/kind) for names NOT
//     absorbed by a group;
//  3. an explicit, idempotent tier/kind UPDATE for every 70b row this wave
//     manages (the "tier 终标" write path — re-applying an edited decision takes
//     effect; a second identical apply writes zero).
//
// All writes are ON CONFLICT DO NOTHING (name / (source_id,source_name)); the
// 74 layer's 171 groups are never touched (their names are excluded from the
// decisions upstream, and group inserts conflict-skip).
func ApplyReviewed(ctx context.Context, opts ApplyReviewedOpts) (*ApplyReviewedStats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn)")
	}
	if opts.Decisions == "" {
		return nil, fmt.Errorf("decisions path is required (--decisions)")
	}
	recs, err := readRecords(opts.Decisions)
	if err != nil {
		return nil, err
	}
	db, err := openGorm(opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect catalog db: %w", err)
	}
	if sqlDB, e := db.DB(); e == nil {
		defer sqlDB.Close()
	}
	src, err := resolveSources(ctx, db)
	if err != nil {
		return nil, err
	}
	keyToID := map[string]int16{sourceKeyVNDB: src.vndb, sourceKeyBangumi: src.bangumi, sourceKeyDlsite: src.dlsite}

	st := &ApplyReviewedStats{}

	groups, absorbed := groupsFromPairs(recs, keyToID, st)
	singles := singlesFromRecords(recs, keyToID, absorbed)
	st.Groups = len(groups)
	st.SingleRows = len(singles)

	// dry run: everything decided, nothing written.
	if !opts.Apply {
		slog.Info("apply-reviewed (dry)", "approved_pairs", st.ApprovedPairs,
			"groups", st.Groups, "single_rows", st.SingleRows)
		return st, nil
	}

	inner := &Stats{JunkByReason: map[string]int{}}
	w := &writer{db: db, stats: inner}

	for _, g := range groups {
		w.writeGroup(ctx, g, true)
	}
	for _, s := range singles {
		id, ok := w.ensureTag(ctx, s.group)
		if !ok {
			continue // error counted in inner.Errors
		}
		writeMemberMap(ctx, db, s.group.Members[0], id, inner)
		// explicit tier/kind finalization — 70b's deliberate second write path.
		if n := stampTierKind(ctx, db, id, s.group.Tier, s.group.Kind); n > 0 {
			st.TierUpdated += n
		}
	}

	st.TagsCreated = inner.TagsCreated
	st.TagsConflict = inner.TagsConflict
	st.MapsCreated = inner.MapsCreated
	st.MapsConflict = inner.MapsConflict
	st.Errors = inner.Errors
	slog.Info("apply-reviewed done", "groups", st.Groups, "single_rows", st.SingleRows,
		"tags_created", st.TagsCreated, "tags_conflict", st.TagsConflict,
		"maps_created", st.MapsCreated, "maps_conflict", st.MapsConflict,
		"tier_updated", st.TierUpdated, "errors", st.Errors)
	return st, nil
}

// singleRow is one approved single-source admission (a one-member group carrying
// the LLM-proposed tier/kind).
type singleRow struct {
	group group
}

// groupsFromPairs runs union-find over the approved exact pairs and materializes
// one group per component. absorbed is the set of member node keys ("id\x00name")
// that a group already covers (so a single-source row for the same name is
// suppressed — group membership wins). Non-exact / unapproved pairs are ignored.
func groupsFromPairs(recs []pairRec, keyToID map[string]int16, st *ApplyReviewedStats) ([]group, map[string]struct{}) {
	uf := newDSU()
	info := map[string]vocabEntry{} // node key → member (source id + name + usage)
	for _, r := range recs {
		if r.Kind != "pair" || !r.Approve || Relation(r.Relation) != RelExact {
			continue
		}
		// Resolve BOTH sides before touching info — an unresolvable source key
		// must never orphan the other side into a bogus one-member group.
		ia, oka := keyToID[r.ASource]
		ib, okb := keyToID[r.BSource]
		if !oka || !okb {
			continue
		}
		ka, kb := mapKey(ia, r.AName), mapKey(ib, r.BName)
		if _, seen := info[ka]; !seen {
			info[ka] = vocabEntry{SourceID: ia, Name: r.AName, Norm: normalize(r.AName), Usage: r.AUsage}
		}
		if _, seen := info[kb]; !seen {
			info[kb] = vocabEntry{SourceID: ib, Name: r.BName, Norm: normalize(r.BName), Usage: r.BUsage}
		}
		st.ApprovedPairs++
		uf.union(ka, kb)
	}

	comps := map[string][]vocabEntry{}
	for k := range info {
		root := uf.find(k)
		comps[root] = append(comps[root], info[k])
	}
	absorbed := map[string]struct{}{}
	var groups []group
	for _, members := range comps {
		srcs := map[int16]struct{}{}
		kind := model.TagKindContent
		for _, m := range members {
			srcs[m.SourceID] = struct{}{}
			if isMeta(m.Norm) {
				kind = model.TagKindMeta
			}
		}
		if len(srcs) < 2 {
			continue // degenerate component (e.g. a self-pair) — not a cross-source
			// group; leave the name for single-source admission, do NOT absorb.
		}
		for _, m := range members {
			absorbed[mapKey(m.SourceID, m.Name)] = struct{}{}
		}
		sort.Slice(members, func(i, j int) bool {
			if members[i].SourceID != members[j].SourceID {
				return members[i].SourceID < members[j].SourceID
			}
			return members[i].Name < members[j].Name
		})
		groups = append(groups, group{
			CanonicalName: pickCanonicalName(members),
			Tier:          model.TagTierCore, // cross-source exact = core (74 convention, ruling 4)
			Kind:          kind,
			Members:       members,
			sourceCount:   len(srcs),
		})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].CanonicalName < groups[j].CanonicalName })
	return groups, absorbed
}

// singlesFromRecords materializes approved single-source admission rows for
// names NOT absorbed by a group.
func singlesFromRecords(recs []pairRec, keyToID map[string]int16, absorbed map[string]struct{}) []singleRow {
	var out []singleRow
	for _, r := range recs {
		if r.Kind != "single" || !r.Approve {
			continue
		}
		id, ok := keyToID[r.Source]
		if !ok || r.Tier == nil || r.Kind_ == nil {
			continue
		}
		k := mapKey(id, r.Name)
		if _, hit := absorbed[k]; hit {
			continue // a group already canonicalizes this name
		}
		tier := *r.Tier
		if tier == model.TagTierCore && r.Usage < singleCoreUsageFloor {
			tier = model.TagTierLongtail
		}
		out = append(out, singleRow{
			group: group{
				CanonicalName: r.Name,
				Tier:          tier,
				Kind:          *r.Kind_,
				Members:       []vocabEntry{{SourceID: id, Name: r.Name, Norm: normalize(r.Name), Usage: r.Usage}},
				sourceCount:   1,
			},
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].group.CanonicalName < out[j].group.CanonicalName })
	return out
}

// writeMemberMap writes one source_map row (ON CONFLICT DO NOTHING), counting
// into the shared Stats — the single-source analogue of writer.writeGroup's map
// loop.
func writeMemberMap(ctx context.Context, db *gorm.DB, m vocabEntry, tagID int64, st *Stats) {
	res := db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "source_id"}, {Name: "source_name"}},
		DoNothing: true,
	}).Create(&model.CatalogTagSourceMap{SourceID: m.SourceID, SourceName: m.Name, TagID: tagID})
	if res.Error != nil {
		st.Errors++
		slog.Warn("write single map", "source", m.SourceID, "name", m.Name, "err", res.Error)
		return
	}
	if res.RowsAffected == 0 {
		st.MapsConflict++
		return
	}
	st.MapsCreated++
}

// stampTierKind is 70b's explicit tier/kind finalization: it UPDATEs only when
// the stored value differs (so a re-applied edited decision takes effect and a
// second identical apply writes zero — the idempotence guarantee). Scoped by tag
// id, it only ever touches the 70b row it is called for; the 74 layer's rows are
// never passed here. Returns rows updated (0 or 1).
func stampTierKind(ctx context.Context, db *gorm.DB, tagID int64, tier, kind int16) int {
	res := db.WithContext(ctx).Exec(
		`UPDATE catalog_tag SET tier = ?, kind = ?, updated_at = now() WHERE id = ? AND (tier <> ? OR kind <> ?)`,
		tier, kind, tagID, tier, kind)
	if res.Error != nil {
		slog.Warn("stamp tier/kind", "id", tagID, "err", res.Error)
		return 0
	}
	return int(res.RowsAffected)
}

// ── tiny union-find ──────────────────────────────────────────────────────────

type dsu struct{ parent map[string]string }

func newDSU() *dsu { return &dsu{parent: map[string]string{}} }

func (d *dsu) find(x string) string {
	p, ok := d.parent[x]
	if !ok {
		d.parent[x] = x
		return x
	}
	if p != x {
		d.parent[x] = d.find(p)
	}
	return d.parent[x]
}

func (d *dsu) union(a, b string) {
	ra, rb := d.find(a), d.find(b)
	if ra != rb {
		d.parent[ra] = rb
	}
}
