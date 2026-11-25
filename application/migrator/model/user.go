package model

import (
	"github.com/jinzhu/gorm"
)

const (
	// Active account in normal state
	Active = iota
	// NotActivicated not activated
	NotActivicated
	// Baned banned
	Baned
	// OveruseBaned banned for overuse
	OveruseBaned
)

// User user model
type User struct {
	// Table fields
	gorm.Model
	Email     string `gorm:"type:varchar(100);unique_index"`
	Nick      string `gorm:"size:50"`
	Password  string `json:"-"`
	Status    int
	GroupID   uint
	Storage   uint64
	TwoFactor string
	Avatar    string
	Options   string `json:"-" gorm:"size:4294967295"`
	Authn     string `gorm:"size:4294967295"`

	// Related models
	Group  Group  `gorm:"save_associations:false:false"`
	Policy Policy `gorm:"PRELOAD:false,association_autoupdate:false"`

	// Fields ignored by the database
	OptionsSerialized UserOption `gorm:"-"`
}

// UserOption user personalization config fields
type UserOption struct {
	ProfileOff     bool   `json:"profile_off,omitempty"`
	PreferredTheme string `json:"preferred_theme,omitempty"`
}
