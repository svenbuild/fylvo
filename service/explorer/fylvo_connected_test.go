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

	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/enttest"
	"github.com/cloudreve/Cloudreve/v4/ent/group"
	"github.com/cloudreve/Cloudreve/v4/ent/user"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/boolset"
	"github.com/cloudreve/Cloudreve/v4/pkg/cache"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider/drive"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider/drive/drivetest"
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
}

const connectedTestToken = `{"access_token":"access-1","token_type":"Bearer","refresh_token":"refresh-1","expiry":"2099-01-01T00:00:00Z"}`

func newConnectedEnv(t *testing.T) *connectedEnv {
	logger := logging.NewConsoleLogger(logging.LevelError)
	kv := cache.NewMemoStore("", logger)
	client := enttest.Open(t, "sqlite3", filepath.Join(t.TempDir(), "fylvo.db"))
	t.Cleanup(func() { _ = client.Close() })
	client, err := inventory.InitializeDBClient(logger, client, kv, "4.19.1")
	require.NoError(t, err)
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

	e := &connectedEnv{t: t, ctx: ctx, dep: dep, client: client, sim: drivetest.New(t)}
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

	c, rec := e.request(nil, http.MethodGet, u.Path)
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
	require.Equal(t, "Docs|Budget.gsheet|notes.txt|twice.txt", fileNames(res.Files))
	require.True(t, res.Pagination.IsCursor)
	require.Empty(t, res.Pagination.NextPageToken)
	require.Equal(t, 1, res.Connected.Omitted, "the name with a slash")
	require.Equal(t, e.id, res.Connected.ID)
	require.True(t, res.Connected.ReadOnly)
	require.Equal(t, e.root, res.Parent.Path)
	require.Equal(t, []string{"name", "size", "updated_at"}, res.Props.OrderByOptions)

	byName := map[string]FileResponse{}
	for _, f := range res.Files {
		byName[f.Name] = f
		require.False(t, f.Owned, f.Name)
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
	require.False(t, can("Docs", dbfs.NavigatorCapabilityDownloadFile))

	file := byName["notes.txt"]
	require.Equal(t, int64(5), file.Size)
	require.True(t, strings.HasPrefix(file.PrimaryEntity, "v"))
	require.Contains(t, file.Metadata, dbfs.ThumbDisabledKey)
	require.True(t, can("notes.txt", dbfs.NavigatorCapabilityDownloadFile))
	for _, never := range []dbfs.NavigatorCapability{
		dbfs.NavigatorCapabilityCreateFile, dbfs.NavigatorCapabilityRenameFile, dbfs.NavigatorCapabilityUploadFile,
		dbfs.NavigatorCapabilityDeleteFile, dbfs.NavigatorCapabilityShare, dbfs.NavigatorCapabilityVersionControl,
		dbfs.NavigatorCapabilityUpdateMetadata, dbfs.NavigatorCapabilityRestore, dbfs.NavigatorCapabilitySoftDelete,
	} {
		require.False(t, can("notes.txt", never), "capability %d", never)
	}

	native := byName["Budget.gsheet"]
	require.Equal(t, "xlsx", native.Metadata[connectedMetaExport])
	require.NotEmpty(t, native.Metadata[connectedMetaExternalURL])
	require.Contains(t, native.Metadata, connectedMetaSizeUnknown)

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
	_, err = (&CreateFileService{Uri: e.root + "/new", Type: "folder"}).Create(e.as(e.alex))
	require.Error(t, err)
	_, err = (&FileThumbService{Uri: file}).Get(e.as(e.alex))
	require.Equal(t, serializer.CodeEntityNotExist, connectedCode(err))

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
