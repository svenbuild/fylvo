package model

import (
	"github.com/jinzhu/gorm"
)

// Group is the user group model
type Group struct {
	gorm.Model
	Name          string
	Policies      string
	MaxStorage    uint64
	ShareEnabled  bool
	WebDAVEnabled bool
	SpeedLimit    int
	Options       string `json:"-" gorm:"size:4294967295"`

	// field ignored by the database
	PolicyList        []uint      `gorm:"-"`
	OptionsSerialized GroupOption `gorm:"-"`
}

// GroupOption holds other group settings
type GroupOption struct {
	ArchiveDownload  bool                   `json:"archive_download,omitempty"` // archive download
	ArchiveTask      bool                   `json:"archive_task,omitempty"`     // online compression
	CompressSize     uint64                 `json:"compress_size,omitempty"`    // compressible size
	DecompressSize   uint64                 `json:"decompress_size,omitempty"`
	OneTimeDownload  bool                   `json:"one_time_download,omitempty"`
	ShareDownload    bool                   `json:"share_download,omitempty"`
	Aria2            bool                   `json:"aria2,omitempty"`         // offline download
	Aria2Options     map[string]interface{} `json:"aria2_options,omitempty"` // offline download group settings
	SourceBatchSize  int                    `json:"source_batch,omitempty"`
	RedirectedSource bool                   `json:"redirected_source,omitempty"`
	Aria2BatchSize   int                    `json:"aria2_batch,omitempty"`
	AdvanceDelete    bool                   `json:"advance_delete,omitempty"`
	WebDAVProxy      bool                   `json:"webdav_proxy,omitempty"`
}
