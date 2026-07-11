package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strconv"
	"time"

	"api/internal/platform/community/model"
	"api/internal/platform/community/repository"

	"gorm.io/gorm"
)

// Trust disposition action codes (the wire enum from trust's callback body —
// see trust model constants.go; NEVER renumber). Only 0/1/2 are enforceable on a
// community_post; 3/4/5 are notification/account actions this service does not
// own (step 03 ruling 7).
const (
	trustActionNone   int16 = 0
	trustActionHide   int16 = 1
	trustActionRemove int16 = 2
)

// callbackWindow is the ±window a callback timestamp may fall within (ruling 6).
const callbackWindow = 5 * time.Minute

// TrustCallback is the enforcement callback body posted by the trust dispatch
// worker (must match trust's callbackBody). disposition_id is the natural
// idempotency key — the enforcement itself is idempotent, so no dedup table.
type TrustCallback struct {
	DispositionID int64  `json:"disposition_id"`
	SubjectKind   string `json:"subject_kind"`
	SubjectID     string `json:"subject_id"`
	Action        int16  `json:"action"`
	ReasonCode    string `json:"reason_code"`
}

// CallbackResult distinguishes an enforced callback from one whose action this
// service cannot execute for a community_post (the handler logs + 200s the
// latter so the trust worker does not dead-letter it).
type CallbackResult int

const (
	CallbackEnforced CallbackResult = iota
	CallbackUnsupported
)

// CallbackService enforces trust dispositions on community posts.
type CallbackService struct{ db *gorm.DB }

// NewCallbackService builds the enforcement service.
func NewCallbackService(db *gorm.DB) *CallbackService { return &CallbackService{db: db} }

// Handle enforces one disposition on the subject post and closes any still-open
// local review item for it (step 03 ruling 7). Action mapping:
//
//	1 hide   → mod-hide a visible post
//	2 remove → tombstone (from any non-deleted state)
//	0 none   → restore ONLY a mod-hidden/held post (an author-deleted post is
//	           never resurrected)
//	3/4/5    → unsupported for community_post (CallbackUnsupported)
//
// Every branch is idempotent (a replayed callback re-applies the same terminal
// state = no-op).
func (s *CallbackService) Handle(ctx context.Context, cb TrustCallback) (CallbackResult, error) {
	if cb.Action != trustActionNone && cb.Action != trustActionHide && cb.Action != trustActionRemove {
		return CallbackUnsupported, nil
	}
	postID, err := strconv.ParseInt(cb.SubjectID, 10, 64)
	if err != nil {
		slog.Warn("trust callback: non-numeric subject_id", "subject_id", cb.SubjectID, "disposition_id", cb.DispositionID)
		return CallbackEnforced, nil // nothing to enforce; a retry would not help
	}

	return CallbackEnforced, s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		post, err := repository.GetPostTx(tx, postID)
		if err != nil {
			return err
		}
		if post == nil {
			return nil // subject post gone → nothing to enforce (idempotent)
		}

		closeStatus := model.ReviewStatusRejected // hide/remove: content not kept
		switch cb.Action {
		case trustActionHide:
			if post.Status == model.PostStatusVisible {
				if err := repository.SetPostStatusTx(tx, postID, model.PostStatusHidden); err != nil {
					return err
				}
			}
		case trustActionRemove:
			if post.Status != model.PostStatusDeleted {
				if err := repository.SetPostStatusTx(tx, postID, model.PostStatusDeleted); err != nil {
					return err
				}
			}
		case trustActionNone:
			closeStatus = model.ReviewStatusApproved // content kept
			// Restore only a mod-hidden/held post; an author-deleted (tombstoned)
			// post must never be resurrected (ruling 7).
			if post.Status == model.PostStatusHidden {
				if err := repository.SetPostStatusTx(tx, postID, model.PostStatusVisible); err != nil {
					return err
				}
			}
		}
		return repository.CloseReviewItemsForPostTx(tx, postID, closeStatus)
	})
}

// VerifyTrustSignature checks the trust HMAC on an inbound callback: the header
// X-Trust-Signature must equal hex(HMAC-SHA256(secret, timestamp + "." + body))
// and the timestamp must fall within ±callbackWindow of now (ruling 6). An empty
// secret (unconfigured) fails closed — every callback is rejected.
func VerifyTrustSignature(secret, timestamp, signature string, body []byte, now time.Time) bool {
	if secret == "" || timestamp == "" || signature == "" {
		return false
	}
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	skew := now.Sub(time.Unix(ts, 0))
	if skew < -callbackWindow || skew > callbackWindow {
		return false
	}
	expected := signTrustPayload(secret, timestamp, body)
	return hmac.Equal([]byte(expected), []byte(signature))
}

// signTrustPayload mirrors the trust service's SignPayload.
func signTrustPayload(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
