package service

import (
	"strings"

	"api/internal/platform/catalog/model"
)

const claimedSQL = "(w.site IS NOT NULL AND w.site <> '' AND w.product_work_id IS NOT NULL)"

func claimStateWhere(states []string) (string, []any) {
	if len(states) == 0 {
		return "", nil
	}
	ors := make([]string, 0, len(states))
	args := make([]any, 0, len(states)+1)
	for _, st := range states {
		switch st {
		case model.ClaimStateKeyNone:
			ors = append(ors, "NOT "+claimedSQL)
		case model.ClaimStateKeyLive:
			ors = append(ors, claimedSQL+" AND (w.claim_state IS NULL OR w.claim_state = ?)")
			args = append(args, model.ClaimStateLive)
		case model.ClaimStateKeyDraft:
			ors = append(ors, claimedSQL+" AND w.claim_state = ?")
			args = append(args, model.ClaimStateDraft)
		case model.ClaimStateKeyPending:
			ors = append(ors, claimedSQL+" AND w.claim_state = ?")
			args = append(args, model.ClaimStatePending)
		case model.ClaimStateKeyDeclined:
			ors = append(ors, claimedSQL+" AND w.claim_state = ?")
			args = append(args, model.ClaimStateDeclined)
		case model.ClaimStateKeyHidden:
			ors = append(ors, claimedSQL+" AND w.claim_state IS NOT NULL AND w.claim_state NOT IN (?, ?, ?, ?)")
			args = append(args, model.ClaimStateLive, model.ClaimStateDraft, model.ClaimStatePending, model.ClaimStateDeclined)
		default:
			ors = append(ors, "false")
		}
	}
	return "((" + strings.Join(ors, ") OR (") + "))", args
}
