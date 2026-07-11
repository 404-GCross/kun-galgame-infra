package service

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"api/internal/platform/trust/model"

	"gorm.io/gorm"
)

// auditAdvisoryKey serializes concurrent audit-chain appends across transactions
// so the hash chain stays linear ("trua" ≈ trust-audit). It MUST differ from the
// dbtest suite advisory-lock key (session-held for a package's whole test run),
// or an in-test audit append would block forever on the same lock number
// (advisory locks share one number space across session/xact variants).
const auditAdvisoryKey int64 = 0x74727561

// AuditEntry is the caller-supplied content of one audit row (everything except
// the chain links, which AppendAudit computes). PII stays out — refs and codes
// only (章程 ruling 10).
type AuditEntry struct {
	ActorID     *int64
	Action      string
	Site        *string
	SubjectKind *string
	SubjectID   *string
	ReasonCode  *string
	PolicyRef   *string
}

// canonicalAudit is the deterministic serialization the hash covers. Field
// order is fixed by struct declaration order (encoding/json preserves it), so
// the digest is reproducible.
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

// AppendAudit writes one row onto the append-only hash chain inside tx. It takes
// a transaction advisory lock (auto-released at commit/rollback) so two
// concurrent decision transactions cannot fork the chain, reads the current
// head hash, and links the new row: hash = SHA256(prev_hash || canonical-row).
// The first row's prev_hash is 32 zero bytes.
func AppendAudit(tx *gorm.DB, e AuditEntry) error {
	if err := tx.Exec(`SELECT pg_advisory_xact_lock(?)`, auditAdvisoryKey).Error; err != nil {
		return fmt.Errorf("audit lock: %w", err)
	}

	prev := make([]byte, 32) // genesis prev_hash = 32 zero bytes
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

// strptr is a small helper for building AuditEntry ref fields.
func strptr(s string) *string { return &s }
