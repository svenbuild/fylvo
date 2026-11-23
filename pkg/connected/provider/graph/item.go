package graph

import (
	"strconv"
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
	return entry.Deleted != nil
}

// native is the kind of an entry that has no bytes to read and no children to list: a
// OneNote notebook, or the Personal Vault, which stays locked for an app like this one.
// Such an entry shows in a listing and opens at the provider.
func (entry *driveItem) native() string {
	switch {
	case entry.SpecialFolder != nil && entry.SpecialFolder.Name == "vault":
		return "vault"
	case entry.Package != nil:
		return "onenote"
	}
	return ""
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
	item.Native = content.native()
	item.IsDir = content.Folder != nil && item.Native == ""
	item.ModTime = content.FileSystemInfo.LastModifiedDateTime
	if item.ModTime.IsZero() {
		item.ModTime = content.LastModifiedDateTime
	}
	if content.File != nil && item.Native == "" {
		item.Size = content.Size
		item.Hash = content.File.Hashes.QuickXorHash
		if item.Link == nil {
			// What a listing tells of the target of a link can be old: see Target.
			item.ETag = content.ETag
			item.Version = content.version()
		}
	}
	return item
}

// version names the state of the content of a file: by the tag for it, else by the tag
// of the item (which changes more often, never less), else by its checksum. A time and a size are no evidence, a file can be
// replaced by another one of the same size within the same second: without any of the
// three there is no version, and nothing can be tied to such content.
func (entry *driveItem) version() string {
	switch {
	case entry.CTag != "":
		return "c:" + entry.CTag
	case entry.ETag != "":
		return "e:" + entry.ETag
	case entry.File != nil && entry.File.Hashes.QuickXorHash != "":
		return "h:" + entry.File.Hashes.QuickXorHash + ":" + strconv.FormatInt(entry.Size, 10)
	}
	return ""
}
