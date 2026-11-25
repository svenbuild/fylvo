package model

import (
	"github.com/jinzhu/gorm"
)

// Setting system settings model
type Setting struct {
	gorm.Model
	Type  string `gorm:"not null"`
	Name  string `gorm:"unique;not null;index:setting_key"`
	Value string `gorm:"size:65535"`
}
