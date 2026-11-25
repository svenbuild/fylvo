package model

import (
	"github.com/jinzhu/gorm"
)

// Webdav app account
type Webdav struct {
	gorm.Model
	Name     string // app name
	Password string `gorm:"unique_index:password_only_on"` // app password
	UserID   uint   `gorm:"unique_index:password_only_on"` // user ID
	Root     string `gorm:"type:text"`                     // root directory
	Readonly bool   `gorm:"type:bool"`                     // whether read-only
	UseProxy bool   `gorm:"type:bool"`                     // whether to use the proxy
}
