package explorer

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/cloudreve/Cloudreve/v4/application/constants"
	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/cluster/routes"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs/dbfs"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/manager"
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/pkg/setting"
	"github.com/cloudreve/Cloudreve/v4/pkg/util"
	"github.com/cloudreve/Cloudreve/v4/pkg/wopi"
	"github.com/gin-gonic/gin"
)

// Fylvo: files of a connected storage in the Office server. The sessions and what they
// guarantee are in pkg/connected/office.go; this is their side towards the browser (one
// call that opens a session) and towards the Office server (the three calls it makes).

// sessionExpiresPadding is taken off the lifetime a client is told, as for other sessions.
const connectedSessionPadding = 10 * time.Second

// connectedWopiSrc is the address the Office server reaches a session under.
func connectedWopiSrc(base *url.URL, session string) string {
	route, _ := url.Parse(constants.APIPrefix + "/fylvo/connected/wopi/" + session)
	return base.ResolveReference(route).String()
}

// connectedViewerSession answers PUT /file/viewerSession for a file of a connected
// storage. Only the Office server gets a session: every other viewer fetches the content
// by its address (POST /file/url).
func connectedViewerSession(c *gin.Context, uri *fs.URI, s *CreateViewerSessionService, viewer *types.Viewer) (*ViewerSessionResponse, error) {
	if viewer.Type != types.ViewerTypeWopi || s.Version != "" || len(uri.U.Query()) > 0 {
		return nil, connected.ErrUnsupported
	}
	req, err := openConnected(c, uri)
	if err != nil {
		return nil, err
	}
	defer req.done()
	// What the Office server does with this kind of file is settled before a session
	// is opened: opening one can make another give way, which a request that cannot
	// succeed must not do.
	ext := util.Ext(req.path)
	wantsEdit := s.PreferredAction == types.ViewerActionEdit && officeEdits(viewer, ext)
	if !wantsEdit && !officeAction(viewer, ext, types.ViewerActionView) {
		// "Not supported" is the answer for a file that is there. One that is not is
		// "not found", and a provider that cannot be asked says so itself.
		if _, err := req.m.Tree().ResolveNow(req.ctx, req.conn, req.path); err != nil && !errors.Is(err, connected.ErrAmbiguous) {
			return nil, connected.Public(c, err)
		}
		return nil, connected.ErrUnsupported
	}

	settings := dependency.FromContext(c).SettingProvider()
	ttl := time.Duration(settings.ViewerSessionTTL(c)) * time.Second
	session, err := req.m.OpenOffice(req.ctx, req.conn, connected.OfficeRequest{
		Path:     req.path,
		ViewerID: viewer.ID,
		// Editing only where the Office server edits this kind of file.
		Edit:        wantsEdit,
		Fingerprint: inventory.PasswordFingerprint(req.user),
		TTL:         ttl,
		MaxSize:     settings.MaxOnlineEditSize(c),
	})
	if err != nil {
		return nil, connected.Public(c, err)
	}

	// A session that may only read asks for the viewing mode of the Office server.
	action := types.ViewerAction(types.ViewerActionView)
	if session.Edit {
		action = types.ViewerActionEdit
	}
	base := settings.SiteURL(setting.UseFirstSiteUrl(c))
	src, err := wopi.ActionURL(action, viewer, util.Ext(session.Name), connectedWopiSrc(base, session.ID))
	if err != nil {
		req.m.CloseOffice(session)
		if errors.Is(err, wopi.ErrActionNotSupported) {
			return nil, connected.ErrUnsupported
		}
		return nil, serializer.NewError(serializer.CodeInternalSetting, "failed to generate wopi src", err)
	}
	return &ViewerSessionResponse{
		Session: &manager.ViewerSession{
			ID:          session.ID,
			AccessToken: session.Token,
			Expires:     session.Expires.Add(-connectedSessionPadding).UnixMilli(),
		},
		WopiSrc: src.String(),
	}, nil
}

// connectedOffice is a call of the Office server admitted to its session.
type connectedOffice struct {
	req     *connectedRequest
	session *connected.OfficeSession
	// done ends the call: it leaves the session and the connection.
	done func()
}

