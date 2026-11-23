package explorer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cloudreve/Cloudreve/v4/ent/group"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/boolset"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider/drive/drivetest"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/pkg/setting"
	"github.com/cloudreve/Cloudreve/v4/pkg/util"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Fylvo: files of a connected storage in the Office server, from the call that opens a
// session to the calls the Office server makes with it.

const officeAddress = "https://office.example.com"

// withOffice sets up an Office server as a viewer: it edits docx, and only shows dot.
func (e *connectedEnv) withOffice(t *testing.T, disabled bool) {
	e.withOfficeActions(t, disabled, map[types.ViewerAction]string{
		types.ViewerActionEdit: officeAddress + "/edit?", types.ViewerActionView: officeAddress + "/view?",
	})
}

// withOfficeActions sets up the Office server with the given actions for docx.
func (e *connectedEnv) withOfficeActions(t *testing.T, disabled bool, docx map[types.ViewerAction]string) {
	viewer := types.Viewer{
		ID: "office", Type: types.ViewerTypeWopi, DisplayName: "Office", Exts: []string{"docx", "dot"}, Disabled: disabled,
		WopiActions: map[string]map[types.ViewerAction]string{
			"docx": docx,
			"dot":  {types.ViewerActionView: officeAddress + "/view?"},
		},
	}
	plain := types.Viewer{ID: "text", Type: types.ViewerTypeBuiltin, DisplayName: "Text", Exts: []string{"txt", "docx"}}
	raw, err := json.Marshal([]types.ViewerGroup{{Viewers: []types.Viewer{viewer, plain}}})
	require.NoError(t, err)
	e.set(t, "file_viewers", string(raw))
}

// set changes a setting of the installation the way the admin area does.
func (e *connectedEnv) set(t *testing.T, name, value string) {
	require.NoError(t, e.dep.SettingClient().Set(e.ctx, map[string]string{name: value}))
	require.NoError(t, e.dep.KV().Delete(setting.KvSettingPrefix, name))
}

// officeSession opens a session as the web app does.
func (e *connectedEnv) officeSession(t *testing.T, path string, action types.ViewerAction) (*ViewerSessionResponse, error) {
	t.Helper()
	return (&CreateViewerSessionService{Uri: e.root + path, ViewerID: "office", PreferredAction: action}).Create(e.as(e.alex))
}

// officeExit is the last save of a window that was closed, as the Office server marks it.
func (e *connectedEnv) officeExit(session, token string, body []byte) *httptest.ResponseRecorder {
	e.officeHeaders = map[string]string{"X-COOL-WOPI-IsExitSave": "true", "X-COOL-WOPI-IsModifiedByUser": "true"}
	defer func() { e.officeHeaders = nil }()
	rec, _ := e.office(ConnectedOfficePut, http.MethodPost, session, token, body)
	return rec
}

// officeTemps counts what the server has on its own disk of documents in transit.
func (e *connectedEnv) officeTemps() int {
	dir := util.DataPath(e.dep.SettingProvider().TempPath(e.ctx))
	found, _ := filepath.Glob(filepath.Join(dir, officeTempName+"*"))
	return len(found)
}

// office makes a call of the Office server: no sign-in, only the token.
func (e *connectedEnv) office(handler gin.HandlerFunc, method, session, token string, body []byte) (rec *httptest.ResponseRecorder, cut bool) {
	c, rec := e.request(nil, method, "/api/v4/fylvo/connected/wopi/"+session+"?access_token="+url.QueryEscape(token))
	c.Params = gin.Params{{Key: "session", Value: session}}
	for name, value := range e.officeHeaders {
		c.Request.Header.Set(name, value)
	}
	if body != nil {
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
		c.Request.ContentLength = int64(len(body))
	}
	defer func() {
		if r := recover(); r != nil {
			require.Equal(e.t, http.ErrAbortHandler, r)
			cut = true
		}
	}()
	handler(c)
	return rec, false
}

func TestConnectedOfficeEdit(t *testing.T) {
	e := newConnectedEnv(t)
	e.withOffice(t, false)
	id := e.sim.AddFile("", "letter.docx", []byte("version one"))

	res, err := e.officeSession(t, "/letter.docx", types.ViewerActionEdit)
	require.NoError(t, err)
	session, token := res.Session.ID, res.Session.AccessToken
	require.True(t, strings.HasPrefix(session, "co-"))
	require.Positive(t, res.Session.Expires)
	// The Office server is sent to its editing page, and from there to this session.
	src, err := url.Parse(res.WopiSrc)
	require.NoError(t, err)
	require.Equal(t, "/edit", src.Path)
	require.True(t, strings.HasSuffix(src.Query().Get("WOPISrc"), "/api/v4/fylvo/connected/wopi/"+session), src.Query().Get("WOPISrc"))
	require.NotContains(t, res.WopiSrc, token, "the token is not part of an address")
	// The answer says that this session saves.
	require.NotNil(t, res.Edit)
	require.True(t, *res.Edit)

	// CheckFileInfo.
	rec, _ := e.office(ConnectedOfficeInfo, http.MethodGet, session, token, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var info WopiFileInfo
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))
	require.Equal(t, "letter.docx", info.BaseFileName)
	require.EqualValues(t, len("version one"), info.Size)
	require.True(t, info.UserCanWrite)
	require.False(t, info.ReadOnly)
	require.True(t, info.SupportsUpdate)
	require.False(t, info.SupportsLocks)
	require.True(t, info.UserCanNotWriteRelative)
	require.NotEmpty(t, info.Version)
	require.NotEmpty(t, info.LastModifiedTime)
	require.Equal(t, "alex", info.UserFriendlyName)

	// GetFile, PutFile, and the file object clients see follows.
	rec, cut := e.office(ConnectedOfficeGet, http.MethodGet, session, token, nil)
	require.False(t, cut)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "version one", rec.Body.String())
	require.Equal(t, info.Version, rec.Header().Get("X-WOPI-ItemVersion"))

	rec, _ = e.office(ConnectedOfficePut, http.MethodPost, session, token, []byte("version two, from the editor"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Header().Get("X-WOPI-ServerError"))
	var saved struct{ LastModifiedTime string }
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &saved))
	require.NotEmpty(t, saved.LastModifiedTime)
	require.Equal(t, []byte("version two, from the editor"), e.download(e.root+"/letter.docx"))
	row, err := e.info(e.alex, e.root+"/letter.docx")
	require.NoError(t, err)
	require.Equal(t, row.PrimaryEntity, rec.Header().Get("X-WOPI-ItemVersion"), "the version clients see")

	// Changed at the provider: the save is answered the way the Office server asks the user.
	e.sim.SetContent(id, []byte("changed elsewhere"))
	rec, _ = e.office(ConnectedOfficePut, http.MethodPost, session, token, []byte("version three"))
	require.Equal(t, http.StatusConflict, rec.Code)
	var conflict map[string]int
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &conflict))
	require.Equal(t, 1010, conflict["COOLStatusCode"])
	require.Equal(t, []byte("changed elsewhere"), e.download(e.root+"/letter.docx"))

	// Read again, and the save goes through.
	rec, _ = e.office(ConnectedOfficeGet, http.MethodGet, session, token, nil)
	require.Equal(t, "changed elsewhere", rec.Body.String())
	rec, _ = e.office(ConnectedOfficePut, http.MethodPost, session, token, []byte("version three"))
	require.Equal(t, http.StatusOK, rec.Code)

	// Nothing else is offered: locks, rename, a copy beside the file.
	rec, _ = e.office(ConnectedOfficeOther, http.MethodPost, session, token, nil)
	require.Equal(t, http.StatusNotImplemented, rec.Code)
	// A body without a length is not taken, and one that is too large neither.
	c, rec := e.request(nil, http.MethodPost, "/x?access_token="+url.QueryEscape(token))
	c.Params = gin.Params{{Key: "session", Value: session}}
	c.Request.ContentLength = -1
	ConnectedOfficePut(c)
	require.Equal(t, http.StatusLengthRequired, rec.Code)
	e.set(t, "maxEditSize", "5")
	rec, _ = e.office(ConnectedOfficePut, http.MethodPost, session, token, []byte("too large now"))
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	rec, _ = e.office(ConnectedOfficeGet, http.MethodGet, session, token, nil)
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

