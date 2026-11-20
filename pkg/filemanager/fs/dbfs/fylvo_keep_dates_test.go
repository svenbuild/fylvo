package dbfs

import (
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
)

// Fylvo: "last changed" of a file changes only when its content changes. Renaming, moving,
// copying, tagging and the trash keep it.

var oldDate = time.Date(2016, 8, 3, 14, 5, 9, 0, time.UTC)

// aged makes a file look as if it was last changed long ago.
func (e *sharedFolderEnv) aged(f fs.FileSystem, raw string) {
	file, err := f.Get(e.ctx, e.uri(raw))
	require.NoError(e.t, err)
	require.NoError(e.t, e.fileClient.UpdateModifiedAt(e.ctx, file.(*File).Model, oldDate))
}

func (e *sharedFolderEnv) changedAt(raw string) time.Time {
	_, updated := e.dates(e.as(e.owner), raw)
	return updated
}

func TestRenameKeepsTheDate(t *testing.T) {
	e := newSharedFolderEnv(t)
	owner := e.as(e.owner)
	e.upload(owner, "cloudreve://my/foto.jpg", 10)
	e.aged(owner, "cloudreve://my/foto.jpg")

	_, _, err := e.as(e.owner).Rename(e.ctx, e.uri("cloudreve://my/foto.jpg"), "Urlaub.jpg")
	require.NoError(t, err)
	requireSameTime(t, oldDate, e.changedAt("cloudreve://my/Urlaub.jpg"), "after rename")

	// Only upper and lower case.
	_, _, err = e.as(e.owner).Rename(e.ctx, e.uri("cloudreve://my/Urlaub.jpg"), "urlaub.jpg")
	require.NoError(t, err)
	requireSameTime(t, oldDate, e.changedAt("cloudreve://my/urlaub.jpg"), "after case-only rename")
}

func TestFolderRenameKeepsTheDate(t *testing.T) {
	e := newSharedFolderEnv(t)
	owner := e.as(e.owner)
	e.mkdir(owner, "cloudreve://my/Fotos")
	e.aged(owner, "cloudreve://my/Fotos")

	_, _, err := e.as(e.owner).Rename(e.ctx, e.uri("cloudreve://my/Fotos"), "Fotos 2016")
	require.NoError(t, err)
	requireSameTime(t, oldDate, e.changedAt("cloudreve://my/Fotos 2016"), "folder after rename")
}

func TestMoveKeepsTheDate(t *testing.T) {
	e := newSharedFolderEnv(t)
	owner := e.as(e.owner)
	e.upload(owner, "cloudreve://my/a.txt", 10)
	e.aged(owner, "cloudreve://my/a.txt")
	e.mkdir(owner, "cloudreve://my/Archiv")

	_, err := e.as(e.owner).MoveOrCopy(e.ctx, []*fs.URI{e.uri("cloudreve://my/a.txt")}, e.uri("cloudreve://my/Archiv"), false)
	require.NoError(t, err)
	requireSameTime(t, oldDate, e.changedAt("cloudreve://my/Archiv/a.txt"), "after move")
}

func TestCopyTakesTheDateOfItsSource(t *testing.T) {
	e := newSharedFolderEnv(t)
	owner := e.as(e.owner)
	e.upload(owner, "cloudreve://my/a.txt", 10)
	e.aged(owner, "cloudreve://my/a.txt")
	e.mkdir(owner, "cloudreve://my/Kopie")

	_, err := e.as(e.owner).MoveOrCopy(e.ctx, []*fs.URI{e.uri("cloudreve://my/a.txt")}, e.uri("cloudreve://my/Kopie"), true)
	require.NoError(t, err)
	created, updated := e.dates(e.as(e.owner), "cloudreve://my/Kopie/a.txt")
	requireSameTime(t, oldDate, updated, "copy")
	require.WithinDuration(t, time.Now(), created, time.Minute, "the copy itself is new")
	requireSameTime(t, oldDate, e.changedAt("cloudreve://my/a.txt"), "source")
}

func TestTagsAndPropsKeepTheDate(t *testing.T) {
	e := newSharedFolderEnv(t)
	owner := e.as(e.owner)
	e.upload(owner, "cloudreve://my/a.txt", 10)
	e.aged(owner, "cloudreve://my/a.txt")

	require.NoError(t, e.as(e.owner).PatchMetadata(e.ctx, []*fs.URI{e.uri("cloudreve://my/a.txt")},
		fs.MetadataPatch{Key: "tag:Urlaub", Value: "#ff0000"},
		fs.MetadataPatch{Key: "customize:icon_color", Value: "#00ff00"}))
	requireSameTime(t, oldDate, e.changedAt("cloudreve://my/a.txt"), "after tags")
}

