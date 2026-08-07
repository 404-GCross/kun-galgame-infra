// public_label_graph.go — the label relation GRAPH projection (wave 188).
//
// Wave 186 gave labels/{id} a one-hop relations[] list. This file answers the
// question that list cannot: the whole corporate family around a label, in one
// call, with enough per-node material (logo, work count) to draw it.
package service

import (
	"context"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/model"
)

const (
	// labelGraphMaxDepth is how many expansion rounds run from the seed, so a
	// node in the answer is at most this many hops away. Four reaches
	// grandparent → parent → sibling → sibling's imprint, which is the whole
	// shape of every real publisher family we hold; beyond it the picture stops
	// being about the label the caller is standing on.
	labelGraphMaxDepth = 4
	// labelGraphMaxNodes caps the answer. The cap is enforced breadth-first, so
	// what survives a truncation is the neighbourhood NEAREST the seed rather
	// than an arbitrary slice of the component.
	labelGraphMaxNodes = 60
)

// labelGraphNodeRow is one label as both the BFS frontier and the wire need it.
type labelGraphNodeRow struct {
	ID       int64  `gorm:"column:id"`
	Name     string `gorm:"column:display_name"`
	LogoHash string `gorm:"column:logo_hash"`
}

// LabelRelationGraph projects the corporate-structure component around a seed
// label (GET /v1/catalog/labels/{id}/relation-graph).
//
// Traversal is a breadth-first walk over catalog_label_relation from the seed,
// with a visited set — the graph contains cycles by construction (it is stored
// mirrored, and a parent/subsidiary loop is a legitimate upstream fact), so a
// naive walk would not terminate. Soft-deleted labels are excluded at the join:
// a merged-away label must not surface as a structural fact, exactly as
// labelRelations already rules for the one-hop face.
//
// found=false means the seed label does not exist (or is soft-deleted); the
// handler turns that into the same 404/301 the labels/{id} lane serves. A seed
// with no edges is NOT a miss — it is a one-node graph.
func (s *PublicService) LabelRelationGraph(ctx context.Context, id int64, nsfw bool) (dto.PublicLabelGraph, bool, error) {
	var seed labelGraphNodeRow
	if err := s.db.WithContext(ctx).Raw(`
		SELECT id, display_name, logo_hash FROM catalog_label
		WHERE id = ? AND deleted_at IS NULL`, id).Scan(&seed).Error; err != nil {
		return dto.PublicLabelGraph{}, false, err
	}
	if seed.ID == 0 {
		return dto.PublicLabelGraph{}, false, nil
	}

	nodes, err := s.labelGraphNodes(ctx, seed)
	if err != nil {
		return dto.PublicLabelGraph{}, false, err
	}
	ids := make([]int64, len(nodes))
	for i, n := range nodes {
		ids[i] = n.ID
	}
	edges, err := s.labelGraphEdges(ctx, ids)
	if err != nil {
		return dto.PublicLabelGraph{}, false, err
	}
	// work_count comes from the browse lane's own aggregate (A2-1e), batched
	// for the whole graph — so a node here and the labels/{id} page it links to
	// can never disagree, and a 60-node graph still costs one count query.
	counts, err := s.workCountsFor(ctx, labelWorkEdge, ids, nsfw)
	if err != nil {
		return dto.PublicLabelGraph{}, false, err
	}

	out := dto.PublicLabelGraph{
		Nodes: make([]dto.PublicLabelGraphNode, len(nodes)),
		Edges: edges,
	}
	for i, n := range nodes {
		out.Nodes[i] = dto.PublicLabelGraphNode{
			ID: n.ID, Name: n.Name, LogoHash: n.LogoHash, WorkCount: counts[n.ID],
		}
	}
	return out, true, nil
}

