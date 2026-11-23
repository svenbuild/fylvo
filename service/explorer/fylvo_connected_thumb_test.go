package explorer

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/group"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/boolset"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider/drive/drivetest"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs/dbfs"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Fylvo: previews of files in a connected storage, from the route down to a simulated
// Google Drive.

func testPNG(shade uint8) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 6, 6))
	img.Set(1, 1, color.RGBA{shade, shade, 0, 255})
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		panic(err)
	}
	return out.Bytes()
}

func (e *connectedEnv) thumb(u *ent.User, uri string) (*FileThumbResponse, error) {
	return (&FileThumbService{Uri: uri}).Get(e.as(u))
}

// thumbOf asks for the preview of a file in the version the client's row has.
func (e *connectedEnv) thumbOf(u *ent.User, uri, entity string) (*FileThumbResponse, error) {
	return (&FileThumbService{Uri: uri, Entity: entity}).Get(e.as(u))
}

// picture loads the address of a preview, as a browser would: without a sign-in.
func (e *connectedEnv) picture(address string) *httptest.ResponseRecorder {
	e.t.Helper()
	u, err := url.Parse(address)
	require.NoError(e.t, err)
	require.True(e.t, strings.HasPrefix(u.Path, connectedThumbPath), u.Path)
	grant, name, _ := strings.Cut(strings.TrimPrefix(u.Path, connectedThumbPath), "/")
	c, rec := e.request(nil, http.MethodGet, u.EscapedPath())
	c.Params = gin.Params{{Key: "grant", Value: grant}, {Key: "name", Value: name}}
	ServeConnectedThumb(c)
	return rec
}

// reconnect stores a new token for Alex's connection, as a sign-in that renews it does:
// the connection goes on under its id, in a new generation.
func (e *connectedEnv) reconnect() {
	e.t.Helper()
	storages := inventory.NewConnectedStorageClient(e.client)
	row, err := storages.Get(e.ctx, e.alex.ID, e.id)
	require.NoError(e.t, err)
	_, ok, err := storages.Renew(e.ctx, row.ID, row.Generation, &inventory.RenewConnectedStorageArgs{
		Account: row.Account, Secret: row.Secret, Props: row.Props,
	})
	require.NoError(e.t, err)
	require.True(e.t, ok)
	e.m.Tree().Forget(e.id)
}

// keptPreviews are the files in the folder where previews are kept.
func (e *connectedEnv) keptPreviews() []string {
	var names []string
	_ = filepath.WalkDir(e.thumbDir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(e.thumbDir, path)
			names = append(names, filepath.ToSlash(rel))
		}
		return nil
	})
	return names
}