func TestTrashKeepsBothDates(t *testing.T) {
	e := newSharedFolderEnv(t)
	owner := e.as(e.owner)
	e.upload(owner, "cloudreve://my/a.txt", 10)
	e.aged(owner, "cloudreve://my/a.txt")

	require.NoError(t, e.as(e.owner).SoftDelete(e.ctx, e.uri("cloudreve://my/a.txt")))
	trash, err := e.as(e.owner).AllFilesInTrashBin(e.ctx, fs.WithPageSize(100))
	require.NoError(t, err)
	require.Len(t, trash.Files, 1)

	// In the trash the date tells when it was deleted, and the clean-up still knows when to act.
	trashed := trash.Files[0]
	require.WithinDuration(t, time.Now(), trashed.UpdatedAt(), time.Minute, "deletion time")
	require.Contains(t, trashed.Metadata(), MetadataExpectedCollectTime)

	// Back from the trash, it has its old date again and no trash marks.
	require.NoError(t, e.as(e.owner).Restore(e.ctx, trashed.Uri(false)))
	requireSameTime(t, oldDate, e.changedAt("cloudreve://my/a.txt"), "after restore")
	restored, err := e.as(e.owner).Get(e.ctx, e.uri("cloudreve://my/a.txt"))
	require.NoError(t, err)
	require.NotContains(t, restored.Metadata(), MetadataRestoreModifiedAt)
	require.NotContains(t, restored.Metadata(), MetadataRestoreUri)
}

func TestNewVersionChangesTheDate(t *testing.T) {
	e := newSharedFolderEnv(t)
	owner := e.as(e.owner)
	e.upload(owner, "cloudreve://my/a.txt", 10)
	e.aged(owner, "cloudreve://my/a.txt")

	version := types.EntityTypeVersion
	f := e.as(e.owner)
	session, err := f.PrepareUpload(e.ctx, &fs.UploadRequest{
		Props: &fs.UploadProps{
			Uri:             e.uri("cloudreve://my/a.txt"),
			Size:            20,
			UploadSessionID: uuid.Must(uuid.NewV4()).String(),
			ExpireAt:        time.Now().Add(time.Hour),
			EntityType:      &version,
		},
	})
	require.NoError(t, err)
	_, err = f.CompleteUpload(e.ctx, session)
	require.NoError(t, err)
	require.WithinDuration(t, time.Now(), e.changedAt("cloudreve://my/a.txt"), time.Minute)
}

func TestOtherVersionChangesTheDate(t *testing.T) {
	e := newSharedFolderEnv(t)
	owner := e.as(e.owner)
	e.upload(owner, "cloudreve://my/a.txt", 10)
	e.aged(owner, "cloudreve://my/a.txt")

	file, err := e.as(e.owner).Get(e.ctx, e.uri("cloudreve://my/a.txt"), WithFileEntities())
	require.NoError(t, err)
	require.NoError(t, e.fileClient.SetPrimaryEntity(e.ctx, file.(*File).Model, file.Entities()[0].Model()))
	require.WithinDuration(t, time.Now(), e.changedAt("cloudreve://my/a.txt"), time.Minute)
}

func TestSortingByDateIgnoresRenames(t *testing.T) {
	e := newSharedFolderEnv(t)
	owner := e.as(e.owner)
	e.mkdir(owner, "cloudreve://my/Sort")
	e.upload(owner, "cloudreve://my/Sort/old.txt", 10)
	e.aged(owner, "cloudreve://my/Sort/old.txt")
	e.upload(owner, "cloudreve://my/Sort/new.txt", 10)

	_, _, err := e.as(e.owner).Rename(e.ctx, e.uri("cloudreve://my/Sort/old.txt"), "renamed.txt")
	require.NoError(t, err)

	_, res, err := e.as(e.owner).List(e.ctx, e.uri("cloudreve://my/Sort"), fs.WithPageSize(100),
		fs.WithOrderBy("updated_at"), fs.WithOrderDirection("desc"))
	require.NoError(t, err)
	names := make([]string, 0, len(res.Files))
	for _, f := range res.Files {
		names = append(names, f.Name())
	}
	require.Equal(t, []string{"new.txt", "renamed.txt"}, names)
}

