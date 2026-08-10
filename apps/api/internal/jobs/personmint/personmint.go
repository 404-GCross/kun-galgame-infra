package personmint

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"api/internal/infrastructure/database"

	"gorm.io/gorm"
)

const maxSamples = 10

type Opts struct {
	Apply             bool
	DSN               string
	ClustersPath      string
	SplitWorklistPath string
	Limit             int
	Offset            int
}

type DeferReason string

const (
	DeferE4Split            DeferReason = "e4_split_worklist"
	DeferOrgLabel           DeferReason = "org_label_collision"
	DeferOrgPattern         DeferReason = "org_name_pattern"
	DeferPersonMulti        DeferReason = "multiple_existing_persons"
	DeferPersonCrossCluster DeferReason = "person_spans_clusters"
)

var deferOrder = []DeferReason{
	DeferE4Split, DeferOrgLabel, DeferOrgPattern, DeferPersonMulti, DeferPersonCrossCluster,
}

type Defer struct {
	ClusterID string        `json:"cluster_id"`
	Reason    DeferReason   `json:"reason"`
	Also      []DeferReason `json:"also,omitempty"`
	Members   []int64       `json:"credit_name_ids"`
	Names     []string      `json:"names"`
	Detail    string        `json:"detail,omitempty"`
}

type GenderConflict struct {
	ClusterID string   `json:"cluster_id"`
	Values    []string `json:"values"`
	Names     []string `json:"names"`
}

type Sample struct {
	ClusterID   string   `json:"cluster_id"`
	PersonID    int64    `json:"person_id"`
	Reused      bool     `json:"reused"`
	DisplayName string   `json:"display_name"`
	Members     []int64  `json:"credit_name_ids"`
	Names       []string `json:"names"`
	Anchors     []string `json:"anchors"`
}

type Stats struct {
	ClustersTotal     int
	ClustersAuto      int
	Members           int
	Deferred          int
	Defers            map[DeferReason]int
	Overlap           map[DeferReason]int
	Minted            int
	MintedNew         int
	MintedReuse       int
	WouldCreatePerson int
	WouldLink         int
	LinksAlready      int
	WouldAnchor       int
	AnchorsAlready    int
	WouldSetGender    int
	GenderKept        int
	GenderConflicts   int
	WouldSetBirth     int
	BirthKept         int
	BirthConflicts    int
	PersonsCreated    int
	LinksWritten      int
	AnchorsWritten    int
	PersonsUpdated    int
	Errors            int

	Conflicts []GenderConflict
	DeferList []Defer
	Samples   []Sample
}

func newStats() *Stats {
	return &Stats{Defers: map[DeferReason]int{}, Overlap: map[DeferReason]int{}}
}

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
	return database.OpenJob(dsn)
}
