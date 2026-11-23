package explorer

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/group"
	"github.com/cloudreve/Cloudreve/v4/ent/user"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/boolset"
	"github.com/cloudreve/Cloudreve/v4/pkg/cache"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/connectedtest"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider/drive"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider/drive/drivetest"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs/dbfs"
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/pkg/util"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Fylvo: the file routes of a connected storage, from the route down to a simulated
// Google Drive.

type connectedEnv struct {
	t      *testing.T
	ctx    context.Context
	dep    dependency.Dep
	client *ent.Client
	m      *connected.Manager
	sim    *drivetest.Server

	alex, sam *ent.User
	// root is the address of the root of the storage Alex connected.
	root string
	id   string
	// thumbDir is where previews fetched from the provider are kept.
	thumbDir string
	// officeHeaders are sent with the calls of the Office server (see office).
	officeHeaders map[string]string
}

const connectedTestToken = `{"access_token":"access-1","token_type":"Bearer","refresh_token":"refresh-1","expiry":"2099-01-01T00:00:00Z"}`

func newConnectedEnv(t *testing.T) *connectedEnv {
	logger := logging.NewConsoleLogger(logging.LevelError)
	kv := cache.NewMemoStore("", logger)
	client := connectedtest.Database(t, logger, kv)
	hasher, err := hashid.New("fylvo-test")
	require.NoError(t, err)
	dep := dependency.NewDependency(
		dependency.WithLogger(logger),
		dependency.WithConfigPath(filepath.Join(t.TempDir(), "conf.ini")),
		dependency.WithDbClient(client),
		dependency.WithKV(kv),
		dependency.WithHashIDEncoder(hasher),
	)
	ctx := context.WithValue(context.Background(), dependency.DepCtx{}, dep)
	ctx = context.WithValue(ctx, logging.LoggerCtx{}, logger)

	e := &connectedEnv{t: t, ctx: ctx, dep: dep, client: client, sim: drivetest.New(t), thumbDir: filepath.Join(t.TempDir(), "thumbs")}
	specs := connected.Specs()
	specs[connected.ProviderDrive].NewProvider = func(_ *http.Client, auth provider.Auth, _ map[string]string) provider.Provider {
		return drive.New(drive.Options{HTTP: e.sim.Client(), Auth: auth, BaseURL: e.sim.URL()})
	}
	specs[connected.ProviderDrive].Revoke = nil
	e.m = connected.NewManager(connected.ManagerOptions{
		Storages: inventory.NewConnectedStorageClient(client),
		Settings: connected.DepSettings(dep),
		Sealer:   connected.DepSealer(dep),
		Specs:    specs,
		HTTP:     e.sim.Client(),
		ThumbDir: e.thumbDir,
	})
	connected.Install(dep, e.m)

	e.alex = e.newUser("alex")
	e.sam = e.newUser("sam")
	storage, err := e.m.Create(ctx, e.alex.ID, connected.ProviderDrive,
		&connected.Identity{Key: "account-1", Label: "user@example.com"}, connectedTestToken)
	require.NoError(t, err)
	e.root, e.id = storage.URI, storage.ID
	return e
}

func (e *connectedEnv) newUser(name string) *ent.User {
	created, err := e.client.User.Create().SetEmail(name + "@example.com").SetNick(name).SetPassword("hash-of-" + name).
		SetStatus(user.StatusActive).SetGroupID(2).SetSettings(&types.UserSetting{}).Save(e.ctx)
	require.NoError(e.t, err)
	return e.reload(created.ID)
}

func (e *connectedEnv) reload(id int) *ent.User {
	loaded, err := e.dep.UserClient().GetByID(context.WithValue(e.ctx, inventory.LoadUserGroup{}, true), id)
	require.NoError(e.t, err)
	return loaded
}

// as returns a request of u.
func (e *connectedEnv) as(u *ent.User) *gin.Context {
	c, _ := e.request(u, http.MethodGet, "/")
	return c
}

func (e *connectedEnv) request(u *ent.User, method, target string) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, engine := gin.CreateTestContext(rec)
	engine.ContextWithFallback = true
	c.Request = httptest.NewRequest(method, target, nil)
	util.WithValue(c, dependency.DepCtx{}, e.dep)
	if u != nil {
		util.WithValue(c, inventory.UserCtx{}, u)
	}
	return c, rec
}

func (e *connectedEnv) list(u *ent.User, uri string, size int, token string) (*ListResponse, error) {
	return (&ListFileService{Uri: uri, PageSize: size, OrderBy: "name", NextPageToken: token}).List(e.as(u))
}

func (e *connectedEnv) info(u *ent.User, uri string) (*FileResponse, error) {
	return (&GetFileInfoService{Uri: uri, ExtendedInfo: true}).Get(e.as(u))
}

