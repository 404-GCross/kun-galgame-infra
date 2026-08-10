package dto

type PublicLabelGraph struct {
	Nodes []PublicLabelGraphNode `json:"nodes"`
	Edges []PublicLabelGraphEdge `json:"edges"`
}

type PublicLabelGraphNode struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	LogoHash  string `json:"logo_hash" doc:"brand logo content hash in the image service; \"\" = this label has no logo"`
	WorkCount int    `json:"work_count" doc:"nsfw-aware work count, identical to labels/{id}.work_count for the same nsfw setting"`
}

type PublicLabelGraphEdge struct {
	From     int64  `json:"from"`
	To       int64  `json:"to"`
	Relation string `json:"relation" enum:"parent,imprint,spawned,succeeded_by" doc:"reads \"to is the relation of from\"; the mirrored inverses (subsidiary|imprint_of|origin|formerly) are implied by reading the edge backwards and are never emitted"`
}
