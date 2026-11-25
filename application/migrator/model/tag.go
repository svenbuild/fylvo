package model

import (
	"github.com/jinzhu/gorm"
)

// Tag user-defined tag
type Tag struct {
	gorm.Model
	Name       string // tag name
	Icon       string // icon identifier
	Color      string // icon color
	Type       int    // tag type (file category / directory shortcut)
	Expression string `gorm:"type:text"` // search expression / shortcut path
	UserID     uint   // creator ID
}

const (
	// FileTagType file category tag
	FileTagType = iota
	// DirectoryLinkType directory shortcut tag
	DirectoryLinkType
)