// fetch requests the content behind a download address, as a browser would: without a
// sign-in, with nothing but the address.
func (e *connectedEnv) fetch(address, rangeHeader string) *httptest.ResponseRecorder {
	u, err := url.Parse(address)
	require.NoError(e.t, err)
	require.True(e.t, strings.HasPrefix(u.Path, connectedContentPath), u.Path)
	grant, name, _ := strings.Cut(strings.TrimPrefix(u.Path, connectedContentPath), "/")

	c, rec := e.request(nil, http.MethodGet, u.EscapedPath())
	if rangeHeader != "" {
		c.Request.Header.Set("Range", rangeHeader)
	}
	c.Params = gin.Params{{Key: "grant", Value: grant}, {Key: "name", Value: name}}
	ServeConnectedContent(c)
	return rec
}

func (e *connectedEnv) urlOf(u *ent.User, uri string, download bool) (string, error) {
	res, err := (&FileURLService{Uris: []string{uri}, Download: download}).Get(e.as(u))
	if err != nil {
		return "", err
	}
	return res.Urls[0].Url, nil
}

func connectedCode(err error) int {
	var app serializer.AppError
	if errors.As(err, &app) {
		return app.Code
	}
	return -1
}

func fileNames(files []FileResponse) string {
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, f.Name)
	}
	return strings.Join(names, "|")
}

func TestConnectedListAndInfo(t *testing.T) {
	e := newConnectedEnv(t)
	docs := e.sim.AddFolder("", "Docs")
	e.sim.AddFile("", "notes.txt", []byte("hello"))
	e.sim.AddNative("", "Budget", "gsheet")
	e.sim.AddFile("", "twice.txt", []byte("a"))
	e.sim.AddFile("", "twice.txt", []byte("b"))
	e.sim.AddFile("", "with/slash", []byte("x"))
	e.sim.AddOther("", "A form", "application/vnd.google-apps.form")
	e.sim.AddFile(docs, "ä report #1.pdf", []byte("pdf"))

	res, err := e.list(e.alex, e.root, 0, "")
	require.NoError(t, err)
	require.Equal(t, "Docs|A form.gform|Budget.gsheet|notes.txt|twice.txt", fileNames(res.Files))
	require.True(t, res.Pagination.IsCursor)
	require.Empty(t, res.Pagination.NextPageToken)
	require.Equal(t, 1, res.Connected.Omitted, "the name with a slash")
	require.Equal(t, e.id, res.Connected.ID)
	require.False(t, res.Connected.ReadOnly)
	require.Equal(t, e.root, res.Parent.Path)
	require.Equal(t, []string{"name", "size", "updated_at"}, res.Props.OrderByOptions)

	byName := map[string]FileResponse{}
	for _, f := range res.Files {
		byName[f.Name] = f
		require.True(t, f.Owned, f.Name)
		require.False(t, f.Shared, f.Name)
		require.True(t, strings.HasPrefix(f.ID, "cs"), f.Name)
	}
	can := func(name string, capability dbfs.NavigatorCapability) bool {
		return byName[name].Capability.Enabled(int(capability))
	}

	folder := byName["Docs"]
	require.Equal(t, int(types.FileTypeFolder), folder.Type)
	require.Equal(t, e.root+"/Docs", folder.Path)
	require.Empty(t, folder.PrimaryEntity)
	require.True(t, can("Docs", dbfs.NavigatorCapabilityEnterFolder))
	require.True(t, can("Docs", dbfs.NavigatorCapabilityListChildren))
	require.True(t, can("Docs", dbfs.NavigatorCapabilityDownloadFile), "a client downloads a folder by walking it")

	file := byName["notes.txt"]
	require.Equal(t, int64(5), file.Size)
	require.True(t, strings.HasPrefix(file.PrimaryEntity, "v"))
	require.Contains(t, file.Metadata, dbfs.ThumbDisabledKey)
	require.True(t, can("notes.txt", dbfs.NavigatorCapabilityDownloadFile))
	require.True(t, can("notes.txt", dbfs.NavigatorCapabilityRenameFile))
	require.True(t, can("notes.txt", dbfs.NavigatorCapabilityDeleteFile))
	require.True(t, can("Docs", dbfs.NavigatorCapabilityCreateFile))
	// On a file the upload bit says that its content can be saved over.
	require.True(t, can("notes.txt", dbfs.NavigatorCapabilityUploadFile))
	for _, readOnly := range []string{"Budget.gsheet", "twice.txt"} {
		require.False(t, can(readOnly, dbfs.NavigatorCapabilityRenameFile), readOnly)
		require.False(t, can(readOnly, dbfs.NavigatorCapabilityDeleteFile), readOnly)
		require.False(t, can(readOnly, dbfs.NavigatorCapabilityUploadFile), readOnly)
	}
	for _, never := range []dbfs.NavigatorCapability{
		dbfs.NavigatorCapabilityCreateFile,
		dbfs.NavigatorCapabilityShare, dbfs.NavigatorCapabilityVersionControl,
		dbfs.NavigatorCapabilityUpdateMetadata, dbfs.NavigatorCapabilityRestore, dbfs.NavigatorCapabilitySoftDelete,
	} {
		require.False(t, can("notes.txt", never), "capability %d", never)
	}

	native := byName["Budget.gsheet"]
	require.Equal(t, "xlsx", native.Metadata[connectedMetaExport])
	require.NotEmpty(t, native.Metadata[connectedMetaExternalURL])
	require.Contains(t, native.Metadata, connectedMetaSizeUnknown)

	// Something of the provider's that has no bytes and no export shows as a row that
	// opens at the provider: nothing to download, and nothing is missing from the list.
	form := byName["A form.gform"]
	require.NotEmpty(t, form.Metadata[connectedMetaExternalURL])
	require.NotContains(t, form.Metadata, connectedMetaExport)
	require.False(t, can("A form.gform", dbfs.NavigatorCapabilityDownloadFile))
	_, err = e.urlOf(e.alex, e.root+"/A form.gform", true)
	require.Error(t, err)
	sameForm, err := e.info(e.alex, e.root+"/A form.gform")
	require.NoError(t, err)
	require.Equal(t, form.ID, sameForm.ID)

	dup := byName["twice.txt"]
	require.Equal(t, connected.UnsupportedDuplicate, dup.Metadata[connectedMetaUnsupported])
	require.False(t, can("twice.txt", dbfs.NavigatorCapabilityDownloadFile))
	require.Zero(t, dup.Size)

	// Below the root, with a name that needs escaping in an address.
	sub, err := e.list(e.alex, e.root+"/Docs", 0, "")
	require.NoError(t, err)
	require.Len(t, sub.Files, 1)
	require.Equal(t, "ä report #1.pdf", sub.Files[0].Name)
	require.Equal(t, "Docs", sub.Parent.Name)
	again, err := e.info(e.alex, sub.Files[0].Path)
	require.NoError(t, err, "the address of a row leads back to the row")
	require.Equal(t, sub.Files[0].ID, again.ID)
	require.Equal(t, sub.Files[0].PrimaryEntity, again.PrimaryEntity)
	require.NotNil(t, again.ExtendedInfo)
	require.Equal(t, int64(3), again.ExtendedInfo.StorageUsed)

	rootInfo, err := e.info(e.alex, e.root)
	require.NoError(t, err)
	require.Equal(t, "Google Drive", rootInfo.Name)

	// What cannot be addressed answers as such.
	_, err = e.info(e.alex, e.root+"/twice.txt")
	require.Equal(t, serializer.CodeConnectedItem, connectedCode(err))
	require.Equal(t, connected.ReasonAmbiguous, connected.ReasonOf(err))
	_, err = e.info(e.alex, e.root+"/nope.txt")
	require.Equal(t, serializer.CodeParentNotExist, connectedCode(err))
	_, err = e.list(e.alex, e.root+"/notes.txt", 0, "")
	require.Equal(t, serializer.CodeParentNotExist, connectedCode(err), "a file is no folder")
	_, err = e.info(e.alex, e.root+"/Docs/../notes.txt")
	require.Equal(t, serializer.CodeParamErr, connectedCode(err), "a path is not cleaned up silently")
	_, err = e.list(e.alex, e.root+"?name=notes", 0, "")
	require.Equal(t, serializer.CodeConnectedUnsupported, connectedCode(err), "no search yet")
}

