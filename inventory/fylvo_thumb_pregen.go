package inventory

import (
	"context"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/entity"
	"github.com/cloudreve/Cloudreve/v4/ent/file"
	"github.com/cloudreve/Cloudreve/v4/ent/metadata"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
)

// ThumbCandidates returns up to limit files that have a current version but no thumbnail and are
// not marked with disabledKey (Fylvo, thumbnails made ahead of time). Only files with an id below
// before and above after; newest first.
func (f *fileClient) ThumbCandidates(ctx context.Context, after, before, limit int, disabledKey string) ([]*ent.File, error) {
	return f.client.File.Query().
		Where(
			file.Type(int(types.FileTypeFile)),
			file.IDGT(after),
			file.IDLT(before),
			file.PrimaryEntityNEQ(0),
			file.Not(file.HasEntitiesWith(entity.Type(int(types.EntityTypeThumbnail)), entity.DeletedAtIsNil())),
			file.Not(file.HasMetadataWith(metadata.Name(disabledKey), metadata.DeletedAtIsNil())),
		).
		Order(ent.Desc(file.FieldID)).
		Limit(limit).
		All(ctx)
}

// MaxFileID returns the highest id of a file or folder, 0 if there is none (Fylvo).
func (f *fileClient) MaxFileID(ctx context.Context) (int, error) {
	last, err := f.client.File.Query().Order(ent.Desc(file.FieldID)).First(ctx)
	if ent.IsNotFound(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return last.ID, nil
}