// labelGraphNodes walks the component breadth-first from the seed and returns
// the admitted labels in visit order (seed first).
//
// The walk follows edges in BOTH stored directions, which the mirroring makes
// free: every fact is filed under both endpoints, so `WHERE label_id IN
// (frontier)` already reaches the neighbours on either side and nothing has to
// be inverted. Which edges are RENDERED is a separate decision, taken in
// labelGraphEdges over the finished node set.
func (s *PublicService) labelGraphNodes(ctx context.Context, seed labelGraphNodeRow) ([]labelGraphNodeRow, error) {
	nodes := []labelGraphNodeRow{seed}
	visited := map[int64]struct{}{seed.ID: {}}
	frontier := []int64{seed.ID}

	for depth := 0; depth < labelGraphMaxDepth && len(frontier) > 0 && len(nodes) < labelGraphMaxNodes; depth++ {
		var rows []labelGraphNodeRow
		// DISTINCT: the same neighbour can be reached from several frontier
		// labels, under several relation codes and from several sources, and a
		// node is a node once. The ORDER BY makes the admission order — and
		// therefore what survives the node cap — deterministic.
		if err := s.db.WithContext(ctx).Raw(`
			SELECT DISTINCT other.id, other.display_name, other.logo_hash
			FROM catalog_label_relation r
			JOIN catalog_label other ON other.id = r.other_label_id AND other.deleted_at IS NULL
			WHERE r.label_id IN ?
			ORDER BY other.display_name, other.id`, frontier).Scan(&rows).Error; err != nil {
			return nil, err
		}
		next := make([]int64, 0, len(rows))
		for _, r := range rows {
			if _, seen := visited[r.ID]; seen {
				continue
			}
			if len(nodes) >= labelGraphMaxNodes {
				break
			}
			visited[r.ID] = struct{}{}
			nodes = append(nodes, r)
			next = append(next, r.ID)
		}
		frontier = next
	}
	return nodes, nil
}

// labelGraphEdgeRelations is the CANONICAL side of each inverse pair. The
// graph is stored mirrored, so emitting every stored row would publish each
// fact twice; emitting only these four publishes it once, and the inverse is
// recovered by reading the edge backwards.
var labelGraphEdgeRelations = []int16{
	model.LabelRelationParent,
	model.LabelRelationImprint,
	model.LabelRelationSpawned,
	model.LabelRelationSucceededBy,
}

// labelGraphEdges renders the canonical edges INTERNAL to the given node set.
//
// It runs over the finished set rather than during the walk on purpose. A fact
// whose canonical row is filed under a node the walk never expanded (a
// depth-limit or node-cap leaf) would otherwise be lost, because only its
// mirror was ever read; querying both endpoints against the final set finds it
// regardless of which side stores the canonical direction.
func (s *PublicService) labelGraphEdges(ctx context.Context, ids []int64) ([]dto.PublicLabelGraphEdge, error) {
	out := []dto.PublicLabelGraphEdge{}
	if len(ids) < 2 {
		return out, nil // a lone node can hold no edge to a node in the set
	}
	var rows []struct {
		From     int64 `gorm:"column:label_id"`
		To       int64 `gorm:"column:other_label_id"`
		Relation int16 `gorm:"column:relation"`
	}
	// DISTINCT folds the multi-source case: two sources asserting the same
	// (from, to, relation) are one edge, and source never reaches this face.
	if err := s.db.WithContext(ctx).Raw(`
		SELECT DISTINCT r.label_id, r.other_label_id, r.relation
		FROM catalog_label_relation r
		WHERE r.label_id IN ? AND r.other_label_id IN ? AND r.relation IN ?
		ORDER BY r.label_id, r.relation, r.other_label_id`,
		ids, ids, labelGraphEdgeRelations).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		key, ok := model.LabelRelationKey[r.Relation]
		if !ok {
			continue // a code with no public spelling is dropped, never numbered
		}
		out = append(out, dto.PublicLabelGraphEdge{From: r.From, To: r.To, Relation: key})
	}
	return out, nil
}
