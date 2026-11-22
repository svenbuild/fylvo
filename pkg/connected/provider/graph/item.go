package graph

import (
	"time"

	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
)

type driveItem struct {
	ID                   string     `json:"id"`
	Name                 string     `json:"name"`
	Size                 int64      `json:"size"`
	Folder               *struct{}  `json:"folder"`
	Package              *struct{}  `json:"package"`
	Deleted              *struct{}  `json:"deleted"`
	RemoteItem           *driveItem `json:"remoteItem"`
	LastModifiedDateTime time.Time  `json:"lastModifiedDateTime"`
	CTag                 string     `json:"cTag"`
	ETag                 string     `json:"eTag"`
	WebURL               string     `json:"webUrl"`
	File                 *struct {
		Hashes struct {
			QuickXorHash string `json:"quickXorHash"`
		} `json:"hashes"`
	} `json:"file"`
	SpecialFolder *struct {
		Name string `json:"name"`
	} `json:"specialFolder"`
	FileSystemInfo struct {
		LastModifiedDateTime time.Time `json:"lastModifiedDateTime"`
	} `json:"fileSystemInfo"`
	ParentReference struct {
		DriveID string `json:"driveId"`
	} `json:"parentReference"`
}

func (entry *driveItem) hidden() bool {
	return entry.Deleted != nil || entry.Package != nil || (entry.SpecialFolder != nil && entry.SpecialFolder.Name == "vault")
}

func (entry *driveItem) asItem() *provider.Item {
	if entry.hidden() {
		return nil
	}
	item := &provider.Item{ID: entry.ID, Name: entry.Name, WebURL: entry.WebURL}
	content := entry
	if remote := entry.RemoteItem; remote != nil {
		if remote.hidden() {
			return nil
		}
		content = remote
		item.Link = &provider.Link{TargetID: remote.ID, TargetDrive: remote.ParentReference.DriveID}
		item.Size = remote.Size
	}
	item.IsDir = content.Folder != nil
	item.ModTime = content.FileSystemInfo.LastModifiedDateTime
	if item.ModTime.IsZero() {
		item.ModTime = content.LastModifiedDateTime
	}
	if content.File != nil {
		item.Size = content.Size
		item.Hash = content.File.Hashes.QuickXorHash
		if item.Link == nil {
			item.ETag = content.ETag
			if content.CTag != "" {
				item.Version = "c:" + content.CTag
			}
		}
	}
	return item
}
