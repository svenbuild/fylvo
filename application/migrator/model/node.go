package model

import (
	"github.com/jinzhu/gorm"
)

// Node slave node info model
type Node struct {
	gorm.Model
	Status       NodeStatus // node status
	Name         string     // node alias
	Type         ModelType  // node status
	Server       string     // server address
	SlaveKey     string     `gorm:"type:text"` // master->slave communication key
	MasterKey    string     `gorm:"type:text"` // slave->master communication key
	Aria2Enabled bool       // whether it can be used as an offline download node
	Aria2Options string     `gorm:"type:text"` // offline download config
	Rank         int        // load balancing weight

	// fields ignored by the database
	Aria2OptionsSerialized Aria2Option `gorm:"-"`
}

// Aria2Option non-public Aria2 config properties
type Aria2Option struct {
	// RPC server address
	Server string `json:"server,omitempty"`
	// RPC secret
	Token string `json:"token,omitempty"`
	// temporary download folder
	TempPath string `json:"temp_path,omitempty"`
	// extra download config
	Options string `json:"options,omitempty"`
	// download monitor interval
	Interval int `json:"interval,omitempty"`
	// RPC API request timeout
	Timeout int `json:"timeout,omitempty"`
}

type NodeStatus int
type ModelType int

const (
	NodeActive NodeStatus = iota
	NodeSuspend
)

const (
	SlaveNodeType ModelType = iota
	MasterNodeType
)
