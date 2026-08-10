package personmint

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestLoadClusters(t *testing.T) {
	path := writeFile(t, "clusters.jsonl", `{"cluster_id":"C000002","credit_name_ids":[9,4],"names":["b","a"],"tier":"auto"}
{"cluster_id":"C000001","credit_name_ids":[2,1],"names":["x","y"],"tier":"auto"}
{"cluster_id":"C000003","credit_name_ids":[7,8],"names":["p","q"],"tier":"review"}

`)
	clusters, total, err := LoadClusters(path)
	require.NoError(t, err)
	assert.Equal(t, 3, total, "total counts review clusters too")
	require.Len(t, clusters, 2, "only tier=auto is consumed")
	assert.Equal(t, "C000001", clusters[0].ClusterID)
	assert.Equal(t, []int64{1, 2}, clusters[0].CreditNameIDs, "members are sorted for a stable primary tiebreak")
	assert.Equal(t, []int64{4, 9}, clusters[1].CreditNameIDs)
}

func TestLoadClustersRejectsOverlap(t *testing.T) {
	path := writeFile(t, "clusters.jsonl", `{"cluster_id":"C1","credit_name_ids":[1,2],"tier":"auto"}
{"cluster_id":"C2","credit_name_ids":[2,3],"tier":"auto"}`)
	_, _, err := LoadClusters(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a partition")
}

func TestLoadClustersRejectsSingleton(t *testing.T) {
	path := writeFile(t, "clusters.jsonl", `{"cluster_id":"C1","credit_name_ids":[1],"tier":"auto"}`)
	_, _, err := LoadClusters(path)
	require.Error(t, err)
}

func TestLoadSplitWorklist(t *testing.T) {
	path := writeFile(t, "e4.jsonl", `{"credit_name_id":451,"name":"細井治"}
{"credit_name_id":496,"name":"笹沼晃"}
`)
	got, err := LoadSplitWorklist(path)
	require.NoError(t, err)
	assert.Equal(t, map[int64]bool{451: true, 496: true}, got)

	bad := writeFile(t, "bad.jsonl", `{"name":"no id"}`)
	_, err = LoadSplitWorklist(bad)
	require.Error(t, err, "a row without a credit_name_id would silently shrink the exclusion list")
}

func TestRunRequiresInputs(t *testing.T) {
	_, err := Run(t.Context(), Opts{})
	require.Error(t, err)
	_, err = Run(t.Context(), Opts{DSN: "x"})
	require.Error(t, err)
}