// openConnectedOffice admits a call of the Office server. The token in the query is the
// permission and is checked in full every time: the session, the account it was opened
// for and that account's password and group, the editor, and the connection as it was
// connected when the session was opened. The call first waits for its turn in the
// session and is checked after that, so a call that waited behind another one is held to
// what is allowed when it runs. It answers the call itself when it refuses it.
func openConnectedOffice(c *gin.Context) (*connectedOffice, bool) {
	dep := dependency.FromContext(c)
	m := connected.For(dep)
	refuse := func(status int, reason string) (*connectedOffice, bool) {
		c.Header(wopi.ServerErrorHeader, reason)
		c.AbortWithStatus(status)
		return nil, false
	}
	c.Header("Cache-Control", "private, no-store")

	session, err := m.Office(c.Param("session"), c.Query(wopi.AccessTokenQuery))
	if err != nil {
		return refuse(http.StatusUnauthorized, "invalid access token")
	}
	leave, err := m.OfficeEnter(c, session)
	if err != nil {
		if errors.Is(err, connected.ErrOfficeGone) {
			return refuse(http.StatusUnauthorized, "session ended")
		}
		// The client went away while its call waited.
		return refuse(http.StatusServiceUnavailable, "busy")
	}
	user, err := dep.UserClient().GetLoginUserByID(c, session.User)
	if err != nil || !inventory.SessionStillValid(user, session.Fingerprint) || !mayUseConnected(user) {
		leave()
		return refuse(http.StatusUnauthorized, "session ended")
	}
	if !viewerEnabled(dep.SettingProvider().FileViewers(c), session.ViewerID) {
		leave()
		return refuse(http.StatusUnauthorized, "viewer not found")
	}
	conn, ctx, done, err := m.Use(c, user.ID, session.Conn)
	if err != nil || conn.Generation != session.Generation {
		if err == nil {
			done()
		}
		leave()
		return refuse(http.StatusUnauthorized, "storage not connected")
	}
	return &connectedOffice{
		req:     &connectedRequest{m: m, conn: conn, ctx: ctx, done: done, user: user, path: session.Path},
		session: session,
		done:    func() { done(); leave() },
	}, true
}

// refuseOfficeBody answers a call that must not have a body and has one. The connection
// is closed with the answer, so nobody waits for a body that does not come: the server
// would otherwise read it before it answers, holding a place meanwhile.
func refuseOfficeBody(c *gin.Context) bool {
	if c.Request.ContentLength == 0 {
		return false
	}
	c.AbortWithStatus(http.StatusBadRequest)
	return true
}

// closeAfterOfficeBody makes the connection of a call that must not have a body and has
// one close with the answer, whatever the answer is. To be called before anything else.
func closeAfterOfficeBody(c *gin.Context) {
	if c.Request.ContentLength != 0 {
		c.Header("Connection", "close")
	}
}

// officeAction tells whether the Office server can be sent to a file of the given kind
// for an action: the address that comes out for it (also by the fallback from one action
// to another) has to be a full web address. A setting like "editor/open" would lead to
// this server instead of the Office server.
func officeAction(viewer *types.Viewer, ext string, action types.ViewerAction) bool {
	src, err := wopi.ActionURL(action, viewer, ext, connectedWopiSrc(&url.URL{Scheme: "https", Host: "placeholder.invalid"}, "session"))
	return err == nil && (src.Scheme == "https" || src.Scheme == "http") && src.Hostname() != ""
}

// officeEdits tells whether the Office server edits files of the given kind: it has an
// action of its own for editing them, and that action is usable.
func officeEdits(viewer *types.Viewer, ext string) bool {
	return viewer.WopiActions[ext][types.ViewerActionEdit] != "" && officeAction(viewer, ext, types.ViewerActionEdit)
}

// officeViewerEdits tells whether the editor of a session still edits this kind of file.
func officeViewerEdits(groups []types.ViewerGroup, id, name string) bool {
	for _, group := range groups {
		for _, viewer := range group.Viewers {
			if viewer.ID == id && !viewer.Disabled && viewer.Type == types.ViewerTypeWopi {
				return officeEdits(&viewer, util.Ext(name))
			}
		}
	}
	return false
}

// viewerEnabled tells whether a viewer is still there and switched on.
func viewerEnabled(groups []types.ViewerGroup, id string) bool {
	for _, group := range groups {
		for _, viewer := range group.Viewers {
			if viewer.ID == id && !viewer.Disabled && viewer.Type == types.ViewerTypeWopi {
				return true
			}
		}
	}
	return false
}

