package inventory

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/entity"
	entfile "github.com/cloudreve/Cloudreve/v4/ent/file"
	"github.com/cloudreve/Cloudreve/v4/ent/metadata"
	"github.com/cloudreve/Cloudreve/v4/ent/schema"
	"github.com/cloudreve/Cloudreve/v4/ent/storagepolicy"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/samber/lo"
	"golang.org/x/tools/container/intsets"
)

// Fylvo: helpers for the clean-up of abandoned uploads.

// StaleUploads returns the placeholders of uploads that started before the given time and were never
// finished: entities with an upload session, kept by exactly the one file they belong to, on a local
// storage policy. The file of each entity is loaded.
func (f *fileClient) StaleUploads(ctx context.Context, startedBefore time.Time) ([]*ent.Entity, error) {
	return f.client.Entity.Query().
		Where(
			entity.UploadSessionIDNotNil(),
			entity.TypeIn(int(types.EntityTypeVersion), int(types.EntityTypeLivePhoto)),
			entity.ReferenceCount(1),
			entity.CreatedAtLT(startedBefore),
			entity.HasStoragePolicyWith(storagepolicy.Type(types.PolicyTypeLocal)),
		).
		WithFile().
		Order(ent.Asc(entity.FieldID)).
		All(ctx)
}

// RemoveMetadataIfValue removes a metadata entry of a file only while it still has the given value.
func (f *fileClient) RemoveMetadataIfValue(ctx context.Context, file *ent.File, name, value string) error {
	_, err := f.client.Metadata.Delete().
		Where(metadata.FileID(file.ID), metadata.Name(name), metadata.Value(value)).
		Exec(schema.SkipSoftDelete(ctx))
	if err != nil {
		return fmt.Errorf("failed to remove metadata: %v", err)
	}

	return nil
}

// unlinkIfLinked takes an entity off a file only while it is still on it, so two callers working on the
// same old copy of a file cannot both take it off and give its storage back twice.
func (f *fileClient) unlinkIfLinked(ctx context.Context, entityID int, file *ent.File) (bool, error) {
	n, err := f.client.Entity.Update().
		Where(entity.ID(entityID), entity.HasFileWith(entfile.ID(file.ID))).
		RemoveFile(file).
		AddReferenceCount(-1).
		Save(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to unlink entity: %v", err)
	}

	return n > 0, nil
}

// withCurrentEntities returns copies of the files with the entities that are on them now. A file loaded
// before its path was locked can list a placeholder that was taken off in the meantime.
func (f *fileClient) withCurrentEntities(ctx context.Context, files []*ent.File) ([]*ent.File, error) {
	current := make(map[int][]*ent.Entity, len(files))
	groups, _ := f.batchInCondition(intsets.MaxInt, 10, 1, lo.Map(files, func(file *ent.File, _ int) int {
		return file.ID
	}))
	for _, group := range groups {
		loaded, err := f.client.File.Query().Where(group).WithEntities().All(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to load entities of files: %w", err)
		}
		for _, file := range loaded {
			current[file.ID] = file.Edges.Entities
		}
	}

	return lo.Map(files, func(file *ent.File, _ int) *ent.File {
		fresh := *file
		fresh.Edges.Entities = current[file.ID]
		return &fresh
	}), nil
}