func TestConnectedPaging(t *testing.T) {
	e := newConnectedEnv(t)
	for _, name := range []string{"e", "c", "a", "d", "b"} {
		e.sim.AddFile("", name+".txt", []byte(name))
	}

	var names []string
	token := ""
	for {
		res, err := e.list(e.alex, e.root, 2, token)
		require.NoError(t, err)
		require.Equal(t, 5, res.Pagination.TotalItems)
		names = append(names, fileNames(res.Files))
		// The storage changes while the client pages: the listing stays the one it started.
		e.sim.AddFile("", "0-new.txt", []byte("n"))
		if token = res.Pagination.NextPageToken; token == "" {
			break
		}
	}
	require.Equal(t, "a.txt|b.txt|c.txt|d.txt|e.txt", strings.Join(names, "|"))

	// A token is good for its account, connection, folder and order only.
	first, err := e.list(e.alex, e.root, 2, "")
	require.NoError(t, err)
	_, err = e.list(e.alex, e.root, 2, "made-up")
	require.Equal(t, serializer.CodeConnectedListingGone, connectedCode(err))
	_, err = (&ListFileService{Uri: e.root, PageSize: 2, OrderBy: "size", NextPageToken: first.Pagination.NextPageToken}).List(e.as(e.alex))
	require.Equal(t, serializer.CodeConnectedListingGone, connectedCode(err))
	_, err = e.list(e.sam, e.root, 2, first.Pagination.NextPageToken)
	require.Equal(t, serializer.CodeParentNotExist, connectedCode(err))
}

