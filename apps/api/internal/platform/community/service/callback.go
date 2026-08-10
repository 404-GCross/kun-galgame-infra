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

const (
	trustActionNone   int16 = 0
	trustActionHide   int16 = 1
	trustActionRemove int16 = 2
)

const callbackWindow = 5 * time.Minute

type TrustCallback struct {
	DispositionID int64  `json:"disposition_id"`
	SubjectKind   string `json:"subject_kind"`
	SubjectID     string `json:"subject_id"`
	Action        int16  `json:"action"`
	ReasonCode    string `json:"reason_code"`
}

type CallbackResult int

const (
	CallbackEnforced CallbackResult = iota
	CallbackUnsupported
)

type CallbackService struct{ db *gorm.DB }

func NewCallbackService(db *gorm.DB) *CallbackService { return &CallbackService{db: db} }

func (s *CallbackService) Handle(ctx context.Context, cb TrustCallback) (CallbackResult, error) {
	if cb.Action != trustActionNone && cb.Action != trustActionHide && cb.Action != trustActionRemove {
		return CallbackUnsupported, nil
	}
	postID, err := strconv.ParseInt(cb.SubjectID, 10, 64)
	if err != nil {
		slog.Warn("trust callback: non-numeric subject_id", "subject_id", cb.SubjectID, "disposition_id", cb.DispositionID)
		return CallbackEnforced, nil
	}

	return CallbackEnforced, s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		post, err := repository.GetPostTx(tx, postID)
		if err != nil {
			return err
		}
		if post == nil {
			return nil
		}

		closeStatus := model.ReviewStatusRejected
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
			closeStatus = model.ReviewStatusApproved
			if post.Status == model.PostStatusHidden {
				if err := repository.SetPostStatusTx(tx, postID, model.PostStatusVisible); err != nil {
					return err
				}
			}
		}
		return repository.CloseReviewItemsForPostTx(tx, postID, closeStatus)
	})
}

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

func signTrustPayload(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
