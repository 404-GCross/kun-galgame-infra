package model

import "time"

type AIRouteBudget struct {
	Route             string    `gorm:"primaryKey;column:route" json:"route"`
	Site              string    `gorm:"primaryKey;not null;default:'';column:site" json:"site"`
	DailyCostCapMicro *int64    `gorm:"column:daily_cost_cap_micro" json:"daily_cost_cap_micro"`
	UpdatedAt         time.Time `gorm:"not null;default:now();column:updated_at" json:"updated_at"`
}

func (AIRouteBudget) TableName() string { return "ai_route_budget" }
