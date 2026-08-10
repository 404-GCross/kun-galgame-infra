package handler

import (
	"api/internal/platform/catalog/dto"
	"api/internal/platform/editing"
)

func ProposalView(p *editing.Proposal) dto.EditProposalView { return proposalView(p) }

func RevisionView(r *editing.Revision) dto.EditRevisionView { return revisionView(r) }

func AmendmentViews(items []editing.ProposalAmendment) []dto.EditAmendmentView {
	return amendmentViews(items)
}

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
