package dbfs

import (
	"context"
	"time"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory"
)

// Fylvo: "last changed" of a file changes only with its content. In the trash it tells when
// the file was deleted, as the apps show it that way; the file's own date waits in this
// metadata and comes back when the file is restored.
const MetadataRestoreModifiedAt = MetadataSysPrefix + "restore_modified_at"

// keptModifiedAt reads the date of the file as it is now, after the lock is held, so that a
// new version saved just before the delete is not missed. The time zone stays as it was:
// SQLite sorts dates as text, so a restored date must be written exactly as before.
func keptModifiedAt(ctx context.Context, fc inventory.FileClient, file *ent.File) (string, error) {
	current, err := fc.GetByID(ctx, file.ID)
	if err != nil {
		return "", err
	}

	return current.UpdatedAt.Format(time.RFC3339Nano), nil
}

// restoreModifiedAt gives a file back from the trash its own date. Files deleted before Fylvo
// kept the date have none and keep the date of their deletion.
func restoreModifiedAt(ctx context.Context, fc inventory.FileClient, file *File) error {
	kept, ok := file.Metadata()[MetadataRestoreModifiedAt]
	if !ok {
		return nil
	}

	modifiedAt, err := time.Parse(time.RFC3339Nano, kept)
	if err != nil {
		return nil
	}

	return fc.UpdateModifiedAt(ctx, file.Model, modifiedAt)
}
