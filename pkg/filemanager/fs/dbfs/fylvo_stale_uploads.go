package dbfs

import (
	"context"

	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
)

// MetadataUploadNewFile holds the id of the upload session that made the file, until the upload is
// finished. Fylvo: the clean-up of abandoned uploads removes such a file as a whole.
const MetadataUploadNewFile = MetadataSysPrefix + "upload_new_file"

// removeUploadMarks removes the "uploading" marks of the given upload session from a file. Marks that
// another upload set in the meantime stay.
func (f *DBFS) removeUploadMarks(ctx context.Context, file *File, sessionID string) error {
	for _, key := range []string{MetadataUploadSessionID, MetadataUploadNewFile} {
		if err := f.fileClient.RemoveMetadataIfValue(ctx, file.Model, key, sessionID); err != nil {
			return serializer.NewError(serializer.CodeDBError, "Failed to remove upload session metadata", err)
		}
	}

	return nil
}
