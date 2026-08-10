package service

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"api/internal/platform/trust/model"

	"gorm.io/gorm"
)

const auditAdvisoryKey int64 = 0x74727561

type AuditEntry struct {
	ActorID     *int64
	Action      string
	Site        *string
	SubjectKind *string
	SubjectID   *string
	ReasonCode  *string
	PolicyRef   *string
}

type canonicalAudit struct {
	ActorID     *int64  `json:"actor_id"`
	Action      string  `json:"action"`
	Site        *string `json:"site"`
	SubjectKind *string `json:"subject_kind"`
	SubjectID   *string `json:"subject_id"`
	ReasonCode  *string `json:"reason_code"`
	PolicyRef   *string `json:"policy_ref"`
	CreatedAt   string  `json:"created_at"`
}

func AppendAudit(tx *gorm.DB, e AuditEntry) error {
	if err := tx.Exec(`SELECT pg_advisory_xact_lock(?)`, auditAdvisoryKey).Error; err != nil {
		return fmt.Errorf("audit lock: %w", err)
	}

	prev := make([]byte, 32)
	var last model.TrustAuditLog
	err := tx.Order("id DESC").Limit(1).Take(&last).Error
	if err == nil {
		prev = last.Hash
	} else if err != gorm.ErrRecordNotFound {
		return fmt.Errorf("audit head: %w", err)
	}

	createdAt := time.Now().UTC()
	canonical, err := json.Marshal(canonicalAudit{
		ActorID: e.ActorID, Action: e.Action, Site: e.Site,
		SubjectKind: e.SubjectKind, SubjectID: e.SubjectID,
		ReasonCode: e.ReasonCode, PolicyRef: e.PolicyRef,
		CreatedAt: createdAt.Format(time.RFC3339Nano),
	})
	if err != nil {
		return fmt.Errorf("audit canonical: %w", err)
	}

	h := sha256.New()
	h.Write(prev)
	h.Write(canonical)
	sum := h.Sum(nil)

	row := model.TrustAuditLog{
		ActorID: e.ActorID, Action: e.Action, Site: e.Site,
		SubjectKind: e.SubjectKind, SubjectID: e.SubjectID,
		ReasonCode: e.ReasonCode, PolicyRef: e.PolicyRef,
		PrevHash: prev, Hash: sum, CreatedAt: createdAt,
	}
	if err := tx.Create(&row).Error; err != nil {
		return fmt.Errorf("audit insert: %w", err)
	}
	return nil
}

func strptr(s string) *string { return &s }