// Another account gets the same answer for a connection of Alex as for one that does not
// exist, on every route.
func TestConnectedIsOnlyForItsOwner(t *testing.T) {
	e := newConnectedEnv(t)
	e.sim.AddFile("", "notes.txt", []byte("hello"))
	missing := "cloudreve://aaaaaaaaaaaaaaaa@connected"

	for _, root := range []string{e.root, missing, "cloudreve://connected", "cloudreve://x@connected"} {
		_, err := e.list(e.sam, root, 0, "")
		require.Equal(t, serializer.CodeParentNotExist, connectedCode(err), root)
		_, err = e.info(e.sam, root+"/notes.txt")
		require.Equal(t, serializer.CodeParentNotExist, connectedCode(err), root)
		_, err = e.urlOf(e.sam, root+"/notes.txt", false)
		require.Error(t, err, root)
		require.Error(t, (&PatchViewService{Uri: root, View: &types.ExplorerView{PageSize: 50}}).Patch(e.as(e.sam)), root)
		_, err = ConnectedCapacity(e.as(e.sam), strings.TrimSuffix(strings.TrimPrefix(root, "cloudreve://"), "@connected"))
		require.Error(t, err, root)
	}

	// A guest and an account without the permission get nowhere.
	anonymous, err := e.dep.UserClient().AnonymousUser(e.ctx)
	require.NoError(t, err)
	_, err = e.list(anonymous, e.root, 0, "")
	require.Equal(t, serializer.CodeConnectedUnavailable, connectedCode(err))

	g, err := e.client.Group.Query().Where(group.ID(2)).First(e.ctx)
	require.NoError(t, err)
	permissions := make(boolset.BooleanSet, len(*g.Permissions))
	copy(permissions, *g.Permissions)
	boolset.Set(types.GroupPermissionConnectedStorage, false, &permissions)
	address, err := e.urlOf(e.alex, e.root+"/notes.txt", false)
	require.NoError(t, err)
	require.NoError(t, e.client.Group.Update().Where(group.ID(2)).SetPermissions(&permissions).Exec(e.ctx))
	_, err = e.list(e.reload(e.alex.ID), e.root, 0, "")
	require.Equal(t, serializer.CodeConnectedUnavailable, connectedCode(err))
	require.Equal(t, http.StatusNotFound, e.fetch(address, "").Code, "an address handed out before is dead too")
}

func TestConnectedDownload(t *testing.T) {
	e := newConnectedEnv(t)
	id := e.sim.AddFile("", "notes.txt", []byte("0123456789"))
	e.sim.AddFolder("", "Docs")
	e.sim.AddNative("", "Budget", "gsheet")
	e.sim.AddFile("", "page.html", []byte("<script>alert(1)</script>"))
	calls := e.sim.Calls(drivetest.CallMedia)

	address, err := e.urlOf(e.alex, e.root+"/notes.txt", true)
	require.NoError(t, err)
	require.NotContains(t, address, "access-1")
	require.NotContains(t, address, "?", "nothing that selects data is in a query")

	rec := e.fetch(address, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "0123456789", rec.Body.String())
	require.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	require.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"))
	require.Contains(t, rec.Header().Get("Content-Disposition"), "attachment")
	require.Contains(t, rec.Header().Get("Content-Disposition"), "notes.txt")
	require.Equal(t, "10", rec.Header().Get("Content-Length"))

	rec = e.fetch(address, "bytes=2-5")
	require.Equal(t, http.StatusPartialContent, rec.Code)
	require.Equal(t, "2345", rec.Body.String())
	require.Equal(t, "bytes 2-5/10", rec.Header().Get("Content-Range"))
	require.Equal(t, calls+2, e.sim.Calls(drivetest.CallMedia))

	// The name at the end of the address is for the browser only: changing it changes
	// nothing, and there is no way to point the address at another file.
	other := address[:strings.LastIndex(address, "/")] + "/page.html"
	rec = e.fetch(other, "")
	require.Equal(t, "0123456789", rec.Body.String())
	require.Contains(t, rec.Header().Get("Content-Disposition"), "notes.txt")

	// A changed or made-up grant is nothing.
	broken := strings.Replace(address, "/content/", "/content/A", 1)
	require.Equal(t, http.StatusNotFound, e.fetch(broken, "").Code)
	require.Equal(t, http.StatusNotFound, e.fetch("https://cloud.example.com"+connectedContentPath+"abc/notes.txt", "").Code)

	// A viewer gets the content inline, with the type by the name.
	inline, err := e.urlOf(e.alex, e.root+"/page.html", false)
	require.NoError(t, err)
	rec = e.fetch(inline, "")
	require.Contains(t, rec.Header().Get("Content-Disposition"), "inline")
	require.Contains(t, rec.Header().Get("Content-Type"), "text/html")

	// A document that exists only at the provider is exported under an Office name.
	export, err := e.urlOf(e.alex, e.root+"/Budget.gsheet", true)
	require.NoError(t, err)
	rec = e.fetch(export, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Disposition"), "Budget.xlsx")
	require.Empty(t, rec.Header().Get("Accept-Ranges"))

	// The version a client names is the one it gets, or none.
	row, err := e.info(e.alex, e.root+"/notes.txt")
	require.NoError(t, err)
	_, err = (&FileURLService{Uris: []string{e.root + "/notes.txt"}, Entity: row.PrimaryEntity}).Get(e.as(e.alex))
	require.NoError(t, err)
	_, err = (&FileURLService{Uris: []string{e.root + "/notes.txt"}, Entity: "v000"}).Get(e.as(e.alex))
	require.Error(t, err)
	_, err = e.urlOf(e.alex, e.root+"/Docs", false)
	require.Error(t, err, "a folder has no content")

	// The file changes at the provider: an address handed out before does not deliver
	// the new content under the old version.
	e.sim.SetContent(id, []byte("changed"))
	require.Equal(t, http.StatusConflict, e.fetch(address, "").Code)
	fresh, err := e.urlOf(e.alex, e.root+"/notes.txt", true)
	require.NoError(t, err)
	require.Equal(t, "changed", e.fetch(fresh, "").Body.String())
	after, err := e.info(e.alex, e.root+"/notes.txt")
	require.NoError(t, err)
	require.Equal(t, row.ID, after.ID)
	require.NotEqual(t, row.PrimaryEntity, after.PrimaryEntity)

	// A new password ends the addresses handed out before.
	require.NoError(t, e.client.User.UpdateOneID(e.alex.ID).SetPassword("another-hash").Exec(e.ctx))
	require.Equal(t, http.StatusNotFound, e.fetch(fresh, "").Code)

	// So does disconnecting.
	e.alex = e.reload(e.alex.ID)
	last, err := e.urlOf(e.alex, e.root+"/notes.txt", true)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, e.fetch(last, "").Code)
	require.NoError(t, e.m.Disconnect(e.ctx, e.alex.ID, e.id))
	require.Equal(t, http.StatusNotFound, e.fetch(last, "").Code)
	_, err = e.list(e.alex, e.root, 0, "")
	require.Equal(t, serializer.CodeParentNotExist, connectedCode(err))
}

