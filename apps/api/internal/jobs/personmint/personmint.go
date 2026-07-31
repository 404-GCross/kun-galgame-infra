// Package personmint mints catalog_person entities out of the wave-152 person
// evidence graph (refs/proj/153, program charter refs/proj/150).
//
// Input is the DURABLE artefact pair produced by wave 152 and committed under
// refs/proj/152-artifacts/ — clusters.jsonl (6,194 clusters, of which the 6,110
// tier=auto ones are the only ones this wave touches) and
// e4_p2_split_worklist.jsonl (593 suspect credit names). Nothing is recomputed
// here: the evidence graph is an input, not a step.
//
// What a minted cluster writes, and nothing else:
//
//   - a HOST catalog_person — an existing one when the cluster already
//     intersects the 2,104-row genesis batch (reuse, never a second row), a new
//     one otherwise;
//   - catalog_credit_name.person_id = host for every member that carries NULL.
//     A member already pointing at a DIFFERENT person is a contradiction, not
//     an update: the whole cluster defers (this pipeline never rewrites an
//     existing link);
//   - et=0 (person-grained) catalog_external_ref anchors. The vndb anchor is
//     the STAFF id reached through staff_alias.aid — never the aid itself,
//     which is the alias-grained et=1 anchor already on the credit name
//     (wave 152 report §2.1 / §7.6). bangumi contributes its person id,
//     dlsite / erogamespace their creater id verbatim;
//   - survivorship into the host's EMPTY fields only: gender (vndb staff.gender
//     corroborated by the bangumi 性别 infobox field) and birth_y/m/d (the
//     bangumi 生日 field). An existing value is NEVER overwritten — not even by
//     an agreeing source — and a cross-source disagreement writes nothing and
//     is reported. Provenance follows the R8 array shape (wave 81).
//
// Deliberately NOT done here (doc 150 §2 rulings + doc 153):
//
//   - no read-face change: person stays an internal/S2S entity, credits keep
//     rendering credit_name. No endpoint, no DTO, no spec, no schema, no
//     migration.
//   - no TouchWorks: person does not reach any work read face (the wave-118
//     touch calculus), so a mint bumps no changes-feed watermark.
//   - no person MERGE: a cluster that would merge two existing persons, or a
//     person that appears in more than one cluster, defers to P2b. The merge
//     machinery is not exercised by this wave.
//
// Every decision is deterministic (clusters and members are processed in
// sorted order), so dry and apply decide identically and a second --apply
// writes zero rows.
package personmint

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// maxSamples caps the minted chains a run collects for logging / assertions.
const maxSamples = 10

// Opts configures a run. Apply=false is a dry-run forecast. DSN and
// ClustersPath are REQUIRED; SplitWorklistPath is required too — running
// without the E4 exclusion list would mint the very names wave 152 flagged.
type Opts struct {
	Apply             bool
	DSN               string
	ClustersPath      string
	SplitWorklistPath string
	Limit             int
	Offset            int
}

// DeferReason is why a cluster was not minted. The reasons are evaluated in
// the order of the doc-153 rule list and the FIRST one wins, so the buckets
// partition the deferred set; Overlap counts every reason that also applied.
type DeferReason string

const (
	// DeferE4Split — a member is on the wave-152 split worklist (bare
	// surnames, <=2-char handles, company names in the risk stratum). The
	// anchor set itself is suspect, so identity may not be minted on it.
	DeferE4Split DeferReason = "e4_split_worklist"
	// DeferOrgLabel — a member's normalized name collides with a
	// catalog_label (or label alias) name: the cluster is at least partly an
	// organization, and catalog_credit_name has no person/organization
	// discriminator to separate them (wave 152 §7.5).
	DeferOrgLabel DeferReason = "org_label_collision"
	// DeferOrgPattern — a member's name carries an explicit company/circle
	// marker (株式会社, スタジオ, Inc., Studio, ...).
	DeferOrgPattern DeferReason = "org_name_pattern"
	// DeferPersonMulti — the cluster resolves to two or more existing
	// persons. That is a person MERGE, which this wave does not perform.
	DeferPersonMulti DeferReason = "multiple_existing_persons"
	// DeferPersonCrossCluster — an existing person is spread over more than
	// one auto cluster; adopting it as a host in one of them would silently
	// pre-empt the merge decision in the others.
	DeferPersonCrossCluster DeferReason = "person_spans_clusters"
)

