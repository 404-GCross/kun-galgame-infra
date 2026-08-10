package service

import (
	"time"

	"api/internal/platform/community/model"
	"api/internal/platform/community/repository"
	"api/internal/platform/community/sanitize"
)

const (
	tl0MaxLinks         = 2
	tl0MaxImages        = 1
	tl0MaxMentions      = 2
	tl0MaxTopicsPerDay  = 3
	tl0MaxRepliesPerDay = 10
	sandboxWindow       = 24 * time.Hour
)

func isSandboxed(level int16) bool { return level < model.TrustLevelBasic }

func checkContentSandbox(level int16, cooked sanitize.Cooked) error {
	if !isSandboxed(level) {
		return nil
	}
	switch {
	case cooked.Links > tl0MaxLinks:
		return &SandboxError{Reason: "too many links"}
	case cooked.Images > tl0MaxImages:
		return &SandboxError{Reason: "too many images"}
	case cooked.Mentions > tl0MaxMentions:
		return &SandboxError{Reason: "too many mentions"}
	}
	return nil
}

func trustLevel(trusts *repository.TrustRepository, userID int64) (int16, error) {
	t, err := trusts.GetTrust(userID)
	if err != nil {
		return 0, err
	}
	if t == nil {
		return model.TrustLevelNew, nil
	}
	return t.Level, nil
}
