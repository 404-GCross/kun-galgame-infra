package model

type Role struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	Name        string `gorm:"size:50;uniqueIndex;not null" json:"name"`
	Description string `gorm:"type:text;default:''" json:"description"`
}

func (Role) TableName() string {
	return "roles"
}