func TestConnectedThumbnail(t *testing.T) {
	e := newConnectedEnv(t)
	photo := e.sim.AddFile("", "photo.png", testPNG(1))
	e.sim.AddFile("", "notes.txt", []byte("notes"))
	e.sim.AddFolder("", "Photos")
	sheet := e.sim.AddNative("", "Budget", "gsheet")
	e.sim.SetThumbnail(sheet, testPNG(9))
	e.sim.AddShortcut("", "to photo", photo)

	// The listing tells for which rows a client asks: for every file that is not
	// marked as having no preview, whatever its kind.
	res, err := e.list(e.alex, e.root, 100, "")
	require.NoError(t, err)
	require.True(t, res.Connected.Thumbnails)
	asks := map[string]bool{}
	for _, f := range res.Files {
		_, disabled := f.Metadata[dbfs.ThumbDisabledKey]
		asks[f.Name] = !disabled && f.Type == int(types.FileTypeFile)
	}
	require.Equal(t, map[string]bool{"photo.png": true, "notes.txt": false, "Photos": false, "Budget.gsheet": true, "to photo": false}, asks)
	info, err := e.info(e.alex, e.root+"/photo.png")
	require.NoError(t, err)
	require.NotContains(t, info.Metadata, dbfs.ThumbDisabledKey)

	// The address: one of this server, the same for the whole day, and made without
	// asking the provider anything.
	before := e.sim.Calls(drivetest.CallGet)
	answer, err := e.thumb(e.alex, e.root+"/photo.png")
	require.NoError(t, err)
	address, err := url.Parse(answer.Url)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(address.Path, "/api/v4/fylvo/connected/thumb/"))
	require.True(t, strings.HasSuffix(address.Path, "/thumb.jpg"))
	require.Empty(t, address.RawQuery)
	require.NotContains(t, answer.Url, "photo")
	require.Equal(t, StableThumbExpire(time.Now()).Unix(), answer.Expires.Unix())
	again, err := e.thumb(e.alex, e.root+"/photo.png")
	require.NoError(t, err)
	require.Equal(t, answer.Url, again.Url)
	require.Equal(t, before, e.sim.Calls(drivetest.CallGet))
	require.Zero(t, e.sim.Calls(drivetest.CallThumbnail))
	require.Empty(t, e.keptPreviews(), "nothing is fetched before a client loads the picture")

	// The picture, for the client to keep for a day at most.
	rec := e.picture(answer.Url)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, testPNG(1), rec.Body.Bytes())
	require.Equal(t, "image/png", rec.Header().Get("Content-Type"))
	require.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	require.Regexp(t, `^private, max-age=(86400|8\d{4})$`, rec.Header().Get("Cache-Control"))
	require.Equal(t, []string{"s400"}, e.sim.ThumbnailSizes(), "in the size of the installation's thumbnails")
	// From then on it comes from disk: the provider is asked once per version.
	for i := 0; i < 3; i++ {
		rec = e.picture(answer.Url)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, testPNG(1), rec.Body.Bytes())
	}
	require.Equal(t, 1, e.sim.Calls(drivetest.CallThumbnail))
	kept := e.keptPreviews()
	require.Len(t, kept, 1)
	require.True(t, strings.HasPrefix(kept[0], e.id+"/"))

	// A document that exists only at Google has one too.
	answer, err = e.thumb(e.alex, e.root+"/Budget.gsheet")
	require.NoError(t, err)
	rec = e.picture(answer.Url)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, testPNG(9), rec.Body.Bytes())

	// A picture reached through a shortcut to a folder is not of the storage itself: no
	// preview, in the listing as in the answer.
	inside := e.sim.AddFolder("", "Album")
	e.sim.AddFile(inside, "inside.png", testPNG(4))
	e.sim.AddShortcut("", "to album", inside)
	e.m.Tree().Forget(e.id)
	through, err := e.list(e.alex, e.root+"/to album", 100, "")
	require.NoError(t, err)
	require.Len(t, through.Files, 1)
	require.Contains(t, through.Files[0].Metadata, dbfs.ThumbDisabledKey)
	_, err = e.thumb(e.alex, e.root+"/to album/inside.png")
	require.Equal(t, serializer.CodeEntityNotExist, connectedCode(err))
	direct, err := e.thumb(e.alex, e.root+"/Album/inside.png")
	require.NoError(t, err)
	require.Equal(t, testPNG(4), e.picture(direct.Url).Body.Bytes())

	// No preview: the answer that makes a client stop asking for that version.
	for _, path := range []string{"/notes.txt", "/Photos", "/to photo"} {
		_, err = e.thumb(e.alex, e.root+path)
		require.Equal(t, serializer.CodeEntityNotExist, connectedCode(err), path)
	}
	_, err = e.thumb(e.alex, e.root+"/missing.png")
	require.Equal(t, serializer.CodeParentNotExist, connectedCode(err))
	// Somebody else's storage does not exist for this account.
	_, err = e.thumb(e.sam, e.root+"/photo.png")
	require.Equal(t, serializer.CodeParentNotExist, connectedCode(err))
}

