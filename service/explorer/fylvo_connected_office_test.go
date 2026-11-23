package explorer

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/ent/group"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/boolset"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/pkg/setting"
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

// office makes a call of the Office server: no sign-in, only the token.
func (e *connectedEnv) office(handler gin.HandlerFunc, method, session, token string, body []byte) (rec *httptest.ResponseRecorder, cut bool) {
	c, rec := e.request(nil, method, "/api/v4/fylvo/connected/wopi/"+session+"?access_token="+url.QueryEscape(token))
	c.Params = gin.Params{{Key: "session", Value: session}}
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
// With nothing at the address the save is "not found".
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
	require.Equal(t, http.StatusNotFound, rec.Code)

	e.sim.AddFile("", "letter.docx", []byte("another letter"))
	rec, _ = e.office(ConnectedOfficePut, http.MethodPost, session, token, []byte("an edit"))
	require.Equal(t, http.StatusConflict, rec.Code)
	var conflict map[string]int
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &conflict))
	require.Equal(t, 1010, conflict["COOLStatusCode"])
	require.Equal(t, []byte("another letter"), e.download(e.root+"/letter.docx"))
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
	rec, _ = e.office(ConnectedOfficePut, http.MethodPost, session, token, []byte("an edit"))
	require.Equal(t, http.StatusNotFound, rec.Code)
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