func TestConnectedOfficeOnlyForWhatItIsFor(t *testing.T) {
	e := newConnectedEnv(t)
	e.withOffice(t, false)
	e.sim.AddFile("", "letter.docx", []byte("a letter"))
	e.sim.AddFile("", "template.dot", []byte("a template"))
	e.sim.AddFile("", "notes.txt", []byte("notes"))
	e.sim.AddNative("", "Budget", "gsheet")

	// Asked for editing, opened for viewing: a kind of file the Office server only shows,
	// and a request that asks for viewing.
	for path, action := range map[string]types.ViewerAction{"/template.dot": types.ViewerActionEdit, "/letter.docx": types.ViewerActionView} {
		res, err := e.officeSession(t, path, action)
		require.NoError(t, err, path)
		src, _ := url.Parse(res.WopiSrc)
		require.Equal(t, "/view", src.Path, path)
		// The answer says that the session only shows, whatever was asked for.
		require.NotNil(t, res.Edit, path)
		require.False(t, *res.Edit, path)
		rec, _ := e.office(ConnectedOfficeInfo, http.MethodGet, res.Session.ID, res.Session.AccessToken, nil)
		var info WopiFileInfo
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))
		require.True(t, info.ReadOnly, path)
		require.False(t, info.UserCanWrite, path)
		rec, _ = e.office(ConnectedOfficeGet, http.MethodGet, res.Session.ID, res.Session.AccessToken, nil)
		require.Equal(t, http.StatusOK, rec.Code, path)
		rec, _ = e.office(ConnectedOfficePut, http.MethodPost, res.Session.ID, res.Session.AccessToken, []byte("x"))
		require.Equal(t, http.StatusForbidden, rec.Code, path)
	}
	require.Equal(t, []byte("a letter"), e.download(e.root+"/letter.docx"))

	// No session: a kind of file the Office server does not know, a document that exists
	// only at the provider, a viewer that is not the Office server, an older version,
	// another account, no file.
	unsupported := func(s *CreateViewerSessionService) {
		t.Helper()
		_, err := s.Create(e.as(e.alex))
		require.Equal(t, serializer.CodeConnectedUnsupported, connectedCode(err), s.Uri)
	}
	unsupported(&CreateViewerSessionService{Uri: e.root + "/notes.txt", ViewerID: "office", PreferredAction: types.ViewerActionEdit})
	unsupported(&CreateViewerSessionService{Uri: e.root + "/Budget.gsheet", ViewerID: "office", PreferredAction: types.ViewerActionEdit})
	unsupported(&CreateViewerSessionService{Uri: e.root + "/letter.docx", ViewerID: "text", PreferredAction: types.ViewerActionEdit})
	unsupported(&CreateViewerSessionService{Uri: e.root + "/letter.docx", ViewerID: "office", Version: "v1", PreferredAction: types.ViewerActionEdit})
	_, err := (&CreateViewerSessionService{Uri: e.root + "/letter.docx", ViewerID: "office", PreferredAction: types.ViewerActionEdit}).Create(e.as(e.sam))
	require.Equal(t, serializer.CodeParentNotExist, connectedCode(err))
	_, err = e.officeSession(t, "/missing.docx", types.ViewerActionEdit)
	require.Equal(t, serializer.CodeParentNotExist, connectedCode(err))
	_, err = (&CreateViewerSessionService{Uri: e.root + "/letter.docx", ViewerID: "unknown", PreferredAction: types.ViewerActionEdit}).Create(e.as(e.alex))
	require.Equal(t, serializer.CodeParamErr, connectedCode(err))

	// Where the installation only reads connected storages, the editor only shows.
	t.Setenv(connected.EnvWrite, "")
	res, err := e.officeSession(t, "/letter.docx", types.ViewerActionEdit)
	require.NoError(t, err)
	require.False(t, *res.Edit)
	rec, _ := e.office(ConnectedOfficePut, http.MethodPost, res.Session.ID, res.Session.AccessToken, []byte("x"))
	require.Equal(t, http.StatusForbidden, rec.Code)
}

// The token is the permission, and it is checked in full with every call.
func TestConnectedOfficeTokens(t *testing.T) {
	e := newConnectedEnv(t)
	e.withOffice(t, false)
	e.sim.AddFile("", "letter.docx", []byte("a letter"))
	e.sim.AddFile("", "other.docx", []byte("another"))
	open := func() (string, string) {
		res, err := e.officeSession(t, "/letter.docx", types.ViewerActionEdit)
		require.NoError(t, err)
		return res.Session.ID, res.Session.AccessToken
	}
	refused := func(session, token, why string) {
		t.Helper()
		for _, call := range []struct {
			handler gin.HandlerFunc
			method  string
			body    []byte
		}{
			{ConnectedOfficeInfo, http.MethodGet, nil},
			{ConnectedOfficeGet, http.MethodGet, nil},
			{ConnectedOfficePut, http.MethodPost, []byte("not stored")},
			{ConnectedOfficeOther, http.MethodPost, nil},
		} {
			rec, _ := e.office(call.handler, call.method, session, token, call.body)
			require.Equal(t, http.StatusUnauthorized, rec.Code, why)
			require.Empty(t, rec.Body.String(), why)
		}
	}
	untouched := func() {
		t.Helper()
		require.Equal(t, []byte("a letter"), e.download(e.root+"/letter.docx"))
	}

	session, token := open()
	other, err := e.officeSession(t, "/other.docx", types.ViewerActionEdit)
	require.NoError(t, err)
	refused(session, "", "no token")
	refused(session, token+"x", "wrong token")
	refused(session, other.Session.AccessToken, "the token of another session")
	refused(other.Session.ID, token, "another session with this token")
	refused("co-nothing", token, "no such session")
	untouched()
	rec, _ := e.office(ConnectedOfficeInfo, http.MethodGet, session, token, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	// The Office server is switched off.
	e.withOffice(t, true)
	refused(session, token, "viewer switched off")
	e.withOffice(t, false)
	untouched()

	// The group loses the permission.
	g, err := e.client.Group.Query().Where(group.ID(2)).First(e.ctx)
	require.NoError(t, err)
	without := make(boolset.BooleanSet, len(*g.Permissions))
	copy(without, *g.Permissions)
	boolset.Set(types.GroupPermissionConnectedStorage, false, &without)
	require.NoError(t, e.client.Group.Update().Where(group.ID(2)).SetPermissions(&without).Exec(e.ctx))
	refused(session, token, "group without the permission")
	require.NoError(t, e.client.Group.Update().Where(group.ID(2)).SetPermissions(g.Permissions).Exec(e.ctx))
	untouched()
	rec, _ = e.office(ConnectedOfficeInfo, http.MethodGet, session, token, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	// A new password.
	require.NoError(t, e.client.User.UpdateOneID(e.alex.ID).SetPassword("another-hash").Exec(e.ctx))
	refused(session, token, "new password")
	e.alex = e.reload(e.alex.ID)
	untouched()

	// The storage is disconnected.
	session, token = open()
	rec, _ = e.office(ConnectedOfficeInfo, http.MethodGet, session, token, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, e.m.Disconnect(e.ctx, e.alex.ID, e.id))
	for _, handler := range []gin.HandlerFunc{ConnectedOfficeInfo, ConnectedOfficeGet} {
		rec, _ = e.office(handler, http.MethodGet, session, token, nil)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	}
}

// What changed since a session was opened counts: with changes switched off for the
// installation the editor is told it may only read, and a save is refused as forbidden.
// A call that must not have a body and has one is refused before anything else.
func TestConnectedOfficeAfterItWasOpened(t *testing.T) {
	e := newConnectedEnv(t)
	e.withOffice(t, false)
	e.sim.AddFile("", "letter.docx", []byte("a letter"))
	e.sim.AddFile("", "twice.docx", []byte("one"))
	e.sim.AddFile("", "twice.docx", []byte("two"))
	res, err := e.officeSession(t, "/letter.docx", types.ViewerActionEdit)
	require.NoError(t, err)
	session, token := res.Session.ID, res.Session.AccessToken

	info := func() WopiFileInfo {
		rec, _ := e.office(ConnectedOfficeInfo, http.MethodGet, session, token, nil)
		require.Equal(t, http.StatusOK, rec.Code)
		var info WopiFileInfo
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))
		return info
	}
	require.True(t, info().UserCanWrite)
	rec, _ := e.office(ConnectedOfficeGet, http.MethodGet, session, token, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	t.Setenv(connected.EnvWrite, "")
	require.False(t, info().UserCanWrite)
	require.True(t, info().ReadOnly)
	rec, _ = e.office(ConnectedOfficePut, http.MethodPost, session, token, []byte("not stored"))
	require.Equal(t, http.StatusForbidden, rec.Code)
	t.Setenv(connected.EnvWrite, "1")
	require.Equal(t, []byte("a letter"), e.download(e.root+"/letter.docx"))

	// The Office server no longer edits this kind of file: the same.
	e.withOfficeActions(t, false, map[types.ViewerAction]string{types.ViewerActionView: officeAddress + "/view?"})
	require.False(t, info().UserCanWrite)
	rec, _ = e.office(ConnectedOfficePut, http.MethodPost, session, token, []byte("not stored"))
	require.Equal(t, http.StatusForbidden, rec.Code)
	e.withOffice(t, false)
	require.True(t, info().UserCanWrite)
	require.Equal(t, []byte("a letter"), e.download(e.root+"/letter.docx"))

	// A call with a body where none belongs is refused, but not before its token was
	// looked at: a wrong one is still "no permission".
	rec, _ = e.office(ConnectedOfficeGet, http.MethodGet, session, token+"x", []byte("a body"))
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "close", rec.Header().Get("Connection"))

	for _, handler := range []gin.HandlerFunc{ConnectedOfficeInfo, ConnectedOfficeGet} {
		rec, _ = e.office(handler, http.MethodGet, session, token, []byte("a body where none belongs"))
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.Equal(t, "close", rec.Header().Get("Connection"))
	}

	// A name that stands for two entries has no session.
	_, err = e.officeSession(t, "/twice.docx", types.ViewerActionEdit)
	require.Equal(t, serializer.CodeConnectedUnsupported, connectedCode(err))
}