// Addresses of a connected storage never reach the code for files in the database.
func TestConnectedIsKeptFromTheDatabaseFileSystem(t *testing.T) {
	e := newConnectedEnv(t)
	e.sim.AddFile("", "notes.txt", []byte("hello"))
	file := e.root + "/notes.txt"

	// An archive of connected files: refused before a session is made.
	_, err := (&FileURLService{Uris: []string{file}, Archive: true}).Get(e.as(e.alex))
	require.Equal(t, serializer.CodeConnectedUnsupported, connectedCode(err))
	// A mix of both kinds of addresses in one request.
	_, err = (&FileURLService{Uris: []string{file, "cloudreve://my/a.txt"}}).Get(e.as(e.alex))
	require.Equal(t, serializer.CodeParamErr, connectedCode(err))

	// A sample of the routes that are not for connected storages: each one refuses
	// while parsing, before it does anything.
	require.Error(t, (&PinFileService{Uri: file, Name: "x"}).PinFile(e.as(e.alex)))
	require.Empty(t, e.reload(e.alex.ID).Settings.Pined)
	_, err = (&GetDirectLinkService{Uris: []string{file}}).Get(e.as(e.alex))
	require.Error(t, err)
	require.Error(t, (&DeleteFileService{Uris: []string{file}}).Delete(e.as(e.alex)))
	_, err = (&RenameFileService{Uri: file, NewName: "x.txt"}).Rename(e.as(e.alex))
	require.Error(t, err)
	require.Error(t, (&MoveFileService{Uris: []string{file}, Dst: "cloudreve://my"}).Move(e.as(e.alex)))
	require.Error(t, (&MoveFileService{Uris: []string{"cloudreve://my/a.txt"}, Dst: e.root}).Move(e.as(e.alex)))
	_, err = (&FileThumbService{Uri: file}).Get(e.as(e.alex))
	require.Equal(t, serializer.CodeEntityNotExist, connectedCode(err))

	require.Error(t, (&DeleteFileService{Uris: []string{file}}).Restore(e.as(e.alex)))
	require.Error(t, (&PinFileService{Uri: file, Name: "x"}).UnpinFile(e.as(e.alex)))
	require.Error(t, (&PatchMetadataService{Uris: []string{file}, Patches: []fs.MetadataPatch{{Key: "tag:x", Value: "1"}}}).Patch(e.as(e.alex)))
	require.Error(t, (&SetCurrentVersionService{Uri: file, Version: "x"}).Set(e.as(e.alex)))
	require.Error(t, (&DeleteVersionService{Uri: file, Version: "x"}).Delete(e.as(e.alex)))
	_, err = (&CreateViewerSessionService{Uri: file, ViewerID: "x", PreferredAction: types.ViewerActionView}).Create(e.as(e.alex))
	require.Error(t, err)
	// An upload session that is not one of a connected storage.
	require.Error(t, (&DeleteUploadSessionService{ID: "x", Uri: file}).Delete(e.as(e.alex)))
	_, err = (&ArchiveListFilesService{Uri: file}).List(e.as(e.alex))
	require.Error(t, err)
	require.Error(t, (&ExplorerEventService{Uri: e.root}).HandleExplorerEventsPush(e.as(e.alex)))

	// The task routes keep their sources as text until the task runs. An administrator,
	// who may start every kind of task, connects a storage and tries them all: nothing is
	// queued and nothing is created, also with a destination that is perfectly fine.
	created, err := e.client.User.Create().SetEmail("admin@example.com").SetNick("admin").SetPassword("hash").
		SetStatus(user.StatusActive).SetGroupID(1).SetSettings(&types.UserSetting{}).Save(e.ctx)
	require.NoError(t, err)
	admin := e.reload(created.ID)
	own, err := e.m.Create(e.ctx, admin.ID, connected.ProviderDrive,
		&connected.Identity{Key: "account-2", Label: "admin@example.com"}, connectedTestToken)
	require.NoError(t, err)
	ownFile := own.URI + "/notes.txt"
	tasks, err := e.client.Task.Query().Count(e.ctx)
	require.NoError(t, err)
	files, err := e.client.File.Query().Count(e.ctx)
	require.NoError(t, err)

	_, err = (&ArchiveWorkflowService{Src: []string{ownFile}, Dst: "cloudreve://my"}).CreateExtractTask(e.as(admin))
	require.Equal(t, serializer.CodeConnectedUnsupported, connectedCode(err))
	_, err = (&ArchiveWorkflowService{Src: []string{"cloudreve://my/a.txt", ownFile}, Dst: "cloudreve://my/out.zip"}).CreateCompressTask(e.as(admin))
	require.Equal(t, serializer.CodeConnectedUnsupported, connectedCode(err))
	_, err = (&DownloadWorkflowService{Src: []string{ownFile}, Dst: "cloudreve://my"}).CreateDownloadTask(e.as(admin))
	require.Equal(t, serializer.CodeConnectedUnsupported, connectedCode(err))
	_, err = (&DownloadWorkflowService{SrcFile: ownFile, Dst: "cloudreve://my"}).CreateDownloadTask(e.as(admin))
	require.Equal(t, serializer.CodeConnectedUnsupported, connectedCode(err))
	// And as a destination, which the parser refuses.
	_, err = (&ArchiveWorkflowService{Src: []string{"cloudreve://my/a.zip"}, Dst: own.URI}).CreateExtractTask(e.as(admin))
	require.Error(t, err)
	_, err = (&DownloadWorkflowService{Src: []string{"https://example.com/a.bin"}, Dst: own.URI}).CreateDownloadTask(e.as(admin))
	require.Error(t, err)
	_, err = (&ImportWorkflowService{Src: "/srv/import", Dst: own.URI, UserID: hashid.EncodeUserID(e.dep.HashIDEncoder(), admin.ID), PolicyID: 1}).CreateImportTask(e.as(admin))
	require.Equal(t, serializer.CodeConnectedUnsupported, connectedCode(err))

	after, err := e.client.Task.Query().Count(e.ctx)
	require.NoError(t, err)
	require.Equal(t, tasks, after, "no task was queued")
	after, err = e.client.File.Query().Count(e.ctx)
	require.NoError(t, err)
	require.Equal(t, files, after, "no placeholder file was created")

	// The storage at the provider was not touched by any of it.
	res, err := e.list(e.alex, e.root, 0, "")
	require.NoError(t, err)
	require.Equal(t, "notes.txt", fileNames(res.Files))
}

