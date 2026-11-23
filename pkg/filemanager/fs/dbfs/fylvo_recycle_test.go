package dbfs

import (
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
)

// Fylvo: a thumbnail task once crashed in walkNext. The request that queued it had ended and put
// its file system back for reuse (Recycle) while the task still used that file system to store
// the thumbnail.

func TestUploadAfterRecycleDoesNotCrash(t *testing.T) {
	e := newSharedFolderEnv(t)
	owner := e.as(e.owner)
	target := e.uri("cloudreve://my/Shared/doc.txt")

	// The request: reads the file, then ends and puts its file system back.
	_, err := owner.Get(e.ctx, target)
	require.NoError(t, err)
	owner.Recycle()
	// A second Recycle (the background thumbnail maker did that) must not put files back twice.
	owner.Recycle()

	// The task: stores a new version through the same file system.
	version := types.EntityTypeVersion
	require.NotPanics(t, func() {
		session, err := owner.PrepareUpload(e.ctx, &fs.UploadRequest{Props: &fs.UploadProps{
			Uri: target, Size: 5, UploadSessionID: uuid.Must(uuid.NewV4()).String(),
			ExpireAt: time.Now().Add(time.Hour), EntityType: &version,
		}})
		require.NoError(t, err)
		_, err = owner.CompleteUpload(e.ctx, session)
		require.NoError(t, err)
	})
}

func TestWalkFromRecycledFileEndsWithError(t *testing.T) {
	e := newSharedFolderEnv(t)
	owner := e.as(e.owner).(*DBFS)

	// A walk that still holds a folder after it was put back for reuse.
	folder, err := owner.Get(e.ctx, e.uri("cloudreve://my/Shared"))
	require.NoError(t, err)
	held := folder.(*File)
	owner.Recycle()

	navigator := newBaseNavigator(owner.fileClient, defaultFilter, e.owner, owner.hasher, nil)
	require.NotPanics(t, func() {
		_, err = navigator.walkNext(e.ctx, held, "doc.txt", true)
	})
	require.ErrorIs(t, err, errRecycledFile)
}
