package personmint

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// TierAuto is the only wave-152 tier this wave consumes. `review` (the 84
// consistency-guard downgrades) and anything else belongs to a human queue.
const TierAuto = "auto"

// Cluster is the slice of a wave-152 clusters.jsonl line this wave reads. The
// file carries much more (per-edge evidence, guards, source anchors); the mint
// deliberately re-derives anchors from the DATABASE instead of trusting the
// artefact's copy, so only the membership and the tier are consumed here.
type Cluster struct {
	ClusterID     string   `json:"cluster_id"`
	CreditNameIDs []int64  `json:"credit_name_ids"`
	Names         []string `json:"names"`
	Tier          string   `json:"tier"`
}

// LoadClusters reads clusters.jsonl and returns the tier=auto clusters in
// deterministic order (by cluster id, members sorted). total is every line
// read, so the caller can reconcile against the wave-152 headline.
func LoadClusters(path string) (clusters []Cluster, total int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("open clusters: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	// Cluster lines carry the full evidence chain; the 59-member tail lines
	// are far past bufio's 64KiB default.
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	seen := map[int64]string{}
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		total++
		var c Cluster
		if err := json.Unmarshal(line, &c); err != nil {
			return nil, 0, fmt.Errorf("parse cluster line %d: %w", total, err)
		}
		if c.Tier != TierAuto {
			continue
		}
		if len(c.CreditNameIDs) < 2 {
			return nil, 0, fmt.Errorf("cluster %s has %d members: a cluster is an equivalence class of at least two names", c.ClusterID, len(c.CreditNameIDs))
		}
		// A credit name in two clusters would break the union-find premise and
		// silently fork one identity across two persons.
		for _, id := range c.CreditNameIDs {
			if other, dup := seen[id]; dup {
				return nil, 0, fmt.Errorf("credit_name %d appears in clusters %s and %s: the input is not a partition", id, other, c.ClusterID)
			}
			seen[id] = c.ClusterID
		}
		sort.Slice(c.CreditNameIDs, func(i, j int) bool { return c.CreditNameIDs[i] < c.CreditNameIDs[j] })
		clusters = append(clusters, c)
	}
	if err := sc.Err(); err != nil {
		return nil, 0, fmt.Errorf("read clusters: %w", err)
	}
	sort.Slice(clusters, func(i, j int) bool { return clusters[i].ClusterID < clusters[j].ClusterID })
	return clusters, total, nil
}

// LoadSplitWorklist reads e4_p2_split_worklist.jsonl — the 593 credit names
// wave 152 flagged as possibly carrying anchors of SEVERAL people (bare
// surnames, <=2-char handles, company names). Their identity must be settled by
// a human before a person is minted on them.
func LoadSplitWorklist(path string) (map[int64]bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open split worklist: %w", err)
	}
	defer f.Close()
	out := map[int64]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<16), 4<<20)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var row struct {
			CreditNameID int64 `json:"credit_name_id"`
		}
		if err := json.Unmarshal(sc.Bytes(), &row); err != nil {
			return nil, fmt.Errorf("parse split worklist: %w", err)
		}
		if row.CreditNameID == 0 {
			return nil, fmt.Errorf("split worklist row without credit_name_id")
		}
		out[row.CreditNameID] = true
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read split worklist: %w", err)
	}
	return out, nil
}
