package dbfs

import (
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/file"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
)

// Fylvo: names that differ only in upper and lower case. Windows cannot hold both in one
// folder, so the server refuses the second one. A rename of an entry that only changes its
// case stays allowed, and twins that are there already (old data, imports) stay usable.

// legacyTwin puts a file next to an existing one in the database directly, the way twins
// came in before this check: imports from Linux, old data.
func (e *sharedFolderEnv) legacyTwin(folder, name string, fileType types.FileType) {
	parent, err := e.as(e.owner).Get(e.ctx, e.uri(folder))
	require.NoError(e.t, err)
	_, err = e.client.File.Create().SetName(name).SetOwnerID(e.owner.ID).SetType(int(fileType)).
		SetParentID(parent.ID()).SetSize(0).Save(e.ctx)
	require.NoError(e.t, err)
}

func (e *sharedFolderEnv) rename(f fs.FileSystem, raw, name string) error {
	_, _, err := f.Rename(e.ctx, e.uri(raw), name)
	return err
}

func (e *sharedFolderEnv) moveOrCopy(f fs.FileSystem, raws []string, dst string, isCopy bool) error {
	uris := make([]*fs.URI, 0, len(raws))
	for _, raw := range raws {
		uris = append(uris, e.uri(raw))
	}
	_, err := f.MoveOrCopy(e.ctx, uris, e.uri(dst), isCopy)
	return err
}

func TestCreateRefusesACaseTwin(t *testing.T) {
	e := newSharedFolderEnv(t)
	owner := e.as(e.owner)

	_, err := owner.Create(e.ctx, e.uri("cloudreve://my/Shared/DOC.txt"), types.FileTypeFile)
	requireCode(t, err, serializer.CodeNameCaseTaken)
	_, err = owner.Create(e.ctx, e.uri("cloudreve://my/Shared/SUB"), types.FileTypeFolder)
	requireCode(t, err, serializer.CodeNameCaseTaken)
	// A path through a twin folder is refused as well.
	_, err = owner.Create(e.ctx, e.uri("cloudreve://my/Shared/Sub/new.txt"), types.FileTypeFile)
	requireCode(t, err, serializer.CodeNameCaseTaken)

	// The same name is what it was: an existing folder is simply used, a file is "existed".
	_, err = owner.Create(e.ctx, e.uri("cloudreve://my/Shared/sub"), types.FileTypeFolder)
	require.NoError(t, err)
	_, err = owner.Create(e.ctx, e.uri("cloudreve://my/Shared/doc.txt"), types.FileTypeFile, WithErrorOnConflict())
	requireCode(t, err, serializer.CodeObjectExist)

	// Umlauts: Ä and ä are one name, the composed and the decomposed ü are two (as on NTFS).
	_, err = owner.Create(e.ctx, e.uri("cloudreve://my/Shared/Bär.txt"), types.FileTypeFile)
	require.NoError(t, err)
	_, err = owner.Create(e.ctx, e.uri("cloudreve://my/Shared/BÄR.txt"), types.FileTypeFile)
	requireCode(t, err, serializer.CodeNameCaseTaken)
	_, err = owner.Create(e.ctx, e.uri("cloudreve://my/Shared/Müller.txt"), types.FileTypeFile)
	require.NoError(t, err)
	_, err = owner.Create(e.ctx, e.uri("cloudreve://my/Shared/Müller.txt"), types.FileTypeFile)
	require.NoError(t, err)
}

func TestUploadRefusesACaseTwin(t *testing.T) {
	e := newSharedFolderEnv(t)

	requireCode(t, e.tryUpload(e.as(e.owner), e.uri("cloudreve://my/Shared/Doc.txt"), 1, nil), serializer.CodeNameCaseTaken)
	requireCode(t, e.tryUpload(e.as(e.editor), e.shared("/Doc.txt"), 1, nil), serializer.CodeNameCaseTaken)
	requireCode(t, e.importFile(e.as(e.owner), "cloudreve://my/Shared/DOC.TXT", 1, diskDate), serializer.CodeNameCaseTaken)

	names, err := e.names(e.as(e.owner), e.uri("cloudreve://my/Shared"))
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"sub", "doc.txt"}, names)
}