// connectedOfficeError answers a call of the Office server with an error. The Office
// server gets a status and a short text of Fylvo's own, never one of the provider.
func connectedOfficeError(c *gin.Context, err error) {
	if errors.Is(err, connected.ErrOfficeGone) {
		c.Header(wopi.ServerErrorHeader, "session ended")
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	err = connected.Public(c, err)
	status, text := http.StatusInternalServerError, "failed"
	var app serializer.AppError
	if errors.As(err, &app) {
		text = app.Msg
		switch app.Code {
		case serializer.CodeParentNotExist, serializer.CodeNotFound, serializer.CodeUploadTargetGone:
			status = http.StatusNotFound
		case serializer.CodeConnectedReconnect, serializer.CodeConnectedUnavailable:
			status = http.StatusUnauthorized
		case serializer.CodeFileTooLarge:
			status = http.StatusRequestEntityTooLarge
		case serializer.CodeConnectedBusy:
			status = http.StatusServiceUnavailable
			c.Header("Retry-After", "5")
		case serializer.CodeConnectedUnsupported:
			status = http.StatusNotImplemented
		}
	}
	c.Header(wopi.ServerErrorHeader, text)
	c.AbortWithStatus(status)
}

// officeTime writes a time the way the Office protocol does.
func officeTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.0000000Z")
}

// ConnectedOfficeInfo answers CheckFileInfo: GET /fylvo/connected/wopi/:session.
func ConnectedOfficeInfo(c *gin.Context) {
	closeAfterOfficeBody(c)
	o, ok := openConnectedOffice(c)
	if !ok {
		return
	}
	defer o.done()
	if refuseOfficeBody(c) {
		return
	}

	item, edit, err := o.req.m.OfficeInfo(o.req.ctx, o.req.conn, o.session)
	if err != nil {
		connectedOfficeError(c, err)
		return
	}
	dep := dependency.FromContext(c)
	settings := dep.SettingProvider()
	siteURL := settings.SiteURL(c)
	user := hashid.EncodeUserID(dep.HashIDEncoder(), o.req.user.ID)
	// What the session may do right now: also the editor must still edit this kind of file.
	edit = edit && officeViewerEdits(settings.FileViewers(c), o.session.ViewerID, item.Name)
	folder, _ := connected.ParentOf(o.session.Path)
	folderName := "/"
	if folder != "" {
		_, folderName = connected.ParentOf(folder)
	}
	c.JSON(http.StatusOK, &WopiFileInfo{
		BaseFileName:         item.Name,
		Version:              connected.VersionID(o.req.conn.ID, &connected.Row{Item: *item}),
		Size:                 item.Size,
		BreadcrumbBrandName:  settings.SiteBasic(c).Name,
		BreadcrumbBrandUrl:   siteURL.String(),
		BreadcrumbFolderName: folderName,
		BreadcrumbFolderUrl:  routes.FrontendHomeUrl(siteURL, connectedRowURI(o.req.conn.ID, folder)).String(),
		ClosePostMessage:     true,
		PostMessageOrigin:    "*",
		FileNameMaxLength:    dbfs.MaxFileNameLength,
		LastModifiedTime:     officeTime(item.ModTime),
		UserFriendlyName:     o.req.user.Nick,
		UserId:               user,
		OwnerId:              user,
		ReadOnly:             !edit,
		UserCanReview:        edit,
		UserCanWrite:         edit,
		// No copy beside the file, no rename and no locks: a save goes to the file the
		// session was opened for, and only over the version the editor last read.
		UserCanNotWriteRelative: true,
		SupportsReviewing:       true,
		SupportsUpdate:          true,
	})
}

// movingWriter writes to a client that has to keep taking bytes, and has to be through
// by end.
type movingWriter struct {
	w       io.Writer
	control *http.ResponseController
	end     time.Time
}

// deadline is how long the next write may take: a while, and never past the end.
func (m *movingWriter) deadline() time.Time {
	deadline := time.Now().Add(connectedIdle)
	if m.end.Before(deadline) {
		deadline = m.end
	}
	return deadline
}

func (m *movingWriter) Write(p []byte) (int, error) {
	// For this write only: between writes the provider is read, which has a watch of
	// its own, and must not run into a deadline left over from the last write.
	_ = m.control.SetWriteDeadline(m.deadline())
	defer func() { _ = m.control.SetWriteDeadline(time.Time{}) }()
	return m.w.Write(p)
}

// Flush sends what was written on its way to the client, and tells when that failed.
func (m *movingWriter) Flush() error {
	_ = m.control.SetWriteDeadline(m.deadline())
	defer func() { _ = m.control.SetWriteDeadline(time.Time{}) }()
	return m.control.Flush()
}

// rawWriter is the writer of the server below the one of the web framework. Its flush
// tells an error; the framework's does not.
func rawWriter(c *gin.Context) http.ResponseWriter {
	if wrapped, ok := c.Writer.(interface{ Unwrap() http.ResponseWriter }); ok {
		return wrapped.Unwrap()
	}
	return c.Writer
}