// deferOrder is the evaluation order of the rules (doc 153 §铸造规则 1-3).
var deferOrder = []DeferReason{
	DeferE4Split, DeferOrgLabel, DeferOrgPattern, DeferPersonMulti, DeferPersonCrossCluster,
}

// Defer is one deferred cluster, for the P2b worklist.
type Defer struct {
	ClusterID string        `json:"cluster_id"`
	Reason    DeferReason   `json:"reason"`
	Also      []DeferReason `json:"also,omitempty"`
	Members   []int64       `json:"credit_name_ids"`
	Names     []string      `json:"names"`
	Detail    string        `json:"detail,omitempty"`
}

// GenderConflict is a cluster whose gender sources disagree. Nothing is
// written for such a cluster's gender (the column stays NULL).
type GenderConflict struct {
	ClusterID string   `json:"cluster_id"`
	Values    []string `json:"values"`
	Names     []string `json:"names"`
}

// Sample is one minted chain, for dry-run logging and human spot checks.
type Sample struct {
	ClusterID   string   `json:"cluster_id"`
	PersonID    int64    `json:"person_id"`
	Reused      bool     `json:"reused"`
	DisplayName string   `json:"display_name"`
	Members     []int64  `json:"credit_name_ids"`
	Names       []string `json:"names"`
	Anchors     []string `json:"anchors"`
}

// Stats reports the run. The DECIDED counters (Would*, Defer*, Gender/Birth
// decisions) are identical in dry and apply; the Inserted / Updated counters
// only move in apply.
type Stats struct {
	// ClustersTotal is every line of clusters.jsonl; ClustersAuto is the
	// tier=auto subset this wave consumes, Members its credit names.
	ClustersTotal int
	ClustersAuto  int
	Members       int
	// Deferred counts and its per-reason breakdown. Reasons partition
	// Deferred; Overlap counts the reasons that ALSO applied to a cluster
	// already deferred for an earlier reason.
	Deferred int
	Defers   map[DeferReason]int
	Overlap  map[DeferReason]int
	// Minted = ClustersAuto - Deferred, split by host provenance.
	Minted      int
	MintedNew   int
	MintedReuse int
	// WouldCreatePerson / WouldLink / WouldAnchor are the planned writes;
	// LinksAlready / AnchorsAlready are what a re-run finds in place.
	WouldCreatePerson int
	WouldLink         int
	LinksAlready      int
	WouldAnchor       int
	AnchorsAlready    int
	// Survivorship decisions. *Set counts a value written into an EMPTY
	// field; *Kept counts a host that already had one (never overwritten);
	// GenderConflicts counts clusters whose sources disagree.
	WouldSetGender  int
	GenderKept      int
	GenderConflicts int
	WouldSetBirth   int
	BirthKept       int
	// BirthConflicts counts clusters whose bangumi persons disagree on the
	// birth date — nothing is written for those either.
	BirthConflicts int
	// Apply-only counters.
	PersonsCreated int
	LinksWritten   int
	AnchorsWritten int
	PersonsUpdated int
	Errors         int

	Conflicts []GenderConflict
	DeferList []Defer
	Samples   []Sample
}

func newStats() *Stats {
	return &Stats{Defers: map[DeferReason]int{}, Overlap: map[DeferReason]int{}}
}

