package dbfs

import (
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
)

// Fylvo: the import of an existing folder keeps the dates of the files and folders on disk.

var diskDate = time.Date(2014, 5, 17, 9, 30, 12, 0, time.UTC)

// importFile creates a file the way the admin import does: the content stays where it is on
// disk, only the database rows are made.
func (e *sharedFolderEnv) importFile(f fs.FileSystem, raw string, size int64, modified time.Time) error {
	src := &fs.PhysicalObject{Name: "x", Source: "import/x", Size: size, LastModify: modified}
	policy, err := e.policies.GetPolicyByID(e.ctx, 1)
	require.NoError(e.t, err)

	_, err = f.PrepareUpload(e.ctx, &fs.UploadRequest{
		Props: &fs.UploadProps{
			Uri:                    e.uri(raw),
			Size:                   size,
			UploadSessionID:        uuid.Must(uuid.NewV4()).String(),
			PreferredStoragePolicy: policy.ID,
			SavePath:               src.Source,
			LastModified:           &src.LastModify,
		},
		ImportFrom: src,
	})
	return err
}

func (e *sharedFolderEnv) dates(f fs.FileSystem, raw string) (created, updated time.Time) {
	file, err := f.Get(e.ctx, e.uri(raw), WithFileEntities())
	require.NoError(e.t, err)
	return file.CreatedAt(), file.UpdatedAt()
}

func requireSameTime(t *testing.T, want, got time.Time, what string) {
	t.Helper()
	require.Equal(t, want.Unix(), got.Unix(), "%s: want %s, got %s", what, want, got)
}

func TestImportedFileKeepsItsDate(t *testing.T) {
	e := newSharedFolderEnv(t)
	owner := e.as(e.owner)

	require.NoError(t, e.importFile(owner, "cloudreve://my/Import/photo.jpg", 100, diskDate))

	created, updated := e.dates(owner, "cloudreve://my/Import/photo.jpg")
	requireSameTime(t, diskDate, updated, "change date")
	requireSameTime(t, diskDate, created, "creation date")

	// The version list shows the date of the version, that is the date on disk too.
	file, err := owner.Get(e.ctx, e.uri("cloudreve://my/Import/photo.jpg"), WithFileEntities())
	require.NoError(t, err)
	require.Len(t, file.Entities(), 1)
	requireSameTime(t, diskDate, file.Entities()[0].CreatedAt(), "version date")
	requireSameTime(t, diskDate, file.Entities()[0].UpdatedAt(), "version change date")
}

func TestImportingAgainChangesNothing(t *testing.T) {
	e := newSharedFolderEnv(t)
	owner := e.as(e.owner)

	require.NoError(t, e.importFile(owner, "cloudreve://my/Import/a.txt", 10, diskDate))
	err := e.importFile(e.as(e.owner), "cloudreve://my/Import/a.txt", 10, diskDate.Add(time.Hour))
	require.ErrorIs(t, err, fs.ErrFileExisted)

	file, err := e.as(e.owner).Get(e.ctx, e.uri("cloudreve://my/Import/a.txt"), WithFileEntities())
	require.NoError(t, err)
	require.Len(t, file.Entities(), 1)
	requireSameTime(t, diskDate, file.UpdatedAt(), "change date")
}

func TestImportedFolderKeepsItsDate(t *testing.T) {
	e := newSharedFolderEnv(t)
	owner := e.as(e.owner)

	_, err := owner.Create(e.ctx, e.uri("cloudreve://my/Import/Fotos 2014"), types.FileTypeFolder, fs.WithModifiedAt(diskDate))
	require.NoError(t, err)
	created, updated := e.dates(owner, "cloudreve://my/Import/Fotos 2014")
	requireSameTime(t, diskDate, updated, "change date")
	requireSameTime(t, diskDate, created, "creation date")

	// Only the folder that was asked for gets the date, not the folders made on the way.
	_, updated = e.dates(owner, "cloudreve://my/Import")
	require.WithinDuration(t, time.Now(), updated, time.Minute)

	// A file put into the folder later does not change the date of the folder.
	require.NoError(t, e.importFile(owner, "cloudreve://my/Import/Fotos 2014/a.jpg", 10, diskDate.Add(time.Hour)))
	_, updated = e.dates(e.as(e.owner), "cloudreve://my/Import/Fotos 2014")
	requireSameTime(t, diskDate, updated, "change date after adding a file")
}

func TestNormalUploadStillGetsTodaysDate(t *testing.T) {
	e := newSharedFolderEnv(t)
	owner := e.as(e.owner)

	e.upload(owner, "cloudreve://my/new.txt", 10)
	created, updated := e.dates(owner, "cloudreve://my/new.txt")
	require.WithinDuration(t, time.Now(), created, time.Minute)
	require.WithinDuration(t, time.Now(), updated, time.Minute)
}

func TestNewContentOfImportedFileGetsTodaysDate(t *testing.T) {
	e := newSharedFolderEnv(t)
	owner := e.as(e.owner)
	require.NoError(t, e.importFile(owner, "cloudreve://my/Import/doc.txt", 10, diskDate))

	version := types.EntityTypeVersion
	f := e.as(e.owner)
	session, err := f.PrepareUpload(e.ctx, &fs.UploadRequest{
		Props: &fs.UploadProps{
			Uri:             e.uri("cloudreve://my/Import/doc.txt"),
			Size:            20,
			UploadSessionID: uuid.Must(uuid.NewV4()).String(),
			ExpireAt:        time.Now().Add(time.Hour),
			EntityType:      &version,
		},
	})
	require.NoError(t, err)
	_, err = f.CompleteUpload(e.ctx, session)
	require.NoError(t, err)

	created, updated := e.dates(e.as(e.owner), "cloudreve://my/Import/doc.txt")
	require.WithinDuration(t, time.Now(), updated, time.Minute)
	requireSameTime(t, diskDate, created, "creation date stays")
}
