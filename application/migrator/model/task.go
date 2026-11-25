package model

import (
	"github.com/jinzhu/gorm"
)

// Task task model
type Task struct {
	gorm.Model
	Status   int    // task status
	Type     int    // task type
	UserID   uint   // initiator UID, 0 means started by the system
	Progress int    // progress
	Error    string `gorm:"type:text"` // error message
	Props    string `gorm:"type:text"` // task properties
}