func TestUploadLinkVisitorGetsAFreeName(t *testing.T) {
	e := newSharedFolderEnv(t)
	_, at := e.uploadLink(&types.ShareProps{AllowUpload: true})
	visitor := e.visitors()[1]

	// `Doc.txt` is taken as `doc.txt`, `Doc (1).txt` as `doc (1).txt`.
	e.upload(e.as(e.owner), "cloudreve://my/Shared/doc (1).txt", 1)
	require.NoError(t, e.tryUpload(e.as(visitor), at("/Doc.txt"), 1, nil))
	_, err := e.as(visitor).Create(e.ctx, at("/DOC.txt"), types.FileTypeFile)
	require.NoError(t, err)

	names, err := e.names(e.as(e.owner), e.uri("cloudreve://my/Shared"))
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"sub", "doc.txt", "doc (1).txt", "Doc (2).txt", "DOC (3).txt"}, names)
}

func TestRenameAndCaseTwins(t *testing.T) {
	e := newSharedFolderEnv(t)
	owner := e.as(e.owner)
	e.upload(owner, "cloudreve://my/Shared/foto.jpg", 1)

	// Only the case of the same entry changes: allowed.
	require.NoError(t, e.rename(owner, "cloudreve://my/Shared/foto.jpg", "Foto.jpg"))
	require.NoError(t, e.rename(owner, "cloudreve://my/Shared/sub", "Sub"))

	// Onto another entry in another spelling: refused.
	requireCode(t, e.rename(owner, "cloudreve://my/Shared/Foto.jpg", "DOC.txt"), serializer.CodeNameCaseTaken)
	_, _, err := e.as(e.editor).Rename(e.ctx, e.shared("/Foto.jpg"), "Doc.TXT")
	requireCode(t, err, serializer.CodeNameCaseTaken)
}

func TestMoveAndCopyRefuseCaseTwins(t *testing.T) {
	e := newSharedFolderEnv(t)
	owner := e.as(e.owner)
	e.upload(owner, "cloudreve://my/Private/DOC.txt", 1)
	e.upload(owner, "cloudreve://my/Private/a.jpg", 1)

	for _, isCopy := range []bool{false, true} {
		requireCode(t, e.moveOrCopy(owner, []string{"cloudreve://my/Private/DOC.txt"}, "cloudreve://my/Shared", isCopy), serializer.CodeNameCaseTaken)
	}

	// Two entries that are twins of each other cannot go into one folder together.
	e.legacyTwin("cloudreve://my/Private", "A.jpg", types.FileTypeFile)
	requireCode(t, e.moveOrCopy(owner, []string{"cloudreve://my/Private/a.jpg", "cloudreve://my/Private/A.jpg"}, "cloudreve://my/Shared/sub", false), serializer.CodeNameCaseTaken)

	// A folder that holds twins moves as a whole, but is not copied: the copy would make new twins.
	require.NoError(t, e.moveOrCopy(owner, []string{"cloudreve://my/Private"}, "cloudreve://my/Shared/sub", false))
	requireCode(t, e.moveOrCopy(owner, []string{"cloudreve://my/Shared/sub/Private"}, "cloudreve://my/Shared", true), serializer.CodeNameCaseTaken)

	names, err := e.names(owner, e.uri("cloudreve://my/Shared"))
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"sub", "doc.txt"}, names)
}

func TestRestoreNextToACaseTwinIsRefused(t *testing.T) {
	e := newSharedFolderEnv(t)
	owner := e.as(e.owner)
	require.NoError(t, owner.SoftDelete(e.ctx, e.uri("cloudreve://my/Shared/doc.txt")))
	e.upload(owner, "cloudreve://my/Shared/DOC.txt", 2)

	_, res, err := owner.List(e.ctx, e.uri("cloudreve://trash"), fs.WithPageSize(10))
	require.NoError(t, err)
	require.Len(t, res.Files, 1)
	// Restore answers with an error per file, as it does for a name that is taken exactly.
	err = owner.Restore(e.ctx, res.Files[0].Uri(false))
	var aggregate *serializer.AggregateError
	require.ErrorAs(t, err, &aggregate)
	for _, inner := range aggregate.Raw() {
		requireCode(t, inner, serializer.CodeNameCaseTaken)
	}

	// It stays in the trash, and the file in its place stays as it is.
	_, res, err = owner.List(e.ctx, e.uri("cloudreve://trash"), fs.WithPageSize(10))
	require.NoError(t, err)
	require.Len(t, res.Files, 1)
	file, err := owner.Get(e.ctx, e.uri("cloudreve://my/Shared/DOC.txt"))
	require.NoError(t, err)
	require.EqualValues(t, 2, file.Size())
}

