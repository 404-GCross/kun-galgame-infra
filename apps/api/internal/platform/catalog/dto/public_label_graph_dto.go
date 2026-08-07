// public_label_graph_dto.go — the label RELATION GRAPH face (wave 188):
// GET /v1/catalog/labels/{id}/relation-graph.
//
// labels/{id}.relations[] answers "what sits one hop from this label", which is
// enough for a text list and not enough for a picture: a consumer standing on
// Key cannot see VisualArt's other brands without walking the one-hop face once
// per neighbour. This face answers the whole question in one call — the
// connected corporate component around a seed label, bounded, with the two
// fields a rendered node needs (logo, work count).
package dto

// PublicLabelGraph is the corporate-structure component around a seed label:
// the labels reachable from it over catalog_label_relation, and the edges
// between them.
//
// Both lists are ALWAYS present. nodes always holds at least the seed (a label
// with no relations yields a one-node, zero-edge graph — the honest answer, not
// a 404), and edges is [] rather than null when there are none.
type PublicLabelGraph struct {
	// Nodes are breadth-first from the seed, which is always nodes[0]: nearest
	// labels come first, so a consumer that truncates the list keeps the part
	// of the family closest to the label being viewed.
	Nodes []PublicLabelGraphNode `json:"nodes"`
	Edges []PublicLabelGraphEdge `json:"edges"`
}

// PublicLabelGraphNode is one label in the graph, carrying exactly what a
// rendered node needs — the identity, the brand logo and the size of the
// catalogue behind it.
type PublicLabelGraphNode struct {
	ID int64 `json:"id"`
	// Name is the label's display_name — the same string labels/{id} returns as
	// display_name, spelled `name` here because a graph node's label IS its
	// name (the relations[] convention).
	Name string `json:"name"`
	// LogoHash is the brand logo's content hash in the image service (wave
	// 170), the same currency and the same projection as
	// PublicLabel.LogoHash. ALWAYS present (never omitempty): "" is the real
	// answer "this label has no logo", and a missing key would be
	// indistinguishable from a consumer's parse failure.
	LogoHash string `json:"logo_hash" doc:"brand logo content hash in the image service; \"\" = this label has no logo"`
	// WorkCount is NSFW-AWARE and comes from the browse lane's own aggregate,
	// so a node's number is byte-identical to the one labels/{id} and the
	// labels list report for the same caller — three faces, one count.
	WorkCount int `json:"work_count" doc:"nsfw-aware work count, identical to labels/{id}.work_count for the same nsfw setting"`
}

// PublicLabelGraphEdge is one corporate-structure fact, oriented.
//
// SEMANTICS — an edge reads "`to` is the `relation` of `from`", the same
// reading relations[].relation already has (there, `from` is the label being
// viewed). So {from: Key, to: VisualArt's, relation: "parent"} means
// "VisualArt's is the parent of Key".
//
// The underlying graph is stored MIRRORED (every fact twice, once per direction
// with the inverse code), but this face emits each fact ONCE: only the rows
// whose relation is on the canonical side of its inverse pair — parent,
// imprint, spawned, succeeded_by — are rendered, and the four even inverses
// (subsidiary, imprint_of, origin, formerly) are implied by reading the same
// edge backwards. A consumer that wants "subsidiaries of X" collects the edges
// whose `to` is X and whose relation is parent.
type PublicLabelGraphEdge struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
	// Relation is one of the four canonical spellings only — the mirrored
	// inverses never appear on this face.
	Relation string `json:"relation" enum:"parent,imprint,spawned,succeeded_by" doc:"reads \"to is the relation of from\"; the mirrored inverses (subsidiary|imprint_of|origin|formerly) are implied by reading the edge backwards and are never emitted"`
}
