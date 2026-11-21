package activity

import (
	"context"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
)

// Kinds of shares stored in ActivityContent.Target.
const (
	ShareKindLink    = "link"
	ShareKindUpload  = "upload"
	ShareKindMembers = "members"
)

// RecordShare logs a change of share, done by actor, about the shared file (nil when unknown).
func RecordShare(ctx context.Context, typ Type, actor int, share *ent.Share, file *ent.File) {
	e := Entry{Type: typ, UserID: actor, ShareID: share.ID}
	switch {
	case share.Props != nil && share.Props.MemberOnly:
		e.Content.Target = ShareKindMembers
	case share.Props != nil && share.Props.AllowUpload:
		e.Content.Target = ShareKindUpload
	default:
		e.Content.Target = ShareKindLink
	}

	if file == nil {
		file = share.Edges.File
	}
	if file != nil {
		e.FileID = file.ID
		e.OwnerID = file.OwnerID
		e.ParentID = file.FileChildren
		e.Content.Name = file.Name
		e.Content.IsFolder = file.Type == int(types.FileTypeFolder)
	}

	Record(ctx, e)
}