func TestViewSettingsKeepTheDate(t *testing.T) {
	e := newSharedFolderEnv(t)
	owner := e.as(e.owner)
	e.mkdir(owner, "cloudreve://my/Fotos")
	e.aged(owner, "cloudreve://my/Fotos")

	require.NoError(t, e.as(e.owner).PatchProps(e.ctx, e.uri("cloudreve://my/Fotos"),
		&types.FileProps{View: &types.ExplorerView{PageSize: 100, View: "grid"}}, false))
	requireSameTime(t, oldDate, e.changedAt("cloudreve://my/Fotos"), "after view settings")
}

func TestFolderCopyKeepsTheDatesInside(t *testing.T) {
	e := newSharedFolderEnv(t)
	owner := e.as(e.owner)
	e.mkdir(owner, "cloudreve://my/Fotos")
	e.upload(owner, "cloudreve://my/Fotos/a.jpg", 10)
	e.aged(owner, "cloudreve://my/Fotos")
	e.aged(owner, "cloudreve://my/Fotos/a.jpg")
	e.mkdir(owner, "cloudreve://my/Backup")

	_, err := e.as(e.owner).MoveOrCopy(e.ctx, []*fs.URI{e.uri("cloudreve://my/Fotos")}, e.uri("cloudreve://my/Backup"), true)
	require.NoError(t, err)
	requireSameTime(t, oldDate, e.changedAt("cloudreve://my/Backup/Fotos"), "copied folder")
	requireSameTime(t, oldDate, e.changedAt("cloudreve://my/Backup/Fotos/a.jpg"), "file in copied folder")
}

func TestTrashedFolderComesBackWithItsDate(t *testing.T) {
	e := newSharedFolderEnv(t)
	owner := e.as(e.owner)
	e.mkdir(owner, "cloudreve://my/Fotos")
	e.upload(owner, "cloudreve://my/Fotos/a.jpg", 10)
	e.aged(owner, "cloudreve://my/Fotos")
	e.aged(owner, "cloudreve://my/Fotos/a.jpg")

	require.NoError(t, e.as(e.owner).SoftDelete(e.ctx, e.uri("cloudreve://my/Fotos")))
	trash, err := e.as(e.owner).AllFilesInTrashBin(e.ctx, fs.WithPageSize(100))
	require.NoError(t, err)
	require.Len(t, trash.Files, 1)
	require.NoError(t, e.as(e.owner).Restore(e.ctx, trash.Files[0].Uri(false)))

	requireSameTime(t, oldDate, e.changedAt("cloudreve://my/Fotos"), "folder")
	requireSameTime(t, oldDate, e.changedAt("cloudreve://my/Fotos/a.jpg"), "file inside")
}

func TestFileTrashedBeforeThisChangeStillComesBack(t *testing.T) {
	e := newSharedFolderEnv(t)
	owner := e.as(e.owner)
	e.upload(owner, "cloudreve://my/a.txt", 10)

	require.NoError(t, e.as(e.owner).SoftDelete(e.ctx, e.uri("cloudreve://my/a.txt")))
	trash, err := e.as(e.owner).AllFilesInTrashBin(e.ctx, fs.WithPageSize(100))
	require.NoError(t, err)
	require.Len(t, trash.Files, 1)
	trashed := trash.Files[0].(*File)
	// As if it had been deleted by the build before: no kept date.
	require.NoError(t, e.fileClient.RemoveMetadata(e.ctx, trashed.Model, MetadataRestoreModifiedAt))
	deletedAt := trashed.UpdatedAt()

	require.NoError(t, e.as(e.owner).Restore(e.ctx, trashed.Uri(false)))
	requireSameTime(t, deletedAt, e.changedAt("cloudreve://my/a.txt"), "keeps the deletion date")
}

func TestRestoredDateIsWrittenAsBefore(t *testing.T) {
	e := newSharedFolderEnv(t)
	owner := e.as(e.owner)
	e.upload(owner, "cloudreve://my/a.txt", 10)
	before := e.changedAt("cloudreve://my/a.txt")

	require.NoError(t, e.as(e.owner).SoftDelete(e.ctx, e.uri("cloudreve://my/a.txt")))
	trash, err := e.as(e.owner).AllFilesInTrashBin(e.ctx, fs.WithPageSize(100))
	require.NoError(t, err)
	require.NoError(t, e.as(e.owner).Restore(e.ctx, trash.Files[0].Uri(false)))

	// SQLite sorts dates as text: the same moment in another time zone would sort differently.
	after := e.changedAt("cloudreve://my/a.txt")
	require.True(t, before.Equal(after), "want %s, got %s", before, after)
	_, beforeOffset := before.Zone()
	_, afterOffset := after.Zone()
	require.Equal(t, beforeOffset, afterOffset)
}
