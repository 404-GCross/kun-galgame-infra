package handler

import (
	"api/internal/platform/catalog/dto"
	"api/internal/platform/editing"
)

// Exported view constructors for the editing-engine wire shapes.
//
// These are thin, behavior-preserving aliases of the unexported view builders
// the S2S edit face uses (edit.go: proposalView / revisionView / amendmentViews
// and the schema field mapping). They exist so a SECOND face — the /internal
// platform proposal bridge (09-open-api-phase2 06b, hosted by galgameapp) — can
// emit responses that are BYTE-IDENTICAL to the S2S face without duplicating the
// shape (doc 23 §5 P10). The bridge is pure Fiber and lives in another package,
// so the shapes must be reachable across the package boundary; exporting is the
// minimal, additive change (edit.go is untouched, its output unchanged).
//
// Do NOT add per-face logic here: these must stay pure projections of the same
// dto shapes, or the byte-for-byte equivalence gate breaks.

// ProposalView renders a proposal to its wire shape (patch only; the detail
// endpoints set EffectivePatch/Amendments on top).
func ProposalView(p *editing.Proposal) dto.EditProposalView { return proposalView(p) }

// RevisionView renders a revision-log row to its wire shape.
func RevisionView(r *editing.Revision) dto.EditRevisionView { return revisionView(r) }

// AmendmentViews renders a proposal's amendments (seq order) to their wire shape.
func AmendmentViews(items []editing.ProposalAmendment) []dto.EditAmendmentView {
	return amendmentViews(items)
}

// SchemaFieldViews maps engine field projections to their wire shape — the same
// mapping the S2S schema endpoint performs inline (edit.go: (*EditServer).schema).
func SchemaFieldViews(fields []editing.FieldProjection) []dto.EditSchemaFieldView {
	out := make([]dto.EditSchemaFieldView, 0, len(fields))
	for _, f := range fields {
		out = append(out, dto.EditSchemaFieldView{
			Key: f.Key, Kind: string(f.Kind), DiffHint: f.DiffHint, Deprecated: f.Deprecated,
			Locked: f.Locked, CanPropose: f.CanPropose, CanReview: f.CanReview,
			WouldAutomerge: f.WouldAutomerge,
		})
	}
	return out
}
