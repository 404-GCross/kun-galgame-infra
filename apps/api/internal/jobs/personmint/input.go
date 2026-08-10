package personmint

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

const TierAuto = "auto"

type Cluster struct {
	ClusterID     string   `json:"cluster_id"`
	CreditNameIDs []int64  `json:"credit_name_ids"`
	Names         []string `json:"names"`
	Tier          string   `json:"tier"`
}

func LoadClusters(path string) (clusters []Cluster, total int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("open clusters: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
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