func TestConnectedViewAndCapacity(t *testing.T) {
	e := newConnectedEnv(t)
	e.sim.SetQuota(250, 1000)

	usage, err := ConnectedCapacity(e.as(e.alex), e.id)
	require.NoError(t, err)
	require.Equal(t, int64(250), usage.Used)
	require.Equal(t, int64(1000), usage.Total)
	// Asked again at once, the provider is not.
	before := e.sim.Calls(drivetest.CallAbout)
	_, err = ConnectedCapacity(e.as(e.alex), e.id)
	require.NoError(t, err)
	require.Equal(t, before, e.sim.Calls(drivetest.CallAbout))

	view := &types.ExplorerView{PageSize: 50, Order: "size", OrderDirection: "desc"}
	require.NoError(t, (&PatchViewService{Uri: e.root, View: view}).Patch(e.as(e.alex)))
	res, err := e.list(e.alex, e.root, 0, "")
	require.NoError(t, err)
	require.Equal(t, view, res.View)
	require.NoError(t, (&PatchViewService{Uri: e.root}).Patch(e.as(e.alex)))
	res, err = e.list(e.alex, e.root, 0, "")
	require.NoError(t, err)
	require.Nil(t, res.View)
}

// A link shows the content of another item. An address for it is tied to the state of
// that item, like any other address.
func TestConnectedDownloadThroughALink(t *testing.T) {
	e := newConnectedEnv(t)
	target := e.sim.AddFile("", "notes.txt", []byte("0123456789"))
	link := e.sim.AddShortcut("", "Link to notes", target)

	address, err := e.urlOf(e.alex, e.root+"/Link to notes", true)
	require.NoError(t, err)
	rec := e.fetch(address, "bytes=2-5")
	require.Equal(t, http.StatusPartialContent, rec.Code)
	require.Equal(t, "2345", rec.Body.String())

	// The target changes: the address made before does not deliver the new content.
	e.sim.SetContent(target, []byte("changed"))
	require.Equal(t, http.StatusConflict, e.fetch(address, "").Code)
	fresh, err := e.urlOf(e.alex, e.root+"/Link to notes", true)
	require.NoError(t, err)
	require.Equal(t, "changed", e.fetch(fresh, "").Body.String())

	// The link is removed: its address ends, although the target is still there.
	e.sim.Trash(link)
	require.Equal(t, http.StatusNotFound, e.fetch(fresh, "").Code)
}

