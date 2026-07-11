package service

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// ReporterWeight is the v0 reputation snapshot for a reporter (章程 ruling 7):
// the report's base weight, and whether the reporter is staff (main-DB role
// moderator/admin/ren) — staff reports open a review item on a single vote.
type ReporterWeight struct {
	Weight float32
	Staff  bool
}

// Weigher computes a reporter's v0 weight. It is an interface so the accept
// flow is decoupled from the main DB — production wires DBWeigher; tests inject
// a fake without provisioning a users table.
type Weigher interface {
	Weigh(ctx context.Context, reporterID int64) (ReporterWeight, error)
}

// DBWeigher reads the main database (users + user_roles) for account age and
// global roles. The main-DB connection is the same one app.New provides for the
// S2S client registry.
type DBWeigher struct{ mainDB *gorm.DB }

// NewDBWeigher builds the production weigher over the main database.
func NewDBWeigher(mainDB *gorm.DB) *DBWeigher { return &DBWeigher{mainDB: mainDB} }

// staffRoles is the set of global roles that count as staff (single-report
// fast-path). Site-scoped roles are deliberately excluded — a T&S single-vote
// bypass is a platform-trust decision, not a per-site grant.
var staffRoles = []string{"moderator", "admin", "ren"}

// Weigh implements Weigher: base 1.0, halved for accounts younger than 7 days;
// staff are never discounted and get the single-report fast-path. A reporter
// the main DB cannot find (deleted race) weighs the base 1.0, non-staff.
func (w *DBWeigher) Weigh(ctx context.Context, reporterID int64) (ReporterWeight, error) {
	db := w.mainDB.WithContext(ctx)

	var createdAt time.Time
	if err := db.Table("users").Select("created_at").
		Where("id = ?", reporterID).Scan(&createdAt).Error; err != nil {
		return ReporterWeight{}, err
	}

	var staffCount int64
	if err := db.Table("user_roles AS ur").
		Joins("JOIN roles r ON r.id = ur.role_id").
		Where("ur.user_id = ? AND r.name IN ?", reporterID, staffRoles).
		Count(&staffCount).Error; err != nil {
		return ReporterWeight{}, err
	}
	if staffCount > 0 {
		return ReporterWeight{Weight: 1.0, Staff: true}, nil
	}

	weight := float32(1.0)
	// A zero createdAt (reporter not found) is treated as an old account
	// (no discount): fail-open rather than penalize an intake race.
	if !createdAt.IsZero() && time.Since(createdAt) < newAccountAge {
		weight = 0.5
	}
	return ReporterWeight{Weight: weight, Staff: false}, nil
}
