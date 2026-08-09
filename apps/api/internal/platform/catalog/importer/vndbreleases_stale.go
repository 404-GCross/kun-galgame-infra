package importer

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"api/internal/platform/catalog/model"
)

// staleAnchorRow is one rid whose EXACT anchor sits on a release under a work
// that upstream no longer associates the rid with. Under the wave-202 identity
// split these no longer block anything (the new row simply takes a probable
// ref), but "no longer fatal" must not become "invisible": each one is a real
// disagreement between our anchor and upstream, and only a human can decide
// whether the anchor moved, upstream re-pointed, or the two works are duplicates.
type staleAnchorRow struct {
	rid             string
	gateWorkID      int64
	gateWorkVID     string
	holderReleaseID int64
	holderWorkID    int64
	holderRetired   bool
}

// exportStaleAnchors writes the review worklist as TSV with trailing empty
// decision/notes columns — the egdlsite_ambiguous.go conflict-export convention.
// The holder's own work vid and the rid's full upstream vid list are resolved
// here (the set is small) so a reviewer can adjudicate from the file alone.
func (im *Importer) exportStaleAnchors(rows []staleAnchorRow, path string) error {
	holderVIDs, err := im.loadWorkVNDBIDs(uniqueWorkIDs(rows))
	if err != nil {
		return err
	}
	upstream, err := im.loadReleaseUpstreamVIDs(uniqueRIDs(rows))
	if err != nil {
		return err
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	bw := bufio.NewWriter(f)
	fmt.Fprintln(bw, strings.Join([]string{
		"rid", "gate_work_id", "gate_work_vid", "holder_release_id", "holder_work_id",
		"holder_work_vid", "holder_retired", "upstream_vids", "decision", "notes",
	}, "\t"))
	for _, r := range rows {
		fmt.Fprintln(bw, strings.Join([]string{
			r.rid,
			strconv.FormatInt(r.gateWorkID, 10),
			r.gateWorkVID,
			strconv.FormatInt(r.holderReleaseID, 10),
			strconv.FormatInt(r.holderWorkID, 10),
			holderVIDs[r.holderWorkID],
			strconv.FormatBool(r.holderRetired),
			strings.Join(upstream[r.rid], "|"),
			"", "",
		}, "\t"))
	}
	if err := bw.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s: %d stale release anchor rows\n", path, len(rows))
	return nil
}

// loadWorkVNDBIDs maps work id → its (lowest, for determinism) exact vndb work
// anchor. A work with no vndb anchor simply has no entry.
func (im *Importer) loadWorkVNDBIDs(workIDs []int64) (map[int64]string, error) {
	out := map[int64]string{}
	if len(workIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		EntityID   int64  `gorm:"column:entity_id"`
		ExternalID string `gorm:"column:external_id"`
	}
	if err := im.catalog.Raw(`
		SELECT entity_id, min(external_id) AS external_id
		FROM catalog_external_ref
		WHERE entity_type = ? AND source_id = ? AND link_kind = ? AND entity_id IN ?
		GROUP BY entity_id`,
		model.EntityTypeWork, vndbSource, model.LinkKindExact, workIDs).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.EntityID] = r.ExternalID
	}
	return out, nil
}

// loadReleaseUpstreamVIDs maps r-id → every vid upstream currently associates it
// with (sorted). One vid means upstream is unambiguous and the disagreement is
// purely about WHICH work should hold the anchor.
func (im *Importer) loadReleaseUpstreamVIDs(rids []string) (map[string][]string, error) {
	out := map[string][]string{}
	if len(rids) == 0 {
		return out, nil
	}
	var rows []struct {
		ID  string `gorm:"column:id"`
		VID string `gorm:"column:vid"`
	}
	if err := im.catalog.Raw(`SELECT id, vid FROM src_vndb.releases_vn WHERE id IN ? ORDER BY id, vid`, rids).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ID] = append(out[r.ID], r.VID)
	}
	return out, nil
}

func uniqueWorkIDs(rows []staleAnchorRow) []int64 {
	seen := map[int64]bool{}
	out := make([]int64, 0, len(rows))
	for _, r := range rows {
		if r.holderWorkID == 0 || seen[r.holderWorkID] {
			continue
		}
		seen[r.holderWorkID] = true
		out = append(out, r.holderWorkID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func uniqueRIDs(rows []staleAnchorRow) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if seen[r.rid] {
			continue
		}
		seen[r.rid] = true
		out = append(out, r.rid)
	}
	sort.Strings(out)
	return out
}