// A file the listing promised a preview of, and Drive then has none: the picture is
// "not found", and the next question for its address says so without asking Drive again.
func TestConnectedThumbnailThatIsNotThere(t *testing.T) {
	e := newConnectedEnv(t)
	photo := e.sim.AddFile("", "photo.png", testPNG(1))
	answer, err := e.thumb(e.alex, e.root+"/photo.png")
	require.NoError(t, err)
	e.sim.SetThumbnail(photo, nil)
	asked := e.sim.Calls(drivetest.CallGet)

	for i := 0; i < 3; i++ {
		rec := e.picture(answer.Url)
		require.Equal(t, http.StatusNotFound, rec.Code)
		require.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"))
		require.Empty(t, rec.Body.Bytes())
	}
	require.Equal(t, asked+1, e.sim.Calls(drivetest.CallGet), "asked once, then remembered")
	_, err = e.thumb(e.alex, e.root+"/photo.png")
	require.Equal(t, serializer.CodeEntityNotExist, connectedCode(err))
	require.Empty(t, e.keptPreviews())

	// The provider is slow or says "later": nothing is remembered, the client comes back.
	other := e.sim.AddFile("", "other.png", testPNG(2))
	_ = other
	answer, err = e.thumb(e.alex, e.root+"/other.png")
	require.NoError(t, err)
	e.sim.RefuseThumbnails(http.StatusTooManyRequests)
	rec := e.picture(answer.Url)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Equal(t, "5", rec.Header().Get("Retry-After"))
	require.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"))
	e.sim.RefuseThumbnails(0)
	rec = e.picture(answer.Url)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, testPNG(2), rec.Body.Bytes())
}

// The connection ends while its picture is on the way from the provider: the request
// that was let in before gets nothing, and nothing is kept.
func TestConnectedThumbnailEndsWithTheConnectionMidway(t *testing.T) {
	e := newConnectedEnv(t)
	e.sim.AddFile("", "photo.png", testPNG(1))
	answer, err := e.thumb(e.alex, e.root+"/photo.png")
	require.NoError(t, err)
	var once sync.Once
	ended := make(chan error, 1)
	e.sim.OnThumbnail(func() {
		// The simulated Drive is in the middle of this call: the connection ends now,
		// and the provider answers after it is gone.
		once.Do(func() { ended <- e.m.Disconnect(e.ctx, e.alex.ID, e.id) })
	})
	rec := e.picture(answer.Url)
	require.NoError(t, <-ended)
	require.Equal(t, 1, e.sim.Calls(drivetest.CallThumbnail), "the provider was asked, and answered")
	require.NotEqual(t, http.StatusOK, rec.Code)
	require.Empty(t, rec.Body.Bytes())
	require.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"))
	require.Empty(t, e.keptPreviews())
}

