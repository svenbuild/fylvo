package connected

import (
	"context"
	"net/http"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider/graph"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider/graph/graphtest"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/stretchr/testify/require"
)

// oneDriveConn connects account 7 to a simulated OneDrive and admits a request to it.
func oneDriveConn(t *testing.T) (*managerFixture, *graphtest.Server, *Conn, context.Context) {
	f := newManagerFixture(t)
	ctx := context.Background()
	secret := "client-secret"
	require.NoError(t, f.SetProvider(ctx, ProviderDrive, "client-id", &secret))
	s := graphtest.New(t)
	f.specs[ProviderDrive].NewProvider = func(_ *http.Client, auth provider.Auth, _ map[string]string) provider.Provider {
		return graph.New(graph.Options{Auth: auth, DriveID: s.DriveID(), BaseURL: s.URL()})
	}
	storage, err := f.connect(t, 7, "")
	require.NoError(t, err)
	c, opCtx, done, err := f.Use(ctx, 7, storage.ID)
	require.NoError(t, err)
	t.Cleanup(done)
	return f, s, c, opCtx
}

// source is what a client sends for an entry: its path and the id of its row.
func source(t *testing.T, f *managerFixture, ctx context.Context, c *Conn, path string) Source {
	row, err := f.Tree().Resolve(ctx, c, path)
	require.NoError(t, err)
	return Source{Path: path, ID: FileID(c.ID, row)}
}

// OneDrive keeps names apart without regard to case. A name that is taken in another
// spelling is told as such, and nothing is created, renamed or moved onto it.
func TestWritesWhereCaseDoesNotCount(t *testing.T) {
	f, s, c, ctx := oneDriveConn(t)
	s.AddFile("", "notes.txt", []byte("hello"))

	docs, err := f.CreateEntry(ctx, c, "Docs", true, true)
	require.NoError(t, err)
	require.True(t, docs.IsDir)
	_, err = f.CreateEntry(ctx, c, "docs", true, false)
	require.Equal(t, serializer.CodeNameCaseTaken, codeOf(Error(ctx, err)))
	_, err = f.CreateEntry(ctx, c, "NOTES.TXT", false, false)
	require.Equal(t, serializer.CodeNameCaseTaken, codeOf(Error(ctx, err)))
	// The same name in the same spelling is the entry that is there.
	again, err := f.CreateEntry(ctx, c, "Docs", true, false)
	require.NoError(t, err)
	require.Equal(t, docs.ID, again.ID)

	// An entry may change the spelling of its own name.
	renamed, err := f.RenameEntry(ctx, c, source(t, f, ctx, c, "notes.txt"), "Notes.txt")
	require.NoError(t, err)
	require.Equal(t, "Notes.txt", renamed.Name)
	// But not take the name of another entry in another spelling.
	_, err = f.CreateEntry(ctx, c, "other.txt", false, true)
	require.NoError(t, err)
	_, err = f.RenameEntry(ctx, c, source(t, f, ctx, c, "other.txt"), "NOTES.txt")
	require.Equal(t, serializer.CodeNameCaseTaken, codeOf(Error(ctx, err)))

	// Moving into a folder that has the name in another spelling.
	_, err = f.CreateEntry(ctx, c, "Docs/OTHER.txt", false, true)
	require.NoError(t, err)
	failed, err := f.MoveEntries(ctx, c, []Source{source(t, f, ctx, c, "other.txt")}, Source{Path: "Docs"})
	require.NoError(t, err)
	require.Equal(t, serializer.CodeNameCaseTaken, codeOf(Error(ctx, failed[0])))
	// And one that is free there.
	failed, err = f.MoveEntries(ctx, c, []Source{source(t, f, ctx, c, "Notes.txt")}, Source{Path: "Docs"})
	require.NoError(t, err)
	require.Equal(t, []error{nil}, failed)
	moved, err := f.Tree().Resolve(ctx, c, "Docs/Notes.txt")
	require.NoError(t, err)
	require.Equal(t, renamed.ID, moved.ID, "the entry is the same one after the move")

	// Into the trash of the provider.
	failed, err = f.TrashEntries(ctx, c, []Source{{Path: "Docs/Notes.txt", ID: FileID(c.ID, moved)}, {Path: "nothing.txt", ID: "x"}})
	require.NoError(t, err)
	require.NoError(t, failed[0])
	require.ErrorIs(t, failed[1], ErrNotFound)
	_, err = f.Tree().Resolve(ctx, c, "Docs/Notes.txt")
	require.ErrorIs(t, err, ErrNotFound)
}

// Names Fylvo makes up for entries without bytes ("Notes.onenote") are names like any
// other where case does not count: nothing is created beside one in another spelling,
// and a folder is not moved into itself under another spelling of its name.
func TestMadeUpNamesWhereCaseDoesNotCount(t *testing.T) {
	f, s, c, ctx := oneDriveConn(t)
	s.AddNotebook("", "Notes")

	_, err := f.CreateEntry(ctx, c, "Notes.ONENOTE", false, true)
	require.Equal(t, serializer.CodeNameCaseTaken, codeOf(Error(ctx, err)))
	_, err = f.CreateEntry(ctx, c, "Notes.onenote", false, true)
	require.Equal(t, serializer.CodeObjectExist, codeOf(Error(ctx, err)))

	_, err = f.CreateEntry(ctx, c, "Docs", true, true)
	require.NoError(t, err)
	_, err = f.CreateEntry(ctx, c, "Docs/Old", true, true)
	require.NoError(t, err)
	results, err := f.MoveEntries(ctx, c, []Source{source(t, f, ctx, c, "Docs")}, Source{Path: "docs/old"})
	require.NoError(t, err)
	require.Equal(t, serializer.CodeParamErr, codeOf(Error(ctx, results[0])))
}