// ConnectedOfficeGet answers GetFile: GET /fylvo/connected/wopi/:session/contents.
func ConnectedOfficeGet(c *gin.Context) {
	closeAfterOfficeBody(c)
	o, ok := openConnectedOffice(c)
	if !ok {
		return
	}
	defer o.done()
	if refuseOfficeBody(c) {
		return
	}

	begun := false
	control := http.NewResponseController(rawWriter(c))
	defer func() { _ = control.SetWriteDeadline(time.Time{}) }()
	limit := dependency.FromContext(c).SettingProvider().MaxOnlineEditSize(c)
	err := o.req.m.OfficeRead(o.req.ctx, o.req.conn, o.session, limit, func(item *provider.Item) {
		c.Header("Content-Type", "application/octet-stream")
		c.Header("Content-Length", fmt.Sprint(item.Size))
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header(wopi.ItemVersionHeader, connected.VersionID(o.req.conn.ID, &connected.Row{Item: *item}))
		c.Status(http.StatusOK)
		c.Writer.WriteHeaderNow()
		begun = true
	}, &movingWriter{w: c.Writer, control: control, end: time.Now().Add(connected.OfficeReadTime)})
	switch {
	case err == nil:
	case begun:
		// A part of the document is out. The answer is cut off, so the Office server
		// does not take the part for the whole.
		panic(http.ErrAbortHandler)
	default:
		connectedOfficeError(c, err)
	}
}

// ConnectedOfficePut answers PutFile: POST /fylvo/connected/wopi/:session/contents.
func ConnectedOfficePut(c *gin.Context) {
	// Whoever throws away what is left of the body after an error does not wait for a
	// client that stands still. Set before anything can refuse the call.
	defer func() { _ = http.NewResponseController(c.Writer).SetReadDeadline(time.Now().Add(connectedIdle)) }()
	o, ok := openConnectedOffice(c)
	if !ok {
		return
	}
	defer o.done()

	// What the session may do right now, by the file as it is and the editor as it is
	// set up. An item that is gone is not decided here: what sits at its address now
	// (something else: a conflict; nothing: gone) is for the save to find, under the
	// lock for changes.
	item, edit, err := o.req.m.OfficeInfo(o.req.ctx, o.req.conn, o.session)
	var gone serializer.AppError
	missing := errors.As(err, &gone) && gone.Code == serializer.CodeParentNotExist
	if err != nil && !missing {
		connectedOfficeError(c, err)
		return
	}
	// A file that has another name by now is for the save as well: it finds the address
	// empty (gone) or taken by something else (a conflict), and stores nothing.
	atItsName := !missing && item.Name == o.session.Name
	viewers := dependency.FromContext(c).SettingProvider().FileViewers(c)
	allowed := o.session.Edit && connected.Writes() && officeViewerEdits(viewers, o.session.ViewerID, o.session.Name)
	if !allowed || (atItsName && !edit) {
		c.Header(wopi.ServerErrorHeader, "read-only access")
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	size := c.Request.ContentLength
	if size < 0 {
		c.AbortWithStatus(http.StatusLengthRequired)
		return
	}
	if size > dependency.FromContext(c).SettingProvider().MaxOnlineEditSize(c) {
		connectedOfficeError(c, connected.ErrOfficeTooLarge)
		return
	}

	row, err := o.req.m.OfficeSave(o.req.ctx, o.req.conn, o.session, movingBody(c, c.Request.Body), size)
	var app serializer.AppError
	if errors.As(err, &app) && app.Code == serializer.CodeStaleVersion {
		// The file is no longer the version this editor read. The body is what the
		// Office server understands as "the document changed in storage": it asks the
		// user instead of trying again.
		c.Header(wopi.ServerErrorHeader, "document changed in storage")
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{"COOLStatusCode": 1010, "LOOLStatusCode": 1010})
		return
	}
	if err != nil {
		connectedOfficeError(c, err)
		return
	}
	c.Header(wopi.ItemVersionHeader, connected.VersionID(o.req.conn.ID, row))
	c.JSON(http.StatusOK, gin.H{"LastModifiedTime": officeTime(row.ModTime)})
}

// ConnectedOfficeOther answers every other call (locks, rename, a copy beside the file):
// none of them is offered for a connected storage.
func ConnectedOfficeOther(c *gin.Context) {
	// Such a call can carry a body (a copy beside the file does). It is not read: the
	// connection closes with the answer.
	c.Header("Connection", "close")
	o, ok := openConnectedOffice(c)
	if !ok {
		return
	}
	defer o.done()
	c.Header(wopi.ServerErrorHeader, "not supported")
	c.AbortWithStatus(http.StatusNotImplemented)
}
