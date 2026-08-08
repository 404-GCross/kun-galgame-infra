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
// The direction was PINNED EMPIRICALLY on 2026-08-08 and is no longer
// provisional. The pair this banner originally nominated — Key / VisualArt's —
// turned out to be unusable: VISUAL ARTS (p993) carried no label anchor, so the
// check could never run, and the graph shipped for two waves with an unverified
// reading. It was confirmed instead against three both-anchored pairs whose
// real-world structure is unambiguous, all of which came out right way up:
//
//	Hudson Soft  --parent-->  コナミ         (Konami acquired Hudson Soft)
//	KCET         --parent-->  コナミ         (Konami Computer Entertainment Tokyo)
//	Genius Yaoi Studio --parent--> Genius Inc.
//
// i.e. a row selected WHERE label_id = X names X's parent in other_label_id,
// which is what the reading above says. (The lesson generalises: an ops check
// nominating a specific pair must first assert that pair is reachable, or it
// silently never runs.)
//
// If a future dump ever comes out inverted, THIS FILE IS THE ONLY EDIT: swap the four
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
