package model

import (
	"github.com/jinzhu/gorm"
)

// Folder folder
type Folder struct {
	// table fields
	gorm.Model
	Name     string `gorm:"unique_index:idx_only_one_name"`
	ParentID *uint  `gorm:"index:parent_id;unique_index:idx_only_one_name"`
	OwnerID  uint   `gorm:"index:owner_id"`

	// fields ignored by the database
	Position      string `gorm:"-"`
	WebdavDstName string `gorm:"-"`
}
