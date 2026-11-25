package model

import (
	"time"

	"github.com/jinzhu/gorm"
)

// Share share model
type Share struct {
	gorm.Model
	Password        string     // Share password, empty means not encrypted
	IsDir           bool       // Whether the original resource is a directory
	UserID          uint       // Creator user ID
	SourceID        uint       // Original resource ID
	Views           int        // View count
	Downloads       int        // Download count
	RemainDownloads int        // Remaining download quota, negative means unlimited
	Expires         *time.Time // Expiry time, empty means no expiry
	PreviewEnabled  bool       // Whether direct preview is allowed
	SourceName      string     `gorm:"index:source"` // Field used for search

	// Fields ignored by the database
	User   User   `gorm:"PRELOAD:false,association_autoupdate:false"`
	File   File   `gorm:"PRELOAD:false,association_autoupdate:false"`
	Folder Folder `gorm:"PRELOAD:false,association_autoupdate:false"`
}