// The address of a preview is for one state of one file of one account. It ends with
// the password of the account, with the right of its group, and with the connection.
func TestConnectedThumbnailAddressIsAGrant(t *testing.T) {
	e := newConnectedEnv(t)
	photo := e.sim.AddFile("", "photo.png", testPNG(1))
	e.sim.AddFile("", "second.png", testPNG(2))
	answer, err := e.thumb(e.alex, e.root+"/photo.png")
	require.NoError(t, err)
	second, err := e.thumb(e.alex, e.root+"/second.png")
	require.NoError(t, err)
	require.NotEqual(t, answer.Url, second.Url)
	require.Equal(t, http.StatusOK, e.picture(answer.Url).Code)
	require.Equal(t, testPNG(2), e.picture(second.Url).Body.Bytes())

	// Not the address of a download, and the other way round.
	download, err := e.urlOf(e.alex, e.root+"/photo.png", false)
	require.NoError(t, err)
	swapped := strings.Replace(download, connectedContentPath, connectedThumbPath, 1)
	require.Equal(t, http.StatusNotFound, e.picture(swapped).Code)
	require.Equal(t, http.StatusNotFound, e.fetch(strings.Replace(answer.Url, connectedThumbPath, connectedContentPath, 1), "").Code)
	// Nothing made up, and nothing changed.
	base := answer.Url[:strings.LastIndex(answer.Url, "/")]
	for _, bad := range []string{"", "AAAA", base[len(base)-40:], strings.Repeat("A", 200)} {
		prefix := answer.Url[:strings.Index(answer.Url, connectedThumbPath)+len(connectedThumbPath)]
		require.Equal(t, http.StatusNotFound, e.picture(prefix+bad+"/thumb.jpg").Code, bad)
	}
	flipped := []byte(base)
	flipped[len(flipped)-10] ^= 1
	require.Equal(t, http.StatusNotFound, e.picture(string(flipped)+"/thumb.jpg").Code)
	// The name at the end is for the client only.
	require.Equal(t, http.StatusOK, e.picture(base+"/whatever.png").Code)

	// New content: the listing tells a new state, and the address is another one. The
	// old address goes on showing the old state.
	e.sim.SetContent(photo, testPNG(3))
	e.m.Tree().Forget(e.id)
	changed, err := e.thumb(e.alex, e.root+"/photo.png")
	require.NoError(t, err)
	require.NotEqual(t, answer.Url, changed.Url)
	require.Equal(t, testPNG(3), e.picture(changed.Url).Body.Bytes())
	require.Equal(t, testPNG(1), e.picture(answer.Url).Body.Bytes())

	// The group loses the right to use connected storages: the pictures on disk stay
	// where they are and are handed to nobody.
	g, err := e.client.Group.Query().Where(group.ID(2)).First(e.ctx)
	require.NoError(t, err)
	permissions := make(boolset.BooleanSet, len(*g.Permissions))
	copy(permissions, *g.Permissions)
	boolset.Set(types.GroupPermissionConnectedStorage, false, &permissions)
	require.NoError(t, e.client.Group.Update().Where(group.ID(2)).SetPermissions(&permissions).Exec(e.ctx))
	require.Equal(t, http.StatusNotFound, e.picture(answer.Url).Code)
	boolset.Set(types.GroupPermissionConnectedStorage, true, &permissions)
	require.NoError(t, e.client.Group.Update().Where(group.ID(2)).SetPermissions(&permissions).Exec(e.ctx))
	require.Equal(t, http.StatusOK, e.picture(answer.Url).Code)

	// A new password ends every address made before it.
	require.NoError(t, e.client.User.UpdateOneID(e.alex.ID).SetPassword("another-hash").Exec(e.ctx))
	require.Equal(t, http.StatusNotFound, e.picture(answer.Url).Code)
	require.Equal(t, http.StatusNotFound, e.picture(changed.Url).Code)
	e.alex = e.reload(e.alex.ID)
	fresh, err := e.thumb(e.alex, e.root+"/photo.png")
	require.NoError(t, err)
	require.NotEqual(t, changed.Url, fresh.Url)
	require.Equal(t, http.StatusOK, e.picture(fresh.Url).Code)

	// Connected again: the address was for the connection as it was before. The picture
	// stays on disk for the address of the new one.
	before := e.sim.Calls(drivetest.CallThumbnail)
	_, err = e.m.Create(e.ctx, e.sam.ID, connected.ProviderDrive, &connected.Identity{Key: "account-9", Label: "x"}, connectedTestToken)
	require.NoError(t, err)
	e.reconnect()
	require.Equal(t, http.StatusNotFound, e.picture(fresh.Url).Code)
	fresh, err = e.thumb(e.alex, e.root+"/photo.png")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, e.picture(fresh.Url).Code)
	require.Equal(t, before, e.sim.Calls(drivetest.CallThumbnail), "served from disk, not fetched again")

	// The connection ends: at once the address is worth nothing, also for a picture
	// that was on disk, and what was kept of the storage is gone.
	require.NotEmpty(t, e.keptPreviews())
	require.NoError(t, e.m.Disconnect(e.ctx, e.alex.ID, e.id))
	require.Equal(t, http.StatusNotFound, e.picture(fresh.Url).Code)
	require.Empty(t, e.keptPreviews())
	_, err = os.Stat(filepath.Join(e.thumbDir, e.id))
	require.True(t, os.IsNotExist(err))
}

