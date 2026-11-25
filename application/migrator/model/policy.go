package model

import (
	"github.com/jinzhu/gorm"
)

// Policy storage policy
type Policy struct {
	// table fields
	gorm.Model
	Name               string
	Type               string
	Server             string
	BucketName         string
	IsPrivate          bool
	BaseURL            string
	AccessKey          string `gorm:"type:text"`
	SecretKey          string `gorm:"type:text"`
	MaxSize            uint64
	AutoRename         bool
	DirNameRule        string
	FileNameRule       string
	IsOriginLinkEnable bool
	Options            string `gorm:"type:text"`

	// fields ignored by the database
	OptionsSerialized PolicyOption `gorm:"-"`
	MasterID          string       `gorm:"-"`
}

// PolicyOption non-public storage policy properties
type PolicyOption struct {
	// Upyun access token
	Token string `json:"token"`
	// allowed file extensions
	FileType []string `json:"file_type"`
	// MimeType
	MimeType string `json:"mimetype"`
	// OauthRedirect Oauth redirect URL
	OauthRedirect string `json:"od_redirect,omitempty"`
	// OdProxy Onedrive reverse proxy URL
	OdProxy string `json:"od_proxy,omitempty"`
	// OdDriver OneDrive drive locator
	OdDriver string `json:"od_driver,omitempty"`
	// Region region code
	Region string `json:"region,omitempty"`
	// ServerSideEndpoint endpoint used for server-side requests, falls back to Policy.Server when empty
	ServerSideEndpoint string `json:"server_side_endpoint,omitempty"`
	// chunk size for chunked upload
	ChunkSize uint64 `json:"chunk_size,omitempty"`
	// whether to reserve space during chunked upload
	PlaceholderWithSize bool `json:"placeholder_with_size,omitempty"`
	// max API requests per second to the storage backend
	TPSLimit float64 `json:"tps_limit,omitempty"`
	// max API request burst per second
	TPSLimitBurst int `json:"tps_limit_burst,omitempty"`
	// Set this to `true` to force the request to use path-style addressing,
	// i.e., `http://s3.amazonaws.com/BUCKET/KEY `
	S3ForcePathStyle bool `json:"s3_path_style"`
	// File extensions that support thumbnail generation using native policy API.
	ThumbExts []string `json:"thumb_exts,omitempty"`
}
