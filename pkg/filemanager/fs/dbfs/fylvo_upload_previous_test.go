package dbfs

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/conf"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
)

// Fylvo: tests for a conditional update, an upload that names the version it started from
// ("previous"). It must replace exactly that version or fail.

// interleavingFileClient runs between once, right after the file named target was read.
// That is where an upload has read its target and has not locked it yet.
type interleavingFileClient struct {
	inventory.FileClient
	target  string
	between func()
	once    sync.Once
}

func (c *interleavingFileClient) GetChildFile(ctx context.Context, root *ent.File, ownerID int, child string, eagerLoading bool) (*ent.File, error) {
	res, err := c.FileClient.GetChildFile(ctx, root, ownerID, child, eagerLoading)
	if child == c.target {
		c.once.Do(c.between)
	}
	return res, err
}

// asInterleaved returns a file system for the user that runs between once after it read target.
func (e *sharedFolderEnv) asInterleaved(u *ent.User, target string, between func()) fs.FileSystem {
	fc := &interleavingFileClient{FileClient: e.fileClient, target: target, between: between}
	return NewDatabaseFS(e.reload(u), fc, e.shareClient, e.logger, e.locks, e.settings, e.policies,
		e.hasher, e.userClient, e.kv, e.kv, inventory.NewDirectLinkClient(e.client, conf.SQLiteDB, e.hasher), nil, e.events)
}

// version returns the hashed id of the current version of a file, as clients see it.
func (e *sharedFolderEnv) version(raw string) string {
	file, err := e.as(e.owner).Get(e.ctx, e.uri(raw), WithFileEntities())
	require.NoError(e.t, err)
	return hashid.EncodeEntityID(e.hasher, file.PrimaryEntityID())
}

// conditionalUpload uploads a new version of target that replaces previous only. With
// required, a missing target fails instead of being created.
func (e *sharedFolderEnv) conditionalUpload(f fs.FileSystem, target *fs.URI, size int64, previous string, required ...bool) (*fs.UploadSession, error) {
	version := types.EntityTypeVersion
	session, err := f.PrepareUpload(e.ctx, &fs.UploadRequest{Props: &fs.UploadProps{
		Uri: target, Size: size, UploadSessionID: uuid.Must(uuid.NewV4()).String(), ExpireAt: time.Now().Add(time.Hour),
		EntityType: &version, PreviousVersion: previous, PreviousRequired: len(required) > 0 && required[0],
	}})
	if err != nil {
		return nil, err
	}

	_, err = f.CompleteUpload(e.ctx, session)
	return session, err
}

func requireCode(t *testing.T, err error, code int) {
	t.Helper()
	require.Error(t, err)
	var appErr serializer.AppError
	require.ErrorAs(t, err, &appErr, err.Error())
	require.Equal(t, code, appErr.Code, err.Error())
}

func TestConditionalUploadFailsWhenReplacedBeforeItsLock(t *testing.T) {
	e := newSharedFolderEnv(t)
	previous := e.version("cloudreve://my/Shared/doc.txt")

	// Between the read and the lock of the phone, the editor uploads a new version.
	phone := e.asInterleaved(e.owner, "doc.txt", func() {
		version := types.EntityTypeVersion
		require.NoError(t, e.tryUploadVersion(e.as(e.editor), e.shared("/doc.txt"), 20, &version))
	})

	_, err := e.conditionalUpload(phone, e.uri("cloudreve://my/Shared/doc.txt"), 30, previous)
	requireCode(t, err, serializer.CodeStaleVersion)

	// The version of the editor stays the current one.
	file, err := e.as(e.owner).Get(e.ctx, e.uri("cloudreve://my/Shared/doc.txt"))
	require.NoError(t, err)
	require.EqualValues(t, 20, file.Size())
}

// deletions are the two ways a file goes: for good, or into the trash.
var deletions = map[string]bool{"hard delete": false, "soft delete": true}

func TestConditionalUploadFailsWhenDeletedBeforeItsLock(t *testing.T) {
	for name, soft := range deletions {
		t.Run(name, func(t *testing.T) {
			e := newSharedFolderEnv(t)
			previous := e.version("cloudreve://my/Shared/doc.txt")

			phone := e.asInterleaved(e.owner, "doc.txt", func() {
				e.remove(soft, "cloudreve://my/Shared/doc.txt")
			})
			_, err := e.conditionalUpload(phone, e.uri("cloudreve://my/Shared/doc.txt"), 30, previous, true)
			requireCode(t, err, serializer.CodeUploadTargetGone)
			e.requireMissing("cloudreve://my/Shared/doc.txt")

			// Without PreviousRequired it fails as well, as a conflict, as any change before a lock does.
			e.upload(e.as(e.owner), "cloudreve://my/Shared/doc.txt", 10)
			previous = e.version("cloudreve://my/Shared/doc.txt")
			phone = e.asInterleaved(e.owner, "doc.txt", func() {
				e.remove(soft, "cloudreve://my/Shared/doc.txt")
			})
			_, err = e.conditionalUpload(phone, e.uri("cloudreve://my/Shared/doc.txt"), 30, previous)
			requireCode(t, err, serializer.CodeConflict)
			e.requireMissing("cloudreve://my/Shared/doc.txt")
		})
	}
}