func TestExistingTwinsStayUsable(t *testing.T) {
	e := newSharedFolderEnv(t)
	owner := e.as(e.owner)
	e.legacyTwin("cloudreve://my/Shared", "Doc.txt", types.FileTypeFile)
	e.legacyTwin("cloudreve://my/Shared", "SUB", types.FileTypeFolder)
	e.mkdir(owner, "cloudreve://my/Shared/SUB/inner")

	// Both are listed and can be read.
	names, err := e.names(owner, e.uri("cloudreve://my/Shared"))
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"sub", "SUB", "doc.txt", "Doc.txt"}, names)
	_, err = owner.Get(e.ctx, e.uri("cloudreve://my/Shared/Doc.txt"))
	require.NoError(t, err)

	// Other files come and go next to them, also into the twin folders.
	e.upload(owner, "cloudreve://my/Shared/other.txt", 1)
	e.upload(owner, "cloudreve://my/Shared/sub/x.txt", 1)
	e.upload(owner, "cloudreve://my/Shared/SUB/inner/y.txt", 1)
	_, err = owner.Create(e.ctx, e.uri("cloudreve://my/Shared/SUB/inner"), types.FileTypeFolder)
	require.NoError(t, err)

	// A new version of a twin works.
	version := types.EntityTypeVersion
	require.NoError(t, e.tryUploadVersion(owner, e.uri("cloudreve://my/Shared/Doc.txt"), 3, &version))

	// The admin may change a twin, but not give a name another spelling of one in its folder.
	twin, err := e.client.File.Query().Where(file.Name("doc.txt")).Only(e.ctx)
	require.NoError(t, err)
	_, err = e.fileClient.Update(e.ctx, twin)
	require.NoError(t, err)
	other, err := e.client.File.Query().Where(file.Name("other.txt")).Only(e.ctx)
	require.NoError(t, err)
	other.Name = "OTHER.txt"
	_, err = e.fileClient.Update(e.ctx, other)
	require.NoError(t, err)
	// In a transaction, as the admin service does: the refused name is rolled back.
	fc, tx, ctx, err := inventory.WithTx(e.ctx, e.fileClient)
	require.NoError(t, err)
	other.Name = "DOC.TXT"
	_, err = fc.Update(ctx, other)
	requireCode(t, err, serializer.CodeNameCaseTaken)
	require.NoError(t, inventory.Rollback(tx))
	_, err = owner.Get(e.ctx, e.uri("cloudreve://my/Shared/OTHER.txt"))
	require.NoError(t, err)

	// A move within its own folder (WebDAV moves first, then renames) brings no new name there,
	// also for a twin.
	legacy, err := e.client.File.Query().Where(file.Name("Doc.txt")).Only(e.ctx)
	require.NoError(t, err)
	parent, err := e.client.File.Get(e.ctx, legacy.FileChildren)
	require.NoError(t, err)
	require.NoError(t, e.fileClient.SetParent(e.ctx, []*ent.File{legacy}, parent))

	// A twin can be renamed to a free name, not to another spelling of its twin.
	requireCode(t, e.rename(owner, "cloudreve://my/Shared/Doc.txt", "DOC.txt"), serializer.CodeNameCaseTaken)
	require.NoError(t, e.rename(owner, "cloudreve://my/Shared/Doc.txt", "Doc 2.txt"))
}

func TestRacingUploadsMakeNoTwins(t *testing.T) {
	e := newSharedFolderEnv(t)

	// Between the read and the lock of the first upload, a second one creates the other spelling.
	// The locks are on different paths, so only the check in the transaction stops it.
	first := e.asInterleaved(e.owner, "Foto.jpg", func() {
		require.NoError(t, e.tryUpload(e.as(e.editor), e.shared("/foto.jpg"), 1, nil))
	})
	session, err := first.PrepareUpload(e.ctx, &fs.UploadRequest{Props: &fs.UploadProps{
		Uri: e.uri("cloudreve://my/Shared/Foto.jpg"), Size: 1, UploadSessionID: uuid.Must(uuid.NewV4()).String(), ExpireAt: time.Now().Add(time.Hour),
	}})
	require.Nil(t, session)
	requireCode(t, err, serializer.CodeNameCaseTaken)

	names, err := e.names(e.as(e.owner), e.uri("cloudreve://my/Shared"))
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"sub", "doc.txt", "foto.jpg"}, names)
}