// A client can name the version of the row it shows. It gets the preview of that version
// or is told that its row is old, never the picture of another version.
func TestConnectedThumbnailOfTheVersionShown(t *testing.T) {
	e := newConnectedEnv(t)
	photo := e.sim.AddFile("", "photo.png", testPNG(1))
	e.sim.AddFile("", "notes.txt", []byte("notes"))
	e.sim.AddFolder("", "Photos")

	entity := func(name string) string {
		t.Helper()
		res, err := e.list(e.alex, e.root, 100, "")
		require.NoError(t, err)
		for _, f := range res.Files {
			if f.Name == name {
				return f.PrimaryEntity
			}
		}
		t.Fatalf("no row %q", name)
		return ""
	}
	shown := entity("photo.png")
	require.NotEmpty(t, shown)

	// The version the server has too: the same address as without naming it.
	plain, err := e.thumb(e.alex, e.root+"/photo.png")
	require.NoError(t, err)
	named, err := e.thumbOf(e.alex, e.root+"/photo.png", shown)
	require.NoError(t, err)
	require.Equal(t, plain.Url, named.Url)

	// The file changes at the provider, and the server reads the folder again while the
	// client still shows its old row.
	e.sim.SetContent(photo, testPNG(2))
	e.m.Tree().Forget(e.id)
	_, err = e.thumbOf(e.alex, e.root+"/photo.png", shown)
	require.Equal(t, serializer.CodeStaleVersion, connectedCode(err))
	require.Empty(t, e.keptPreviews(), "and nothing is fetched for it")
	// Without a version the client gets what is there now, as before.
	latest, err := e.thumb(e.alex, e.root+"/photo.png")
	require.NoError(t, err)
	require.NotEqual(t, plain.Url, latest.Url)
	// After listing again it names the new version and gets that address.
	renewed := entity("photo.png")
	require.NotEqual(t, shown, renewed)
	named, err = e.thumbOf(e.alex, e.root+"/photo.png", renewed)
	require.NoError(t, err)
	require.Equal(t, latest.Url, named.Url)
	require.Equal(t, testPNG(2), e.picture(named.Url).Body.Bytes())

	// A version that is not this file's is old too, whatever else is true of the row:
	// a made-up one, that of another file, one for a row that has none.
	_, err = e.thumbOf(e.alex, e.root+"/photo.png", "made-up")
	require.Equal(t, serializer.CodeStaleVersion, connectedCode(err))
	_, err = e.thumbOf(e.alex, e.root+"/photo.png", entity("notes.txt"))
	require.Equal(t, serializer.CodeStaleVersion, connectedCode(err))
	_, err = e.thumbOf(e.alex, e.root+"/Photos", renewed)
	require.Equal(t, serializer.CodeStaleVersion, connectedCode(err))
	// A file without a preview says so when the version is right.
	_, err = e.thumbOf(e.alex, e.root+"/notes.txt", entity("notes.txt"))
	require.Equal(t, serializer.CodeEntityNotExist, connectedCode(err))
	// What is not there, or is somebody else's, answers as without a version.
	_, err = e.thumbOf(e.alex, e.root+"/missing.png", renewed)
	require.Equal(t, serializer.CodeParentNotExist, connectedCode(err))
	_, err = e.thumbOf(e.sam, e.root+"/photo.png", renewed)
	require.Equal(t, serializer.CodeParentNotExist, connectedCode(err))
}
