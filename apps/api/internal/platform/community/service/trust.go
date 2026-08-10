package service

import (
	"context"

	"api/internal/platform/community/model"
	"api/internal/platform/community/repository"

	"gorm.io/gorm"
)

const (
	tl1TopicsEntered    int32 = 5
	tl1PostsRead        int32 = 30
	tl1ReadTimeS        int32 = 600
	tl2DaysVisited      int32 = 15
	tl2PostsRead        int32 = 100
	tl2Likes            int32 = 1
	tl3WindowActiveDays int32 = 50
)

type TrustService struct {
	db     *gorm.DB
	trusts *repository.TrustRepository
}

func NewTrustService(db *gorm.DB) *TrustService {
	return &TrustService{db: db, trusts: repository.NewTrustRepository(db)}
}

type ActivityReceipt struct {
	UserID           int64
	TopicsEntered    int32
	PostsRead        int32
	ReadTimeS        int32
	DaysVisited      int32
	WindowActiveDays *int32
}

func (s *TrustService) RecordActivity(ctx context.Context, r ActivityReceipt) (*model.CommunityTrust, error) {
	var out model.CommunityTrust
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := repository.GetOrCreateTrustTx(tx, r.UserID); err != nil {
			return err
		}
		if err := repository.ApplyMeteringTx(tx, r.UserID, repository.MeteringDelta{
			TopicsEntered: r.TopicsEntered, PostsRead: r.PostsRead, ReadTimeS: r.ReadTimeS, DaysVisited: r.DaysVisited,
		}); err != nil {
			return err
		}
		trust, err := repository.GetTrustTx(tx, r.UserID)
		if err != nil {
			return err
		}
		if newLevel := evaluateLevel(trust, r.WindowActiveDays); newLevel != trust.Level {
			if err := repository.SetLevelTx(tx, r.UserID, newLevel); err != nil {
				return err
			}
			trust.Level = newLevel
		}
		out = *trust
		return nil
	})
	return &out, err
}

func (s *TrustService) SetBoost(ctx context.Context, userID int64, boost int16) (*model.CommunityTrust, error) {
	var out model.CommunityTrust
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := repository.GetOrCreateTrustTx(tx, userID); err != nil {
			return err
		}
		if err := repository.SetBoostTx(tx, userID, boost); err != nil {
			return err
		}
		if boost == model.GrantedBoostStaff {
			if err := repository.ClearHoldsTx(tx, userID); err != nil {
				return err
			}
		}
		trust, err := repository.GetTrustTx(tx, userID)
		if err != nil {
			return err
		}
		if floor := boostFloor(&boost); trust.Level < floor {
			if err := repository.SetLevelTx(tx, userID, floor); err != nil {
				return err
			}
			trust.Level = floor
		}
		out = *trust
		return nil
	})
	return &out, err
}

func evaluateLevel(t *model.CommunityTrust, window *int32) int16 {
	if t.Level >= model.TrustLevelLeader {
		return t.Level
	}
	earned := earnedLevel(t)
	newLevel := earned
	if t.Level == model.TrustLevelRegular {
		newLevel = model.TrustLevelRegular
	}
	if window != nil {
		if earned >= model.TrustLevelMember && *window >= tl3WindowActiveDays {
			newLevel = model.TrustLevelRegular
		} else if t.Level == model.TrustLevelRegular && *window < tl3WindowActiveDays {
			newLevel = earned
		}
	}
	if newLevel < earned {
		newLevel = earned
	}
	if floor := boostFloor(t.GrantedBoost); newLevel < floor {
		newLevel = floor
	}
	return newLevel
}

func earnedLevel(t *model.CommunityTrust) int16 {
	switch {
	case meetsTL2(t):
		return model.TrustLevelMember
	case meetsTL1(t):
		return model.TrustLevelBasic
	default:
		return model.TrustLevelNew
	}
}

func meetsTL1(t *model.CommunityTrust) bool {
	return nz(t.TopicsEntered) >= tl1TopicsEntered &&
		nz(t.PostsRead) >= tl1PostsRead &&
		nz(t.ReadTimeS) >= tl1ReadTimeS
}

func meetsTL2(t *model.CommunityTrust) bool {
	return meetsTL1(t) &&
		nz(t.DaysVisited) >= tl2DaysVisited &&
		nz(t.PostsRead) >= tl2PostsRead &&
		nz(t.LikesGiven) >= tl2Likes &&
		nz(t.LikesReceived) >= tl2Likes
}

func boostFloor(gb *int16) int16 {
	if gb == nil {
		return model.TrustLevelNew
	}
	switch *gb {
	case model.GrantedBoostVeteran, model.GrantedBoostCreator:
		return model.TrustLevelBasic
	case model.GrantedBoostStaff:
		return model.TrustLevelRegular
	default:
		return model.TrustLevelNew
	}
}

func nz(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}