func TestConnectedRanges(t *testing.T) {
	e := newConnectedEnv(t)
	e.sim.AddFile("", "notes.txt", []byte("0123456789"))
	address, err := e.urlOf(e.alex, e.root+"/notes.txt", true)
	require.NoError(t, err)

	// A range past the end is answered as such, not as a file that changed.
	require.Equal(t, http.StatusRequestedRangeNotSatisfiable, e.fetch(address, "bytes=50-60").Code)
	// Several ranges at once are not passed on: the whole file is the answer.
	rec := e.fetch(address, "bytes=0-1,4-5")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "0123456789", rec.Body.String())
	require.Equal(t, "bytes=0-1", singleRange("bytes=0-1"))
	require.Empty(t, singleRange("items=0-1"))
}

// When the provider's stream breaks off, the answer is cut, not ended: an export has no
// length a client could check it against.
func TestConnectedBrokenStreamIsNotACompleteAnswer(t *testing.T) {
	e := newConnectedEnv(t)
	e.sim.AddNative("", "Budget", "gsheet")
	address, err := e.urlOf(e.alex, e.root+"/Budget.gsheet", true)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, e.fetch(address, "").Code)

	e.sim.BreakExports(true)
	require.PanicsWithValue(t, http.ErrAbortHandler, func() { e.fetch(address, "") })
}

// The reason of a failure is told for each address of a batch.
func TestConnectedReasonInABatch(t *testing.T) {
	e := newConnectedEnv(t)
	e.sim.AddFile("", "twice.txt", []byte("a"))
	e.sim.AddFile("", "twice.txt", []byte("b"))

	_, err := (&FileURLService{Uris: []string{e.root + "/twice.txt"}}).Get(e.as(e.alex))
	require.Equal(t, serializer.CodeBatchOperationNotFullyCompleted, connectedCode(err))
	res := serializer.Err(e.ctx, err)
	entry, ok := res.AggregatedError.(map[string]serializer.Response)[e.root+"/twice.txt"]
	require.True(t, ok, "%+v", res.AggregatedError)
	require.Equal(t, serializer.CodeConnectedItem, entry.Code)
	require.Equal(t, connected.ReasonData{Reason: connected.ReasonAmbiguous}, entry.Data)
}

// A HEAD request is answered from what is known of the file. No download is started.
func TestConnectedHead(t *testing.T) {
	e := newConnectedEnv(t)
	e.sim.AddFile("", "notes.txt", []byte("0123456789"))
	address, err := e.urlOf(e.alex, e.root+"/notes.txt", true)
	require.NoError(t, err)
	u, err := url.Parse(address)
	require.NoError(t, err)
	grant, name, _ := strings.Cut(strings.TrimPrefix(u.Path, connectedContentPath), "/")

	before := e.sim.Calls(drivetest.CallMedia)
	c, rec := e.request(nil, http.MethodHead, u.EscapedPath())
	c.Params = gin.Params{{Key: "grant", Value: grant}, {Key: "name", Value: name}}
	ServeConnectedContent(c)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "10", rec.Header().Get("Content-Length"))
	require.Equal(t, "bytes", rec.Header().Get("Accept-Ranges"))
	require.Contains(t, rec.Header().Get("Content-Disposition"), "notes.txt")
	require.Empty(t, rec.Body.String())
	require.Equal(t, before, e.sim.Calls(drivetest.CallMedia))
}

