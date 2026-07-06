package repository

import (
	"context"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// ProposalRepository reads merge proposals; state transitions happen inside
// the MergeService transactions.
type ProposalRepository struct {
	db *gorm.DB
}

func NewProposalRepository(db *gorm.DB) *ProposalRepository {
	return &ProposalRepository{db: db}
}

func (r *ProposalRepository) Get(ctx context.Context, id int64) (*model.CatalogMergeProposal, error) {
	var row model.CatalogMergeProposal
	err := r.db.WithContext(ctx).First(&row, id).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// LockProposal loads a proposal FOR UPDATE inside a transaction — every
// state transition serializes on the row.
func LockProposal(tx *gorm.DB, id int64) (*model.CatalogMergeProposal, error) {
	var row model.CatalogMergeProposal
	err := tx.Raw(`SELECT * FROM catalog_merge_proposal WHERE id = ? FOR UPDATE`, id).Scan(&row).Error
	if err != nil {
		return nil, err
	}
	if row.ID == 0 {
		return nil, nil
	}
	return &row, nil
}