// The file of a window is deleted and another one takes its name: the save is a conflict
// (the editor asks its user), not "not found", and nothing is written to the other file.
// With nothing at the address it is the same: for the editor the document changed in
// storage, which it can tell its user, where "not found" reads as "session expired".
func TestConnectedOfficeFileGoneFromItsAddress(t *testing.T) {
	e := newConnectedEnv(t)
	e.withOffice(t, false)
	id := e.sim.AddFile("", "letter.docx", []byte("a letter"))
	res, err := e.officeSession(t, "/letter.docx", types.ViewerActionEdit)
	require.NoError(t, err)
	session, token := res.Session.ID, res.Session.AccessToken
	rec, _ := e.office(ConnectedOfficeGet, http.MethodGet, session, token, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	e.sim.Delete(id)
	rec, _ = e.office(ConnectedOfficePut, http.MethodPost, session, token, []byte("an edit"))
	require.Equal(t, http.StatusConflict, rec.Code)
	var conflict map[string]int
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &conflict))
	require.Equal(t, 1010, conflict["COOLStatusCode"])

	e.sim.AddFile("", "letter.docx", []byte("another letter"))
	rec, _ = e.office(ConnectedOfficePut, http.MethodPost, session, token, []byte("an edit"))
	require.Equal(t, http.StatusConflict, rec.Code)
	conflict = nil
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &conflict))
	require.Equal(t, 1010, conflict["COOLStatusCode"])
	require.Equal(t, []byte("another letter"), e.download(e.root+"/letter.docx"))
}

// A window is closed while its file has changed elsewhere. Nobody is left to ask, so
// what the user wrote is kept as a new file beside the one that changed. Nothing is
// replaced and nothing is lost.
func TestConnectedOfficeLastSaveIsKeptBesideAConflict(t *testing.T) {
	e := newConnectedEnv(t)
	e.withOffice(t, false)
	id := e.sim.AddFile("", "letter.docx", []byte("version one"))
	folder := e.sim.AddFolder("", "Docs")
	planID := e.sim.AddFile(folder, "plan.v2.docx", []byte("a plan"))
	open := func(path string) (session, token string) {
		t.Helper()
		res, err := e.officeSession(t, path, types.ViewerActionEdit)
		require.NoError(t, err)
		rec, _ := e.office(ConnectedOfficeGet, http.MethodGet, res.Session.ID, res.Session.AccessToken, nil)
		require.Equal(t, http.StatusOK, rec.Code)
		return res.Session.ID, res.Session.AccessToken
	}
	names := func(path string) string {
		t.Helper()
		e.m.Tree().Forget(e.id)
		res, err := e.list(e.alex, e.root+path, 100, "")
		require.NoError(t, err)
		return fileNames(res.Files)
	}
	before := e.officeTemps()

	// A last save without a conflict is an ordinary save.
	session, token := open("/letter.docx")
	rec := e.officeExit(session, token, []byte("version two"))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, []byte("version two"), e.download(e.root+"/letter.docx"))
	require.Equal(t, "Docs|letter.docx", names(""))

	// Two windows. The first saves; the second is closed with changes of its own.
	first, firstToken := open("/letter.docx")
	second, secondToken := open("/letter.docx")
	rec, _ = e.office(ConnectedOfficePut, http.MethodPost, first, firstToken, []byte("from the first window"))
	require.Equal(t, http.StatusOK, rec.Code)
	// A save while the window is still open is the conflict the editor asks about.
	// What it wanted to save is beside the file from that moment on.
	rec, _ = e.office(ConnectedOfficePut, http.MethodPost, second, secondToken, []byte("from the second window"))
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, "Docs|letter (conflict).docx|letter.docx", names(""))
	require.Equal(t, []byte("from the second window"), e.download(e.root+"/letter (conflict).docx"))
	// The window is closed, with a little more in it: that goes into the same copy.
	rec = e.officeExit(second, secondToken, []byte("from the second window, a little more"))
	require.Equal(t, http.StatusOK, rec.Code)
	var saved map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &saved))
	require.NotEmpty(t, saved["LastModifiedTime"])
	require.Equal(t, "Docs|letter (conflict).docx|letter.docx", names(""))
	require.Equal(t, []byte("from the first window"), e.download(e.root+"/letter.docx"))
	require.Equal(t, []byte("from the second window, a little more"), e.download(e.root+"/letter (conflict).docx"))

	// Once more: the name of the first copy is taken, the next one gets a number. The
	// conflict is found by this very save, not by one before it.
	third, thirdToken := open("/letter.docx")
	e.sim.SetContent(id, []byte("changed at the provider"))
	rec = e.officeExit(third, thirdToken, []byte("from the third window"))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "Docs|letter (conflict 2).docx|letter (conflict).docx|letter.docx", names(""))
	require.Equal(t, []byte("from the third window"), e.download(e.root+"/letter (conflict 2).docx"))
	require.Equal(t, []byte("changed at the provider"), e.download(e.root+"/letter.docx"))

	// The file was renamed while its window was open: for the editor a conflict, and
	// the copy lies where the file was opened, under the name the file has now.
	inFolder, inFolderToken := open("/Docs/plan.v2.docx")
	e.sim.Rename(planID, "final plan.docx")
	rec, _ = e.office(ConnectedOfficePut, http.MethodPost, inFolder, inFolderToken, []byte("an edit"))
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, "final plan (conflict).docx|final plan.docx", names("/Docs"))
	rec = e.officeExit(inFolder, inFolderToken, []byte("an edit, kept"))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "final plan (conflict).docx|final plan.docx", names("/Docs"))
	require.Equal(t, []byte("a plan"), e.download(e.root+"/Docs/final plan.docx"))
	require.Equal(t, []byte("an edit, kept"), e.download(e.root+"/Docs/final plan (conflict).docx"))

	// The file is deleted while its window is open: the same.
	gone, goneToken := open("/letter.docx")
	e.sim.Delete(id)
	rec = e.officeExit(gone, goneToken, []byte("all that is left of it"))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, []byte("all that is left of it"), e.download(e.root+"/letter (conflict 3).docx"))

	// An account that reads German gets a German word.
	require.NoError(t, e.client.User.UpdateOneID(e.alex.ID).SetSettings(&types.UserSetting{Language: "de-CH"}).Exec(e.ctx))
	e.alex = e.reload(e.alex.ID)
	briefID := e.sim.AddFile("", "brief.docx", []byte("ein Brief"))
	german, germanToken := open("/brief.docx")
	e.sim.SetContent(briefID, []byte("anders"))
	rec = e.officeExit(german, germanToken, []byte("mein Brief"))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, []byte("mein Brief"), e.download(e.root+"/brief (Konflikt).docx"))

	// Nothing of a document is left on the server's own disk.
	require.Equal(t, before, e.officeTemps())
}