func TestConditionalUploadFailsWhenDeletedBefore(t *testing.T) {
	for name, soft := range deletions {
		t.Run(name, func(t *testing.T) {
			e := newSharedFolderEnv(t)
			previous := e.version("cloudreve://my/Shared/doc.txt")
			e.remove(soft, "cloudreve://my/Shared/doc.txt")

			_, err := e.conditionalUpload(e.as(e.owner), e.uri("cloudreve://my/Shared/doc.txt"), 30, previous, true)
			requireCode(t, err, serializer.CodeUploadTargetGone)
			e.requireMissing("cloudreve://my/Shared/doc.txt")

			// Without PreviousRequired the file is created new, as the web app and the desktop
			// client expect today.
			_, err = e.conditionalUpload(e.as(e.owner), e.uri("cloudreve://my/Shared/doc.txt"), 30, previous)
			require.NoError(t, err)
		})
	}
}

func TestConditionalUploadFailsWhenAnotherFileTookThePlace(t *testing.T) {
	for name, soft := range deletions {
		t.Run(name, func(t *testing.T) {
			e := newSharedFolderEnv(t)
			previous := e.version("cloudreve://my/Shared/doc.txt")

			// Between the read and the lock, the file is deleted and a copy of it with the very
			// same content (the same version) takes its place.
			phone := e.asInterleaved(e.owner, "doc.txt", func() {
				owner := e.as(e.owner)
				_, err := owner.MoveOrCopy(e.ctx, []*fs.URI{e.uri("cloudreve://my/Shared/doc.txt")}, e.uri("cloudreve://my/Private"), true)
				require.NoError(t, err)
				e.remove(soft, "cloudreve://my/Shared/doc.txt")
				_, err = owner.MoveOrCopy(e.ctx, []*fs.URI{e.uri("cloudreve://my/Private/doc.txt")}, e.uri("cloudreve://my/Shared"), false)
				require.NoError(t, err)
			})
			_, err := e.conditionalUpload(phone, e.uri("cloudreve://my/Shared/doc.txt"), 30, previous, true)
			requireCode(t, err, serializer.CodeStaleVersion)
		})
	}
}

func TestPreviousRequiredRules(t *testing.T) {
	e := newSharedFolderEnv(t)

	// It needs a version to compare with.
	_, err := e.conditionalUpload(e.as(e.owner), e.uri("cloudreve://my/Shared/doc.txt"), 30, "", true)
	requireCode(t, err, serializer.CodeParamErr)

	// A visitor of an upload link only adds files. Without PreviousRequired the upload goes to
	// a free name, as before; with it, it fails, as the visitor may not replace the file.
	_, path := e.uploadLink(&types.ShareProps{AllowUpload: true})
	visitor := e.visitors()[0]
	previous := e.version("cloudreve://my/Shared/doc.txt")
	_, err = e.conditionalUpload(e.as(visitor), path("/doc.txt"), 30, previous, true)
	requireCode(t, err, serializer.CodeOwnerOnly)
	session, err := e.conditionalUpload(e.as(visitor), path("/doc.txt"), 30, previous)
	require.NoError(t, err)
	require.Equal(t, "doc (1).txt", session.Props.Uri.Name())
	require.Equal(t, previous, e.version("cloudreve://my/Shared/doc.txt"))
}

func TestConditionalUploadReplacesItsVersion(t *testing.T) {
	e := newSharedFolderEnv(t)
	previous := e.version("cloudreve://my/Shared/doc.txt")

	// The phone of the owner and an editor in the shared folder.
	session, err := e.conditionalUpload(e.as(e.owner), e.uri("cloudreve://my/Shared/doc.txt"), 30, previous)
	require.NoError(t, err)
	require.Equal(t, hashid.EncodeEntityID(e.hasher, session.EntityID), e.version("cloudreve://my/Shared/doc.txt"))

	previous = e.version("cloudreve://my/Shared/doc.txt")
	session, err = e.conditionalUpload(e.as(e.editor), e.shared("/doc.txt"), 40, previous)
	require.NoError(t, err)
	require.Equal(t, hashid.EncodeEntityID(e.hasher, session.EntityID), e.version("cloudreve://my/Shared/doc.txt"))

	// An old version is refused.
	_, err = e.conditionalUpload(e.as(e.owner), e.uri("cloudreve://my/Shared/doc.txt"), 50, previous)
	requireCode(t, err, serializer.CodeStaleVersion)

	// A reader may not, whatever the version.
	_, err = e.conditionalUpload(e.as(e.reader), e.shared("/doc.txt"), 50, e.version("cloudreve://my/Shared/doc.txt"))
	require.Error(t, err)
}

func (e *sharedFolderEnv) tryUploadVersion(f fs.FileSystem, target *fs.URI, size int64, entityType *types.EntityType) error {
	session, err := f.PrepareUpload(e.ctx, &fs.UploadRequest{Props: &fs.UploadProps{
		Uri: target, Size: size, UploadSessionID: uuid.Must(uuid.NewV4()).String(), ExpireAt: time.Now().Add(time.Hour),
		EntityType: entityType,
	}})
	if err != nil {
		return err
	}

	_, err = f.CompleteUpload(e.ctx, session)
	return err
}

// remove deletes a file of the owner for good or into the trash.
func (e *sharedFolderEnv) remove(soft bool, raw string) {
	if soft {
		require.NoError(e.t, e.as(e.owner).SoftDelete(e.ctx, e.uri(raw)))
		return
	}
	_, _, err := e.as(e.owner).Delete(e.ctx, []*fs.URI{e.uri(raw)})
	require.NoError(e.t, err)
}

func (e *sharedFolderEnv) requireMissing(raw string) {
	_, err := e.as(e.owner).Get(e.ctx, e.uri(raw))
	require.Error(e.t, err)
}