// What people share with each other: a shortcut to a Google document, and a file one
// may only view.
func TestConnectedSharedItems(t *testing.T) {
	e := newConnectedEnv(t)
	doc := e.sim.AddNative("", "Budget", "gsheet")
	e.sim.AddShortcut("", "Shared budget", doc)
	viewOnly := e.sim.AddFile("", "minutes.txt", []byte("0123456789"))
	e.sim.SetViewOnly(viewOnly)
	e.sim.AddShortcut("", "Shared minutes", viewOnly)

	res, err := e.list(e.alex, e.root, 0, "")
	require.NoError(t, err)
	require.Equal(t, "Budget.gsheet|minutes.txt|Shared budget.gsheet|Shared minutes", fileNames(res.Files))
	// The address at the provider is told for what opens only there. A link to a file
	// that Fylvo can read has none: clients take it for "not to be opened here".
	for _, f := range res.Files {
		if f.Name == "Budget.gsheet" || f.Name == "Shared budget.gsheet" {
			require.NotEmpty(t, f.Metadata[connectedMetaExternalURL], f.Name)
		} else {
			require.NotContains(t, f.Metadata, connectedMetaExternalURL, f.Name)
		}
	}

	export, err := e.urlOf(e.alex, e.root+"/Shared budget.gsheet", true)
	require.NoError(t, err)
	rec := e.fetch(export, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Disposition"), "Shared budget.xlsx")
	require.Contains(t, rec.Body.String(), "export of "+doc)

	for _, name := range []string{"minutes.txt", "Shared minutes"} {
		address, err := e.urlOf(e.alex, e.root+"/"+name, true)
		require.NoError(t, err, name)
		rec := e.fetch(address, "bytes=2-5")
		require.Equal(t, http.StatusPartialContent, rec.Code, name)
		require.Equal(t, "2345", rec.Body.String(), name)
	}
}

// The version a client names is compared with the file as it is now, not as a listing
// of a moment ago showed it.
func TestConnectedURLLooksAtTheFileAsItIs(t *testing.T) {
	e := newConnectedEnv(t)
	id := e.sim.AddFile("", "notes.txt", []byte("first"))
	res, err := e.list(e.alex, e.root, 0, "")
	require.NoError(t, err)
	seen := res.Files[0].PrimaryEntity

	// Changed right after the listing, which the server still has in memory.
	e.sim.SetContent(id, []byte("second"))
	_, err = (&FileURLService{Uris: []string{e.root + "/notes.txt"}, Entity: seen}).Get(e.as(e.alex))
	require.Error(t, err, "the version the client saw is gone")
	address, err := e.urlOf(e.alex, e.root+"/notes.txt", true)
	require.NoError(t, err)
	require.Equal(t, "second", e.fetch(address, "").Body.String())
}

// What a request leaves out of order and page size comes from the view stored for the
// storage, for the first page and for the ones after it.
func TestConnectedListFollowsTheStoredView(t *testing.T) {
	e := newConnectedEnv(t)
	e.sim.AddFile("", "a.txt", []byte("1"))
	e.sim.AddFile("", "b.txt", []byte("333"))
	e.sim.AddFile("", "c.txt", []byte("22"))
	view := &types.ExplorerView{PageSize: 2, Order: "size", OrderDirection: "desc"}
	require.NoError(t, (&PatchViewService{Uri: e.root, View: view}).Patch(e.as(e.alex)))

	first, err := (&ListFileService{Uri: e.root}).List(e.as(e.alex))
	require.NoError(t, err)
	require.Equal(t, "b.txt|c.txt", fileNames(first.Files))
	require.Equal(t, 2, first.Pagination.PageSize)
	second, err := (&ListFileService{Uri: e.root, NextPageToken: first.Pagination.NextPageToken}).List(e.as(e.alex))
	require.NoError(t, err)
	require.Equal(t, "a.txt", fileNames(second.Files))

	// What the request says goes first.
	named, err := (&ListFileService{Uri: e.root, OrderBy: "name", OrderDirection: "asc", PageSize: 10}).List(e.as(e.alex))
	require.NoError(t, err)
	require.Equal(t, "a.txt|b.txt|c.txt", fileNames(named.Files))
}

// stalled is a stream of a provider that sends a little and then nothing more.
type stalled struct {
	sent bool
	stop chan struct{}
}

func (s *stalled) Read(p []byte) (int, error) {
	if !s.sent {
		s.sent = true
		return copy(p, "the first bytes"), nil
	}
	<-s.stop
	return 0, context.Canceled
}

// A transfer that stands still ends, so it does not hold its place for good.
func TestConnectedTransferThatStandsStillEnds(t *testing.T) {
	idle := connectedIdle
	connectedIdle = 30 * time.Millisecond
	defer func() { connectedIdle = idle }()

	c, rec := gin.CreateTestContext(httptest.NewRecorder())
	src := &stalled{stop: make(chan struct{})}
	watch := time.AfterFunc(time.Hour, func() { close(src.stop) })
	defer watch.Stop()
	err := copyMoving(c.Writer, src, watch)
	require.Error(t, err)
	_ = rec
}

// A link itself and what is reached through one are told apart.
func TestConnectedLinkMarks(t *testing.T) {
	e := newConnectedEnv(t)
	docs := e.sim.AddFolder("", "Docs")
	e.sim.AddFolder(docs, "Sub")
	e.sim.AddShortcut(docs, "Back to Docs", docs)
	e.sim.AddShortcut("", "Link to Docs", docs)

	root, err := e.list(e.alex, e.root, 0, "")
	require.NoError(t, err)
	marks := func(res *ListResponse, name string) (link, via bool) {
		for _, f := range res.Files {
			if f.Name == name {
				_, link = f.Metadata[connectedMetaLink]
				_, via = f.Metadata[connectedMetaViaLink]
			}
		}
		return
	}
	link, via := marks(root, "Link to Docs")
	require.True(t, link)
	require.False(t, via)
	link, via = marks(root, "Docs")
	require.False(t, link || via)
	for _, f := range root.Files {
		// A folder is downloaded by walking it, which stops at links.
		require.Equal(t, f.Name == "Docs", f.Capability.Enabled(int(dbfs.NavigatorCapabilityDownloadFile)), f.Name)
	}

	below, err := e.list(e.alex, e.root+"/Link to Docs", 0, "")
	require.NoError(t, err)
	require.True(t, below.Connected.ReadOnly)
	link, via = marks(below, "Sub")
	require.False(t, link, "an ordinary folder below a link can be walked into")
	require.True(t, via)
	link, via = marks(below, "Back to Docs")
	require.True(t, link, "a link below a link is a link: it can lead back up")
	require.False(t, via)

	// An address with a second slash at its end is none of a connected storage.
	_, err = e.info(e.alex, e.root+"/Docs//")
	require.Equal(t, serializer.CodeParamErr, connectedCode(err))
}