// The last save again, because its answer did not arrive: the copy that was made is the
// answer, and no second one is made. Other content is another copy.
func TestConnectedOfficeLastSaveSentTwice(t *testing.T) {
	e := newConnectedEnv(t)
	e.withOffice(t, false)
	id := e.sim.AddFile("", "letter.docx", []byte("version one"))
	res, err := e.officeSession(t, "/letter.docx", types.ViewerActionEdit)
	require.NoError(t, err)
	session, token := res.Session.ID, res.Session.AccessToken
	rec, _ := e.office(ConnectedOfficeGet, http.MethodGet, session, token, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	e.sim.SetContent(id, []byte("changed at the provider"))

	first := e.officeExit(session, token, []byte("what the user wrote"))
	require.Equal(t, http.StatusOK, first.Code)
	second := e.officeExit(session, token, []byte("what the user wrote"))
	require.Equal(t, http.StatusOK, second.Code)
	require.JSONEq(t, first.Body.String(), second.Body.String())
	e.m.Tree().Forget(e.id)
	list, err := e.list(e.alex, e.root, 100, "")
	require.NoError(t, err)
	require.Equal(t, "letter (conflict).docx|letter.docx", fileNames(list.Files))

	// Other content of the same window, before it read the file again, is a newer
	// state of the same document: it takes the place of the first in the copy.
	third := e.officeExit(session, token, []byte("what the user wrote, and more"))
	require.Equal(t, http.StatusOK, third.Code)
	e.m.Tree().Forget(e.id)
	list, err = e.list(e.alex, e.root, 100, "")
	require.NoError(t, err)
	require.Equal(t, "letter (conflict).docx|letter.docx", fileNames(list.Files))
	require.Equal(t, []byte("what the user wrote, and more"), e.download(e.root+"/letter (conflict).docx"))

	// The window reads the file again, and then the first last save comes once more,
	// late: it is still its copy. It is not stored over the file the window has read
	// meanwhile.
	rec, _ = e.office(ConnectedOfficeGet, http.MethodGet, session, token, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	// The first one comes once more, late: still its copy, no third one.
	late := e.officeExit(session, token, []byte("what the user wrote"))
	require.Equal(t, http.StatusOK, late.Code)
	require.JSONEq(t, first.Body.String(), late.Body.String())
	e.m.Tree().Forget(e.id)
	list, err = e.list(e.alex, e.root, 100, "")
	require.NoError(t, err)
	require.Len(t, list.Files, 2)
	require.Equal(t, []byte("changed at the provider"), e.download(e.root+"/letter.docx"))
	require.Equal(t, []byte("what the user wrote, and more"), e.download(e.root+"/letter (conflict).docx"))
}

// What a real Office server does after it told its user of a conflict: it may send
// nothing more, not even when the window is closed. So what a save could not put into
// the file is beside the file from the first refusal on, whatever becomes of the window.
func TestConnectedOfficeConflictIsKeptAtOnce(t *testing.T) {
	e := newConnectedEnv(t)
	e.withOffice(t, false)
	id := e.sim.AddFile("", "letter.docx", []byte("version one"))
	open := func(path string) (session, token string) {
		t.Helper()
		res, err := e.officeSession(t, path, types.ViewerActionEdit)
		require.NoError(t, err)
		rec, _ := e.office(ConnectedOfficeGet, http.MethodGet, res.Session.ID, res.Session.AccessToken, nil)
		require.Equal(t, http.StatusOK, rec.Code)
		return res.Session.ID, res.Session.AccessToken
	}
	names := func() string {
		t.Helper()
		e.m.Tree().Forget(e.id)
		res, err := e.list(e.alex, e.root, 100, "")
		require.NoError(t, err)
		return fileNames(res.Files)
	}
	save := func(session, token, body string) int {
		t.Helper()
		rec, _ := e.office(ConnectedOfficePut, http.MethodPost, session, token, []byte(body))
		return rec.Code
	}
	before := e.officeTemps()

	// A save that goes through keeps nothing beside the file.
	session, token := open("/letter.docx")
	require.Equal(t, http.StatusOK, save(session, token, "version two"))
	require.Equal(t, "letter.docx", names())

	// The file changes elsewhere. The window saves: the editor is told of the conflict
	// and asks its user. The window is then closed without another save.
	e.sim.SetContent(id, []byte("changed at the provider"))
	require.Equal(t, http.StatusConflict, save(session, token, "what the user wrote"))
	require.Equal(t, "letter (conflict).docx|letter.docx", names())
	require.Equal(t, []byte("what the user wrote"), e.download(e.root+"/letter (conflict).docx"))
	require.Equal(t, []byte("changed at the provider"), e.download(e.root+"/letter.docx"))

	// The user goes on typing and the editor saves again, and again: still the
	// conflict, and the one copy holds the newest state. The same state sent again
	// costs no upload.
	require.Equal(t, http.StatusConflict, save(session, token, "what the user wrote, and more"))
	require.Equal(t, "letter (conflict).docx|letter.docx", names())
	require.Equal(t, []byte("what the user wrote, and more"), e.download(e.root+"/letter (conflict).docx"))
	uploads := e.sim.Calls(drivetest.CallStart)
	require.Equal(t, http.StatusConflict, save(session, token, "what the user wrote, and more"))
	require.Equal(t, uploads, e.sim.Calls(drivetest.CallStart))
	require.Equal(t, "letter (conflict).docx|letter.docx", names())

	// The user lets the editor load the document again and works on from there. A
	// conflict after that is another state, with a copy of its own.
	rec, _ := e.office(ConnectedOfficeGet, http.MethodGet, session, token, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, http.StatusOK, save(session, token, "on top of what was loaded"))
	e.sim.SetContent(id, []byte("changed once more"))
	require.Equal(t, http.StatusConflict, save(session, token, "a second try"))
	require.Equal(t, "letter (conflict 2).docx|letter (conflict).docx|letter.docx", names())
	require.Equal(t, []byte("what the user wrote, and more"), e.download(e.root+"/letter (conflict).docx"))
	require.Equal(t, []byte("a second try"), e.download(e.root+"/letter (conflict 2).docx"))

	// Somebody took the copy for their own (changed it, here): it is left alone, and
	// what the window saves next gets a new one.
	e.sim.SetContent(e.sim.Find("", "letter (conflict 2).docx"), []byte("somebody's own by now"))
	require.Equal(t, http.StatusConflict, save(session, token, "a third try"))
	require.Equal(t, "letter (conflict 2).docx|letter (conflict 3).docx|letter (conflict).docx|letter.docx", names())
	require.Equal(t, []byte("somebody's own by now"), e.download(e.root+"/letter (conflict 2).docx"))
	require.Equal(t, []byte("a third try"), e.download(e.root+"/letter (conflict 3).docx"))

	// A save the Office server marks as holding nothing of its user is the conflict
	// and no copy.
	other, otherToken := open("/letter.docx")
	e.sim.SetContent(id, []byte("and again"))
	e.officeHeaders = map[string]string{"X-COOL-WOPI-IsModifiedByUser": "false"}
	require.Equal(t, http.StatusConflict, save(other, otherToken, "as it was read"))
	e.officeHeaders = nil
	require.Len(t, strings.Split(names(), "|"), 4)

	// A copy that is edited and meets a conflict itself gets a further copy of the
	// file, not a copy of a copy.
	copyID := e.sim.Find("", "letter (conflict).docx")
	require.NotEmpty(t, copyID)
	ofCopy, ofCopyToken := open("/letter (conflict).docx")
	e.sim.SetContent(copyID, []byte("the copy changed too"))
	require.Equal(t, http.StatusConflict, save(ofCopy, ofCopyToken, "an edit of the copy"))
	require.Equal(t, "letter (conflict 2).docx|letter (conflict 3).docx|letter (conflict 4).docx|letter (conflict).docx|letter.docx", names())
	require.Equal(t, []byte("an edit of the copy"), e.download(e.root+"/letter (conflict 4).docx"))

	// The storage is full: nothing can be kept, and the conflict is all there is.
	full, fullToken := open("/letter.docx")
	e.sim.SetContent(id, []byte("one last change"))
	e.sim.SetFull(true)
	require.Equal(t, http.StatusConflict, save(full, fullToken, "not kept"))
	e.sim.SetFull(false)
	require.Len(t, strings.Split(names(), "|"), 5)

	// Nothing of a document is left on the server's own disk.
	require.Equal(t, before, e.officeTemps())
}

// A last save through a window that never read the file: there is nothing of a user in
// it, so it is the conflict and no copy.
func TestConnectedOfficeLastSaveOfAWindowThatReadNothing(t *testing.T) {
	e := newConnectedEnv(t)
	e.withOffice(t, false)
	e.sim.AddFile("", "letter.docx", []byte("version one"))
	res, err := e.officeSession(t, "/letter.docx", types.ViewerActionEdit)
	require.NoError(t, err)
	rec := e.officeExit(res.Session.ID, res.Session.AccessToken, []byte("made up"))
	require.Equal(t, http.StatusConflict, rec.Code)
	e.m.Tree().Forget(e.id)
	list, err := e.list(e.alex, e.root, 100, "")
	require.NoError(t, err)
	require.Equal(t, "letter.docx", fileNames(list.Files))
	require.Equal(t, []byte("version one"), e.download(e.root+"/letter.docx"))
}

// The file is deleted at the provider in the very moment of the last save, while the
// upload for it is under way: a conflict like any other, and the content is kept.
func TestConnectedOfficeLastSaveMeetsADeletionMidway(t *testing.T) {
	e := newConnectedEnv(t)
	e.withOffice(t, false)
	id := e.sim.AddFile("", "letter.docx", []byte("version one"))
	res, err := e.officeSession(t, "/letter.docx", types.ViewerActionEdit)
	require.NoError(t, err)
	session, token := res.Session.ID, res.Session.AccessToken
	rec, _ := e.office(ConnectedOfficeGet, http.MethodGet, session, token, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	e.sim.DeleteOnNextUpload(id)
	rec = e.officeExit(session, token, []byte("what the user wrote"))
	require.Equal(t, http.StatusOK, rec.Code)
	e.m.Tree().Forget(e.id)
	list, err := e.list(e.alex, e.root, 100, "")
	require.NoError(t, err)
	require.Equal(t, "letter (conflict).docx", fileNames(list.Files))
	require.Equal(t, []byte("what the user wrote"), e.download(e.root+"/letter (conflict).docx"))

	// Without the window closing it is the conflict the editor asks about.
	other := e.sim.AddFile("", "other.docx", []byte("version one"))
	res, err = e.officeSession(t, "/other.docx", types.ViewerActionEdit)
	require.NoError(t, err)
	rec, _ = e.office(ConnectedOfficeGet, http.MethodGet, res.Session.ID, res.Session.AccessToken, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	e.sim.DeleteOnNextUpload(other)
	rec, _ = e.office(ConnectedOfficePut, http.MethodPost, res.Session.ID, res.Session.AccessToken, []byte("an edit"))
	require.Equal(t, http.StatusConflict, rec.Code)
	// What it wanted to save is kept all the same.
	require.Equal(t, []byte("an edit"), e.download(e.root+"/other (conflict).docx"))
}

// Drive lets a file carry a name that Fylvo gives to nothing new. Such a file can be
// edited, and its copy gets a name that keeps the rules.
func TestConnectedOfficeLastSaveOfANameFylvoWouldNotGive(t *testing.T) {
	e := newConnectedEnv(t)
	e.withOffice(t, false)
	for name, want := range map[string]string{
		"Notes 10:30.docx":   "Notes 10_30 (conflict).docx",
		"A <b> | \"c\".docx": "A _b_ _ _c_ (conflict).docx",
		" draft.docx":        "draft (conflict).docx",
		".docx":              "(conflict).docx",
	} {
		id := e.sim.AddFile("", name, []byte("version one"))
		e.m.Tree().Forget(e.id)
		res, err := e.officeSession(t, "/"+name, types.ViewerActionEdit)
		require.NoError(t, err, name)
		require.True(t, *res.Edit, name)
		rec, _ := e.office(ConnectedOfficeGet, http.MethodGet, res.Session.ID, res.Session.AccessToken, nil)
		require.Equal(t, http.StatusOK, rec.Code, name)
		e.sim.SetContent(id, []byte("changed at the provider"))
		rec = e.officeExit(res.Session.ID, res.Session.AccessToken, []byte("kept of "+name))
		require.Equal(t, http.StatusOK, rec.Code, name)
		e.m.Tree().Forget(e.id)
		require.Equal(t, []byte("kept of "+name), e.download(e.root+"/"+want), name)
	}
}

// The file changes at the provider in the very moment of the last save, after the server
// looked and while it stores: the content is already read by then, and is kept all the same.
func TestConnectedOfficeLastSaveMeetsAChangeMidway(t *testing.T) {
	e := newConnectedEnv(t)
	e.withOffice(t, false)
	id := e.sim.AddFile("", "letter.docx", []byte("version one"))
	res, err := e.officeSession(t, "/letter.docx", types.ViewerActionEdit)
	require.NoError(t, err)
	session, token := res.Session.ID, res.Session.AccessToken
	rec, _ := e.office(ConnectedOfficeGet, http.MethodGet, session, token, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	e.sim.ChangeOnNextUpload(id, []byte("changed in that moment"))
	rec = e.officeExit(session, token, []byte("what the user wrote"))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, []byte("changed in that moment"), e.download(e.root+"/letter.docx"))
	require.Equal(t, []byte("what the user wrote"), e.download(e.root+"/letter (conflict).docx"))
}

// The folder of the file is renamed and another folder takes its path. The copy is for
// the folder the file was in: nothing is put into a folder that only has its name now.
// With no folder at the path the save is a conflict too, not "not found".
func TestConnectedOfficeLastSaveKeepsToItsFolder(t *testing.T) {
	e := newConnectedEnv(t)
	e.withOffice(t, false)
	private := e.sim.AddFolder("", "Private")
	e.sim.AddFile(private, "letter.docx", []byte("version one"))
	res, err := e.officeSession(t, "/Private/letter.docx", types.ViewerActionEdit)
	require.NoError(t, err)
	session, token := res.Session.ID, res.Session.AccessToken
	rec, _ := e.office(ConnectedOfficeGet, http.MethodGet, session, token, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	// The folder gets another name: nothing is at the path of the window any more.
	e.sim.Rename(private, "Archive")
	e.m.Tree().Forget(e.id)
	rec, _ = e.office(ConnectedOfficePut, http.MethodPost, session, token, []byte("an edit"))
	require.Equal(t, http.StatusConflict, rec.Code)
	rec = e.officeExit(session, token, []byte("an edit"))
	require.Equal(t, http.StatusConflict, rec.Code)

	// Another folder takes the name, one that others can see into.
	shared := e.sim.AddFolder("", "Private")
	e.m.Tree().Forget(e.id)
	rec = e.officeExit(session, token, []byte("an edit"))
	require.Equal(t, http.StatusConflict, rec.Code)
	list, err := e.list(e.alex, e.root+"/Private", 100, "")
	require.NoError(t, err)
	require.Empty(t, list.Files, "nothing was put into the folder that took the name")
	_ = shared
	list, err = e.list(e.alex, e.root+"/Archive", 100, "")
	require.NoError(t, err)
	require.Equal(t, "letter.docx", fileNames(list.Files))
	require.Equal(t, []byte("version one"), e.download(e.root+"/Archive/letter.docx"))
}

// A name as long as a name can be: the copy gives up the end of the name, never the
// mark that says what it is, and never the extension.
func TestConnectedOfficeLastSaveOfALongName(t *testing.T) {
	e := newConnectedEnv(t)
	e.withOffice(t, false)
	long := strings.Repeat("ä", 124) + "x.docx" // 248 + 1 + 5 = 254 bytes
	require.Len(t, long, 254)
	id := e.sim.AddFile("", long, []byte("version one"))
	res, err := e.officeSession(t, "/"+long, types.ViewerActionEdit)
	require.NoError(t, err)
	session, token := res.Session.ID, res.Session.AccessToken
	rec, _ := e.office(ConnectedOfficeGet, http.MethodGet, session, token, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	e.sim.SetContent(id, []byte("changed at the provider"))

	rec = e.officeExit(session, token, []byte("what the user wrote"))
	require.Equal(t, http.StatusOK, rec.Code)
	e.m.Tree().Forget(e.id)
	list, err := e.list(e.alex, e.root, 100, "")
	require.NoError(t, err)
	require.Len(t, list.Files, 2)
	var beside string
	for _, f := range list.Files {
		if f.Name != long {
			beside = f.Name
		}
	}
	require.LessOrEqual(t, len(beside), 255)
	require.True(t, strings.HasSuffix(beside, " (conflict).docx"), beside)
	require.True(t, strings.HasPrefix(beside, strings.Repeat("ä", 100)), beside)
	require.True(t, utf8.ValidString(beside))
	require.Equal(t, []byte("what the user wrote"), e.download(e.root+"/"+beside))
}

// Where the last save cannot be kept either, the answer is the conflict, as for any
// other save: a window that only shows, changes switched off, a body that breaks off.
func TestConnectedOfficeLastSaveThatCannotBeKept(t *testing.T) {
	e := newConnectedEnv(t)
	e.withOffice(t, false)
	id := e.sim.AddFile("", "letter.docx", []byte("version one"))
	res, err := e.officeSession(t, "/letter.docx", types.ViewerActionEdit)
	require.NoError(t, err)
	session, token := res.Session.ID, res.Session.AccessToken
	rec, _ := e.office(ConnectedOfficeGet, http.MethodGet, session, token, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	e.sim.SetContent(id, []byte("changed at the provider"))

	// Changes are switched off for the installation: nothing is written anywhere.
	t.Setenv(connected.EnvWrite, "")
	rec = e.officeExit(session, token, []byte("not stored"))
	require.Equal(t, http.StatusForbidden, rec.Code)
	t.Setenv(connected.EnvWrite, "1")

	// The storage is full: no copy, so the conflict is the answer.
	e.sim.SetFull(true)
	rec = e.officeExit(session, token, []byte("not stored"))
	require.Equal(t, http.StatusConflict, rec.Code)
	e.sim.SetFull(false)

	e.m.Tree().Forget(e.id)
	list, err := e.list(e.alex, e.root, 100, "")
	require.NoError(t, err)
	require.Equal(t, "letter.docx", fileNames(list.Files))
	require.Equal(t, []byte("changed at the provider"), e.download(e.root+"/letter.docx"))

	// A last save of a window nobody typed in (an Office server set up to save on every
	// close sends one): there is nothing of the user's to keep, so it is the conflict.
	e.officeHeaders = map[string]string{"X-COOL-WOPI-IsExitSave": "true", "X-COOL-WOPI-IsModifiedByUser": "false"}
	rec, _ = e.office(ConnectedOfficePut, http.MethodPost, session, token, []byte("as it was read"))
	e.officeHeaders = nil
	require.Equal(t, http.StatusConflict, rec.Code)

	// A body that breaks off before its announced end: nothing is kept of it, on the
	// storage or on the server's own disk.
	before := e.officeTemps()
	short, shortRec := e.request(nil, http.MethodPost, "/api/v4/fylvo/connected/wopi/"+session+"?access_token="+url.QueryEscape(token))
	short.Params = gin.Params{{Key: "session", Value: session}}
	short.Request.Header.Set("X-COOL-WOPI-IsExitSave", "true")
	short.Request.Body = io.NopCloser(bytes.NewReader([]byte("only a part")))
	short.Request.ContentLength = 500
	ConnectedOfficePut(short)
	require.GreaterOrEqual(t, shortRec.Code, 400)
	require.Equal(t, before, e.officeTemps())

	// A wrong token gets nothing in, exit save or not.
	rec = e.officeExit(session, token+"x", []byte("not stored"))
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	// And a window that only shows keeps nothing.
	view, err := e.officeSession(t, "/letter.docx", types.ViewerActionView)
	require.NoError(t, err)
	rec = e.officeExit(view.Session.ID, view.Session.AccessToken, []byte("not stored"))
	require.Equal(t, http.StatusForbidden, rec.Code)
	e.m.Tree().Forget(e.id)
	list, err = e.list(e.alex, e.root, 100, "")
	require.NoError(t, err)
	require.Equal(t, "letter.docx", fileNames(list.Files))
}

// What cannot succeed opens nothing: a kind of file the Office server has no usable
// action for answers "not supported" (and "not found" when there is no such file), and
// makes no other window give way.
func TestConnectedOfficeNothingOpensForNothing(t *testing.T) {
	e := newConnectedEnv(t)
	e.sim.AddFile("", "letter.docx", []byte("a letter"))
	e.sim.AddFile("", "notes.txt", []byte("notes"))
	e.withOffice(t, false)
	first, err := e.officeSession(t, "/letter.docx", types.ViewerActionEdit)
	require.NoError(t, err)

	// The viewer knows the kind of file and has no action for it.
	e.withOfficeActions(t, false, map[types.ViewerAction]string{})
	_, err = e.officeSession(t, "/letter.docx", types.ViewerActionEdit)
	require.Equal(t, serializer.CodeConnectedUnsupported, connectedCode(err))
	_, err = e.officeSession(t, "/notes.txt", types.ViewerActionEdit)
	require.Equal(t, serializer.CodeConnectedUnsupported, connectedCode(err))
	_, err = e.officeSession(t, "/missing.txt", types.ViewerActionEdit)
	require.Equal(t, serializer.CodeParentNotExist, connectedCode(err))

	// An action that is no full web address would lead to this server, not to the
	// Office server: it is no action. With every place taken, such a request makes no
	// window give way.
	e.withOffice(t, false)
	for i := 1; i < 50; i++ {
		_, err = e.officeSession(t, "/letter.docx", types.ViewerActionView)
		require.NoError(t, err)
	}
	e.withOfficeActions(t, false, map[types.ViewerAction]string{types.ViewerActionEdit: "editor/open?", types.ViewerActionView: "/view?"})
	_, err = e.officeSession(t, "/letter.docx", types.ViewerActionEdit)
	require.Equal(t, serializer.CodeConnectedUnsupported, connectedCode(err))
	// And a window that is open no longer saves through an editor set up like that.
	rec, _ := e.office(ConnectedOfficePut, http.MethodPost, first.Session.ID, first.Session.AccessToken, []byte("not stored"))
	require.Equal(t, http.StatusForbidden, rec.Code)

	e.withOffice(t, false)
	rec, _ = e.office(ConnectedOfficeInfo, http.MethodGet, first.Session.ID, first.Session.AccessToken, nil)
	require.Equal(t, http.StatusOK, rec.Code, "the window that was open longest is still open")
	require.Equal(t, []byte("a letter"), e.download(e.root+"/letter.docx"))
}

// A file that was renamed while it is open is answered by the save (the address is
// empty: gone), also when the editor does not edit files of its new name.
func TestConnectedOfficeRenamedToAnotherKind(t *testing.T) {
	e := newConnectedEnv(t)
	e.withOffice(t, false)
	id := e.sim.AddFile("", "letter.docx", []byte("a letter"))
	res, err := e.officeSession(t, "/letter.docx", types.ViewerActionEdit)
	require.NoError(t, err)
	session, token := res.Session.ID, res.Session.AccessToken
	rec, _ := e.office(ConnectedOfficeGet, http.MethodGet, session, token, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	e.sim.Rename(id, "letter.txt")
	rec, _ = e.office(ConnectedOfficeInfo, http.MethodGet, session, token, nil)
	var info WopiFileInfo
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))
	require.Equal(t, "letter.txt", info.BaseFileName)
	require.False(t, info.UserCanWrite, "the editor does not edit files of this name")
	// The file is no longer at the address of the window: a conflict, which the editor
	// can tell its user, and nothing is stored.
	rec, _ = e.office(ConnectedOfficePut, http.MethodPost, session, token, []byte("an edit"))
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, []byte("a letter"), e.download(e.root+"/letter.txt"))
}

// The editor is asked for by its edit action when editing is what will happen; a view
// action that is unusable does not stand in the way then. And a viewer of another kind
// under the same id opens nothing for a session that is still open.
func TestConnectedOfficeActionThatIsUsed(t *testing.T) {
	e := newConnectedEnv(t)
	e.sim.AddFile("", "letter.docx", []byte("a letter"))
	e.withOfficeActions(t, false, map[types.ViewerAction]string{types.ViewerActionEdit: officeAddress + "/edit?", types.ViewerActionView: "/view?"})
	res, err := e.officeSession(t, "/letter.docx", types.ViewerActionEdit)
	require.NoError(t, err)
	src, _ := url.Parse(res.WopiSrc)
	require.Equal(t, "/edit", src.Path)
	_, err = e.officeSession(t, "/letter.docx", types.ViewerActionView)
	require.Equal(t, serializer.CodeConnectedUnsupported, connectedCode(err))

	// The viewer with this id is no Office server any more.
	other := types.Viewer{ID: "office", Type: types.ViewerTypeBuiltin, DisplayName: "Other", Exts: []string{"docx"},
		WopiActions: map[string]map[types.ViewerAction]string{"docx": {types.ViewerActionEdit: officeAddress + "/edit?"}}}
	raw, err := json.Marshal([]types.ViewerGroup{{Viewers: []types.Viewer{other}}})
	require.NoError(t, err)
	e.set(t, "file_viewers", string(raw))
	rec, _ := e.office(ConnectedOfficeInfo, http.MethodGet, res.Session.ID, res.Session.AccessToken, nil)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	rec, _ = e.office(ConnectedOfficePut, http.MethodPost, res.Session.ID, res.Session.AccessToken, []byte("not stored"))
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, []byte("a letter"), e.download(e.root+"/letter.docx"))
}

// The one copy of a conflict holds what the window sent last, and only as long as it is
// the copy this window left: taken away, renamed or lost in the middle of an upload,
// what the window sends goes into a new one.
func TestConnectedOfficeConflictCopyIsFollowed(t *testing.T) {
	e := newConnectedEnv(t)
	e.withOffice(t, false)
	id := e.sim.AddFile("", "letter.docx", []byte("version one"))
	res, err := e.officeSession(t, "/letter.docx", types.ViewerActionEdit)
	require.NoError(t, err)
	session, token := res.Session.ID, res.Session.AccessToken
	read := func() {
		t.Helper()
		rec, _ := e.office(ConnectedOfficeGet, http.MethodGet, session, token, nil)
		require.Equal(t, http.StatusOK, rec.Code)
	}
	names := func() string {
		t.Helper()
		e.m.Tree().Forget(e.id)
		res, err := e.list(e.alex, e.root, 100, "")
		require.NoError(t, err)
		return fileNames(res.Files)
	}
	refused := func(body string) {
		t.Helper()
		rec, _ := e.office(ConnectedOfficePut, http.MethodPost, session, token, []byte(body))
		require.Equal(t, http.StatusConflict, rec.Code, body)
	}
	copyOf := func(name string) []byte { return e.download(e.root + "/" + name) }
	read()
	e.sim.SetContent(id, []byte("changed at the provider"))

	// One state, another, and the first again (the user took a change back): the copy
	// holds what came last, not what it once held.
	refused("state A")
	refused("state B")
	refused("state A")
	require.Equal(t, "letter (conflict).docx|letter.docx", names())
	require.Equal(t, []byte("state A"), copyOf("letter (conflict).docx"))

	// Somebody deletes the copy. The same state sent again is stored again.
	e.sim.Delete(e.sim.Find("", "letter (conflict).docx"))
	refused("state A")
	require.Equal(t, "letter (conflict).docx|letter.docx", names())
	require.Equal(t, []byte("state A"), copyOf("letter (conflict).docx"))

	// Somebody renames the copy: it is theirs, with what it holds. The same state sent
	// again gets a new one, and the next state goes into that.
	e.sim.Rename(e.sim.Find("", "letter (conflict).docx"), "mine now.docx")
	refused("state A")
	require.Equal(t, "letter (conflict).docx|letter.docx|mine now.docx", names())
	require.Equal(t, []byte("state A"), copyOf("letter (conflict).docx"))
	refused("state C")
	require.Equal(t, "letter (conflict).docx|letter.docx|mine now.docx", names())
	require.Equal(t, []byte("state A"), copyOf("mine now.docx"))
	require.Equal(t, []byte("state C"), copyOf("letter (conflict).docx"))
	// And once more with a new state right after the rename.
	e.sim.Rename(e.sim.Find("", "letter (conflict).docx"), "mine too.docx")
	refused("state C2")
	require.Equal(t, "letter (conflict).docx|letter.docx|mine now.docx|mine too.docx", names())
	require.Equal(t, []byte("state C"), copyOf("mine too.docx"))
	require.Equal(t, []byte("state C2"), copyOf("letter (conflict).docx"))
	e.sim.Delete(e.sim.Find("", "mine too.docx"))

	// The copy goes away in the middle of the upload that was to renew it: the state
	// is stored in a new one, not lost.
	e.sim.DeleteOnNextUpload(e.sim.Find("", "letter (conflict).docx"))
	refused("state D")
	require.Equal(t, "letter (conflict).docx|letter.docx|mine now.docx", names())
	require.Equal(t, []byte("state D"), copyOf("letter (conflict).docx"))

	// Somebody moves the copy into a folder: the same.
	other := e.sim.AddFolder("", "Elsewhere")
	e.sim.Move(e.sim.Find("", "letter (conflict).docx"), other)
	refused("state D")
	require.Equal(t, "Elsewhere|letter (conflict).docx|letter.docx|mine now.docx", names())
	require.Equal(t, []byte("state D"), copyOf("letter (conflict).docx"))
	require.Equal(t, []byte("state D"), copyOf("Elsewhere/letter (conflict).docx"))
	e.sim.Delete(other)

	// Many states later the window is closed, more than once. Each last save is
	// answered as saved, and the first of them sent again, also after the window read
	// the file anew, never goes over the file.
	for i := 0; i < 12; i++ {
		refused(fmt.Sprintf("state %d", i))
	}
	first := e.officeExit(session, token, []byte("the last state"))
	require.Equal(t, http.StatusOK, first.Code)
	for i := 0; i < 12; i++ {
		rec := e.officeExit(session, token, []byte(fmt.Sprintf("a later last state %d", i)))
		require.Equal(t, http.StatusOK, rec.Code)
	}
	rec := e.officeExit(session, token, []byte("the last state"))
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, first.Body.String(), rec.Body.String())
	rec = e.officeExit(session, token, []byte("the last state of all"))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, []byte("the last state of all"), copyOf("letter (conflict).docx"))
	read()
	for i := 0; i < 12; i++ {
		rec, _ := e.office(ConnectedOfficePut, http.MethodPost, session, token, []byte(fmt.Sprintf("saved %d", i)))
		require.Equal(t, http.StatusOK, rec.Code)
	}
	again := e.officeExit(session, token, []byte("the last state"))
	require.Equal(t, http.StatusOK, again.Code)
	require.JSONEq(t, first.Body.String(), again.Body.String())
	require.Equal(t, []byte("saved 11"), e.download(e.root+"/letter.docx"))
	require.Equal(t, "letter (conflict).docx|letter.docx|mine now.docx", names())

	// After the window read the file again, a state it had before is a state of the
	// new document: refused, it gets its own copy.
	e.sim.SetContent(id, []byte("changed once more"))
	refused("state A")
	require.Equal(t, "letter (conflict 2).docx|letter (conflict).docx|letter.docx|mine now.docx", names())
	require.Equal(t, []byte("state A"), copyOf("letter (conflict 2).docx"))
	require.Equal(t, []byte("the last state of all"), copyOf("letter (conflict).docx"))

	// Every numbered name is taken: the copy gets a name with the time in it.
	for n := 3; n <= 20; n++ {
		e.sim.AddFile("", fmt.Sprintf("letter (conflict %d).docx", n), []byte("in the way"))
	}
	read()
	e.sim.SetContent(id, []byte("changed a last time"))
	refused("state Z")
	held := ""
	for _, name := range strings.Split(names(), "|") {
		if string(copyOf(name)) == "state Z" {
			held = name
		}
	}
	require.Regexp(t, `^letter \(conflict \d{4}-\d\d-\d\d \d\d\.\d\d\.\d\d\)\.docx$`, held)
}

// The web app asks how an editor window stands: whether it can still save, and where
// what it wanted to save is kept. The question costs nothing and tells nobody else.
func TestConnectedOfficeState(t *testing.T) {
	e := newConnectedEnv(t)
	e.withOffice(t, false)
	folder := e.sim.AddFolder("", "Docs")
	id := e.sim.AddFile(folder, "letter.docx", []byte("version one"))
	res, err := e.officeSession(t, "/Docs/letter.docx", types.ViewerActionEdit)
	require.NoError(t, err)
	session, token := res.Session.ID, res.Session.AccessToken
	read := func() {
		t.Helper()
		rec, _ := e.office(ConnectedOfficeGet, http.MethodGet, session, token, nil)
		require.Equal(t, http.StatusOK, rec.Code)
	}
	state := func(u *ent.User, id string) (*ConnectedOfficeStateResponse, error) {
		return ConnectedOfficeState(e.as(u), id)
	}
	calls := func() int {
		n := 0
		for _, kind := range []drivetest.Call{drivetest.CallList, drivetest.CallGet, drivetest.CallMedia, drivetest.CallStart, drivetest.CallRange} {
			n += e.sim.Calls(kind)
		}
		return n
	}

	// Before it read anything, and while it can save: no conflict. A refused save of a
	// window that read nothing holds nothing of its user, also when it cannot be kept.
	got, err := state(e.alex, session)
	require.NoError(t, err)
	require.Equal(t, &ConnectedOfficeStateResponse{}, got)
	e.sim.SetFull(true)
	rec, _ := e.office(ConnectedOfficePut, http.MethodPost, session, token, []byte("nothing read"))
	require.Equal(t, http.StatusConflict, rec.Code)
	e.sim.SetFull(false)
	got, err = state(e.alex, session)
	require.NoError(t, err)
	require.Equal(t, &ConnectedOfficeStateResponse{}, got)
	read()
	rec, _ = e.office(ConnectedOfficePut, http.MethodPost, session, token, []byte("version two"))
	require.Equal(t, http.StatusOK, rec.Code)
	got, err = state(e.alex, session)
	require.NoError(t, err)
	require.False(t, got.Conflict)
	require.Nil(t, got.Copy)

	// The file changes elsewhere and a save is refused: the window cannot save any
	// more, and the answer names the copy as a row a client can open.
	e.sim.SetContent(id, []byte("changed at the provider"))
	rec, _ = e.office(ConnectedOfficePut, http.MethodPost, session, token, []byte("what the user wrote"))
	require.Equal(t, http.StatusConflict, rec.Code)
	before := calls()
	got, err = state(e.alex, session)
	require.NoError(t, err)
	require.True(t, got.Conflict)
	require.NotNil(t, got.Copy)
	require.Equal(t, "letter (conflict).docx", got.Copy.Name)
	require.Equal(t, e.root+"/Docs/letter%20(conflict).docx", got.Copy.Path)
	require.Equal(t, int(types.FileTypeFile), got.Copy.Type)
	require.NotEmpty(t, got.Copy.PrimaryEntity)
	require.Equal(t, before, calls(), "no provider is asked")
	// The row is the one a listing of the folder shows.
	e.m.Tree().Forget(e.id)
	list, err := e.list(e.alex, e.root+"/Docs", 100, "")
	require.NoError(t, err)
	for _, f := range list.Files {
		if f.Name == got.Copy.Name {
			require.Equal(t, f.ID, got.Copy.ID)
			require.Equal(t, f.Path, got.Copy.Path)
			require.Equal(t, f.PrimaryEntity, got.Copy.PrimaryEntity)
		}
	}
	require.Equal(t, []byte("what the user wrote"), e.download(got.Copy.Path))

	// A call of the Office server that runs in the session is not waited for.
	s, err := e.m.Office(session, token)
	require.NoError(t, err)
	leave, err := e.m.OfficeEnter(e.ctx, s)
	require.NoError(t, err)
	answered := make(chan error, 1)
	go func() {
		_, err := state(e.alex, session)
		answered <- err
	}()
	select {
	case err := <-answered:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the question waited for the session")
	}
	leave()

	// Somebody else, and a session that is not there, get the same answer: nothing.
	_, err = state(e.sam, session)
	require.Equal(t, serializer.CodeParentNotExist, connectedCode(err))
	_, err = state(e.alex, "no-such-session")
	require.Equal(t, serializer.CodeParentNotExist, connectedCode(err))

	// The window reads the document again: it can save, and nothing is told of a copy.
	read()
	got, err = state(e.alex, session)
	require.NoError(t, err)
	require.Equal(t, &ConnectedOfficeStateResponse{}, got)

	// A save is refused and cannot be kept either (no room): the window cannot save,
	// and there is no copy to name. A later one that is kept names its copy.
	e.sim.SetContent(id, []byte("changed again"))
	e.sim.SetFull(true)
	rec, _ = e.office(ConnectedOfficePut, http.MethodPost, session, token, []byte("more"))
	require.Equal(t, http.StatusConflict, rec.Code)
	got, err = state(e.alex, session)
	require.NoError(t, err)
	require.Equal(t, &ConnectedOfficeStateResponse{Conflict: true}, got)
	e.sim.SetFull(false)
	// Reading again clears it.
	read()
	got, err = state(e.alex, session)
	require.NoError(t, err)
	require.Equal(t, &ConnectedOfficeStateResponse{}, got)
	e.sim.SetContent(id, []byte("changed a third time"))
	e.sim.SetFull(true)
	rec, _ = e.office(ConnectedOfficePut, http.MethodPost, session, token, []byte("more"))
	require.Equal(t, http.StatusConflict, rec.Code)
	e.sim.SetFull(false)
	rec, _ = e.office(ConnectedOfficePut, http.MethodPost, session, token, []byte("more, kept this time"))
	require.Equal(t, http.StatusConflict, rec.Code)
	got, err = state(e.alex, session)
	require.NoError(t, err)
	require.True(t, got.Conflict)
	require.NotNil(t, got.Copy)
	require.Equal(t, []byte("more, kept this time"), e.download(got.Copy.Path))

	// A group that may not use connected storages is told so.
	g, err := e.client.Group.Query().Where(group.ID(2)).First(e.ctx)
	require.NoError(t, err)
	without := make(boolset.BooleanSet, len(*g.Permissions))
	copy(without, *g.Permissions)
	boolset.Set(types.GroupPermissionConnectedStorage, false, &without)
	require.NoError(t, e.client.Group.Update().Where(group.ID(2)).SetPermissions(&without).Exec(e.ctx))
	e.alex = e.reload(e.alex.ID)
	_, err = state(e.alex, session)
	require.Equal(t, serializer.CodeConnectedUnavailable, connectedCode(err))
	require.NoError(t, e.client.Group.Update().Where(group.ID(2)).SetPermissions(g.Permissions).Exec(e.ctx))
	e.alex = e.reload(e.alex.ID)
	got, err = state(e.alex, session)
	require.NoError(t, err)
	require.True(t, got.Conflict)

	// A new password ends the windows from before it, here as for the Office server.
	old := e.alex
	require.NoError(t, e.client.User.UpdateOneID(e.alex.ID).SetPassword("another-hash").Exec(e.ctx))
	e.alex = e.reload(e.alex.ID)
	_, err = state(e.alex, session)
	require.Equal(t, serializer.CodeParentNotExist, connectedCode(err))
	require.NoError(t, e.client.User.UpdateOneID(e.alex.ID).SetPassword(old.Password).Exec(e.ctx))
	e.alex = e.reload(e.alex.ID)
	got, err = state(e.alex, session)
	require.NoError(t, err)
	require.True(t, got.Conflict)

	// A storage that is disconnected takes its windows with it.
	require.NoError(t, e.m.Disconnect(e.ctx, e.alex.ID, e.id))
	_, err = state(e.alex, session)
	require.Equal(t, serializer.CodeParentNotExist, connectedCode(err))
}
