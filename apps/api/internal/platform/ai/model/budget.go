package model

import "time"

// AIRouteBudget is the per-route (optionally per-site) daily cost fuse
// (doc 20 §5, decision 4). v0 is RECORD-DON'T-BLOCK: the judgment path is wired
// but a NULL cap never blocks — the default until ops inserts a cap row.
//
// Scope resolution: a (route, site) row overrides the (route, ”) route-wide
// default row. A specific row that exists with a NULL cap is an explicit
// "no cap for this site" override (it does NOT fall through to the default).
type AIRouteBudget struct {
	// Composite PK (route, site). site='' is the route-wide default; a concrete
	// site overrides it. '' is a DELIBERATE default value (the fallback scope),
	// not the zero-value trap — a PK column is NOT NULL regardless.
	Route string `gorm:"primaryKey;column:route" json:"route"`
	Site  string `gorm:"primaryKey;not null;default:'';column:site" json:"site"`
	// DailyCostCapMicro is the per-day micro-currency cap. NULL = no block (the
	// v0 default). Deliberately nullable: absence of a cap ("don't judge") is
	// distinct from a 0 cap (which would block everything).
	DailyCostCapMicro *int64    `gorm:"column:daily_cost_cap_micro" json:"daily_cost_cap_micro"`
	UpdatedAt         time.Time `gorm:"not null;default:now();column:updated_at" json:"updated_at"`
}

func (AIRouteBudget) TableName() string { return "ai_route_budget" }
