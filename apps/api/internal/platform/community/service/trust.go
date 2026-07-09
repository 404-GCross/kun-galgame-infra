package service

import (
	"context"

	"api/internal/platform/community/model"
	"api/internal/platform/community/repository"

	"gorm.io/gorm"
)

// TL promotion thresholds — doc 11 §6 numeric starter set (Discourse-derived),
// centralized here so they stay tunable. Values are read against the trust
// row's cumulative metering counters (NULL treated as 0).
const (
	tl1TopicsEntered int32 = 5   // TL0→1: entered 5 topics
	tl1PostsRead     int32 = 30  // …read 30 posts
	tl1ReadTimeS     int32 = 600 // …read for 10 minutes
	tl2DaysVisited   int32 = 15  // TL1→2: visited 15 days
	tl2PostsRead     int32 = 100 // …read 100 posts
	tl2Likes         int32 = 1   // …gave ≥1 and received ≥1 like
	// TL2→3 is a rolling-100-day activity gate that community does NOT store
	// (zero-schema): the consuming site reports "days active in the last 100" on
	// the activity receipt, and this is the bar (Discourse's "visited ≥50 of the
	// last 100 days"). TL3 is the only demotable level — a receipt whose window
	// falls below the bar drops the user back to their earned cumulative level.
	tl3WindowActiveDays int32 = 50
)

// TrustService owns TL metering, promotion/demotion, and starter boosts. It is
// the trust ENGINE (doc 11 §6). It never reverse-connects to the main DB: the
// starter-boost decision (account age / creator / staff) is made at the
// consuming site, which holds the IdP claims, and declared to community via
// SetBoost — community only records and applies it (clean tenant boundary).
type TrustService struct {
	db     *gorm.DB
	trusts *repository.TrustRepository
}

func NewTrustService(db *gorm.DB) *TrustService {
	return &TrustService{db: db, trusts: repository.NewTrustRepository(db)}
}

// ActivityReceipt is one batch of reading-behavior activity a site BFF reports
// for a user. Likes are NOT here — they are counted in place by the reaction
// flow. WindowActiveDays is the site-computed "days active in the last 100";
// when supplied it drives the TL3 promote/demote decision, when nil TL3 is left
// untouched (promotion for TL0-2 is purely cumulative and monotonic).
type ActivityReceipt struct {
	UserID           int64
	TopicsEntered    int32
	PostsRead        int32
	ReadTimeS        int32
	DaysVisited      int32
	WindowActiveDays *int32
}

// RecordActivity applies a receipt's deltas and re-evaluates the user's level in
// one transaction (no cron — evaluated in place at letmoe's single-site scale;
// a materialized job is a scale-trigger). Idempotent at the level layer:
// re-evaluating yields the same level, so a stale receipt never over-promotes.
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

// SetBoost records a starter-boost declaration from the consuming site and
// floors the user's level accordingly (veteran/creator → TL1, staff → TL3). A
// boost is a floor, never a demotion: a user already above the floor is
// unaffected. boost must be a GrantedBoost* value.
func (s *TrustService) SetBoost(ctx context.Context, userID int64, boost int16) (*model.CommunityTrust, error) {
	var out model.CommunityTrust
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := repository.GetOrCreateTrustTx(tx, userID); err != nil {
			return err
		}
		if err := repository.SetBoostTx(tx, userID, boost); err != nil {
			return err
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

// evaluateLevel is the pure promotion/demotion function: effective level =
// max(earned cumulative level, TL3-from-window, boost floor), with TL3
// preserved across receipts that carry no window signal, and TL4 (manual-only)
// never touched by the engine.
func evaluateLevel(t *model.CommunityTrust, window *int32) int16 {
	if t.Level >= model.TrustLevelLeader {
		return t.Level // TL4 is human-granted only
	}
	earned := earnedLevel(t)
	newLevel := earned
	if t.Level == model.TrustLevelRegular {
		newLevel = model.TrustLevelRegular // keep TL3 unless a window signal demotes it
	}
	if window != nil {
		if earned >= model.TrustLevelMember && *window >= tl3WindowActiveDays {
			newLevel = model.TrustLevelRegular
		} else if t.Level == model.TrustLevelRegular && *window < tl3WindowActiveDays {
			newLevel = earned // demote to the earned cumulative level
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

// earnedLevel is the cumulative-counter level (0/1/2), monotonic — it never
// drops as counters only grow.
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

// boostFloor maps a granted boost to the minimum level it guarantees.
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

// nz dereferences a nullable counter, treating NULL as 0.
func nz(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}