// Run loads the evidence-graph artefacts, decides every auto cluster against
// the live catalog and forecasts (dry) or performs (apply) the mint.
func Run(ctx context.Context, opts Opts) (*Stats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn); refusing to guess — the rehearsal copy locally, the live catalog only in the acceptance run")
	}
	if opts.ClustersPath == "" || opts.SplitWorklistPath == "" {
		return nil, fmt.Errorf("--clusters and --split-worklist are required (refs/proj/152-artifacts/)")
	}
	clusters, total, err := LoadClusters(opts.ClustersPath)
	if err != nil {
		return nil, err
	}
	split, err := LoadSplitWorklist(opts.SplitWorklistPath)
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

	st := newStats()
	st.ClustersTotal = total
	clusters = window(clusters, opts.Limit, opts.Offset)
	st.ClustersAuto = len(clusters)
	memberIDs := make([]int64, 0, len(clusters)*3)
	for _, c := range clusters {
		st.Members += len(c.CreditNameIDs)
		memberIDs = append(memberIDs, c.CreditNameIDs...)
	}

	env, err := loadEnvironment(ctx, db, memberIDs)
	if err != nil {
		return nil, err
	}
	slog.Info("person-mint loaded", "clusters_total", st.ClustersTotal, "clusters_auto", st.ClustersAuto,
		"members", st.Members, "split_worklist", len(split), "label_norms", len(env.labelNorms),
		"existing_person_links", len(env.personOfMember), "existing_et0", len(env.et0Owner), "apply", opts.Apply)

	d := &decider{env: env, split: split, crossCluster: crossClusterPersons(clusters, env)}
	w := &writer{db: db, env: env, stats: st, apply: opts.Apply}
	for _, c := range clusters {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		plan, dfr := d.decide(c)
		if dfr != nil {
			st.Deferred++
			st.Defers[dfr.Reason]++
			for _, also := range dfr.Also {
				st.Overlap[also]++
			}
			st.DeferList = append(st.DeferList, *dfr)
			continue
		}
		st.Minted++
		if plan.HostID == 0 {
			st.MintedNew++
		} else {
			st.MintedReuse++
		}
		if err := w.mint(ctx, plan); err != nil {
			return nil, err
		}
	}
	sortDefers(st.DeferList)
	slog.Info("person-mint done", "apply", opts.Apply,
		"clusters_auto", st.ClustersAuto, "minted", st.Minted, "minted_new", st.MintedNew,
		"minted_reuse", st.MintedReuse, "deferred", st.Deferred, "defers", st.Defers,
		"would_create_person", st.WouldCreatePerson, "would_link", st.WouldLink, "links_already", st.LinksAlready,
		"would_anchor", st.WouldAnchor, "anchors_already", st.AnchorsAlready,
		"would_set_gender", st.WouldSetGender, "gender_conflicts", st.GenderConflicts,
		"would_set_birth", st.WouldSetBirth,
		"persons_created", st.PersonsCreated, "links_written", st.LinksWritten,
		"anchors_written", st.AnchorsWritten, "persons_updated", st.PersonsUpdated, "errors", st.Errors)
	return st, nil
}

// crossClusterPersons collects the existing persons whose credit names are
// spread over more than one auto cluster. It is computed from the PRE-RUN
// links only, which is what makes it stable across passes: a person this wave
// mints is reachable from exactly one cluster by construction.
func crossClusterPersons(clusters []Cluster, env *environment) map[int64]int {
	seen := map[int64]map[string]bool{}
	for _, c := range clusters {
		for _, id := range c.CreditNameIDs {
			pid, ok := env.personOfMember[id]
			if !ok {
				continue
			}
			if seen[pid] == nil {
				seen[pid] = map[string]bool{}
			}
			seen[pid][c.ClusterID] = true
		}
	}
	out := map[int64]int{}
	for pid, cs := range seen {
		if len(cs) > 1 {
			out[pid] = len(cs)
		}
	}
	return out
}

func sortDefers(d []Defer) {
	sort.Slice(d, func(i, j int) bool {
		if d[i].Reason != d[j].Reason {
			return indexOfReason(d[i].Reason) < indexOfReason(d[j].Reason)
		}
		return d[i].ClusterID < d[j].ClusterID
	})
}

func indexOfReason(r DeferReason) int {
	for i, x := range deferOrder {
		if x == r {
			return i
		}
	}
	return len(deferOrder)
}

// window applies offset/limit over the cluster list (the chunking discipline
// of the other catalog backfills).
func window[T any](out []T, limit, offset int) []T {
	if offset > 0 {
		if offset >= len(out) {
			return nil
		}
		out = out[offset:]
	}
	if limit > 0 && limit < len(out) {
		out = out[:limit]
	}
	return out
}

func openGorm(dsn string) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
}
