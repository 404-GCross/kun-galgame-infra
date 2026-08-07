package labelrelations

import "api/internal/platform/catalog/model"

// ⚠️⚠️ THE DIRECTION FLIP POINT — READ BEFORE CHANGING ANYTHING BELOW ⚠️⚠️
//
// vndbRelation is the ONE place that turns a VNDB producers_relations row into
// a catalog_label_relation row. It encodes BOTH halves of the contract:
//
//  1. the code vocabulary (par/sub/imp/ipa/spa/ori/new/old → the eight
//     LabelRelation* constants), and
//
//  2. the DIRECTION READING, which is
//
//     dump row (id, pid, rel)  ⇒  (label_id = label(id), other_label_id = label(pid), relation = rel)
//
//     i.e. "the producer named by pid is <rel> of the producer named by id".
//
// The direction is PROVISIONAL: VNDB's dump ships the graph mirrored and its
// schema notes do not state which endpoint the relation word describes, so the
// reading above is a hypothesis to be pinned EMPIRICALLY at ops time against a
// pair whose real-world structure is unambiguous — Key and VisualArt's (Key is
// a brand under VisualArt's, so the mirrored pair must come out as
// "VisualArt's is the PARENT of Key" and "Key is the SUBSIDIARY of VisualArt's").
//
// If that check comes out inverted, THIS FILE IS THE ONLY EDIT: swap the four
// inverse pairs in the table below (par↔sub, imp↔ipa, spa↔ori, new↔old) and
// re-run the builder with --apply. Nothing else in the job, the model or the
// read face encodes a direction — the graph is stored mirrored precisely so no
// reader ever inverts anything. Do NOT "fix" a suspected inversion by flipping
// the SQL, the loader's column order, or the read face's ORDER BY.
var vndbRelation = map[string]int16{
	"par": model.LabelRelationParent,
	"sub": model.LabelRelationSubsidiary,
	"imp": model.LabelRelationImprint,
	"ipa": model.LabelRelationImprintOf,
	"spa": model.LabelRelationSpawned,
	"ori": model.LabelRelationOrigin,
	"new": model.LabelRelationSucceededBy,
	"old": model.LabelRelationFormerly,
}
