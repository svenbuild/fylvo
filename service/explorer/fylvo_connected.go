package explorer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cloudreve/Cloudreve/v4/application/constants"
	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/boolset"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs/dbfs"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/manager"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/pkg/setting"
	"github.com/gin-gonic/gin"
)

// Fylvo: the file routes for connected storages (see pkg/connected).
//
// A connected storage is served by its own small engine behind the routes and answers of
// the ordinary file API. Its files have no rows in the database, so nothing here may hand
// an address on to the file manager: each route below either answers itself or refuses.

const (
	connectedContentPath = constants.APIPrefix + "/fylvo/connected/content/"
	connectedMaxPageSize = 2000

	// Metadata of rows of a connected storage, for clients.
	connectedMetaExternalURL = "fylvo:external_url"
	connectedMetaExport      = "fylvo:export"
	connectedMetaSizeUnknown = "fylvo:size_unknown"
	connectedMetaUnsupported = "fylvo:unsupported"
	connectedMetaLink        = "fylvo:link"
	connectedMetaViaLink     = "fylvo:via_link"
)

// ConnectedListInfo tells a client what the connected storage it is listing can do, so
// it does not have to guess from capability bits.
type ConnectedListInfo struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	// ReadOnly: nothing can be changed in this folder (it is reached through a link).
	ReadOnly bool `json:"read_only"`
	// Search and Thumbnails tell whether these are available yet.
	Search     bool `json:"search"`
	Thumbnails bool `json:"thumbnails"`
	// Omitted counts entries the provider has but the listing leaves out, because
	// their names cannot be part of an address.
	Omitted int `json:"omitted,omitempty"`
}

// connectedURI parses an address and tells whether it lies in a connected storage.
func connectedURI(raw string) (*fs.URI, bool) {
	uri, err := fs.NewAnyUriFromString(raw)
	if err != nil || uri.FileSystem() != constants.FileSystemConnected {
		return nil, false
	}
	// The parser drops one slash at the end by itself. A second one is an empty name,
	// which no path has: such an address is none of a connected storage.
	if address, _, _ := strings.Cut(raw, "?"); strings.HasSuffix(address, "//") {
		return nil, false
	}
	return uri, true
}

// connectedURIs sorts the addresses of a request: all in the database file system (nil),
// or all in connected storages. A mix of both is refused: no route works across the two.
func connectedURIs(raws []string) ([]*fs.URI, error) {
	var res []*fs.URI
	for _, raw := range raws {
		if uri, ok := connectedURI(raw); ok {
			res = append(res, uri)
		}
	}
	if len(res) != 0 && len(res) != len(raws) {
		return nil, serializer.NewError(serializer.CodeParamErr, "Addresses of a connected storage cannot be mixed with others", nil)
	}
	return res, nil
}

// refuseConnectedSources is for the routes that keep addresses as plain text for later
// (the sources of tasks): an address of a connected storage among them is refused now.
func refuseConnectedSources(raws ...string) error {
	for _, raw := range raws {
		if _, ok := connectedURI(raw); ok {
			return connected.ErrUnsupported
		}
	}
	return nil
}

// connectedRequest is a request admitted to a connection.
type connectedRequest struct {
	m    *connected.Manager
	conn *connected.Conn
	ctx  context.Context
	done func()
	user *ent.User
	// path is the checked path of the address, relative to the root of the connection.
	path string
}

func mayUseConnected(u *ent.User) bool {
	return u != nil && !inventory.IsAnonymousUser(u) && u.Edges.Group != nil && u.Edges.Group.Permissions != nil &&
		u.Edges.Group.Permissions.Enabled(int(types.GroupPermissionConnectedStorage))
}

// openConnected admits the request to the connection its address names. The caller must
// call done. Addresses with search parameters are refused here; listing handles them.
func openConnected(c context.Context, uri *fs.URI) (*connectedRequest, error) {
	dep := dependency.FromContext(c)
	user := inventory.UserFromContext(c)
	if !mayUseConnected(user) {
		return nil, connected.ErrNotAllowed
	}

	path, err := connected.CheckPath(uri.U.Path)
	if err != nil {
		return nil, connected.Error(c, err)
	}
	id := ""
	if uri.U.User != nil {
		id = uri.U.User.Username()
	}

	m := connected.For(dep)
	conn, ctx, done, err := m.Use(c, user.ID, id)
	if err != nil {
		return nil, err
	}
	return &connectedRequest{m: m, conn: conn, ctx: ctx, done: done, user: user, path: path}, nil
}

// connectedRowURI is the address of a row.
func connectedRowURI(connection, path string) string {
	base, err := fs.NewAnyUriFromString(connected.RootURI(connection))
	if err != nil || path == "" {
		return connected.RootURI(connection)
	}
	return base.Join(strings.Split(path, "/")...).String()
}

func connectedCapability(row *connected.Row) *boolset.BooleanSet {
	set := &boolset.BooleanSet{}
	flags := map[dbfs.NavigatorCapability]bool{dbfs.NavigatorCapabilityInfo: true}
	if row.Unsupported == "" {
		if row.IsDir {
			flags[dbfs.NavigatorCapabilityListChildren] = row.Enterable()
			flags[dbfs.NavigatorCapabilityEnterFolder] = row.Enterable()
			// Something new can be put into a folder of the storage itself.
			flags[dbfs.NavigatorCapabilityCreateFile] = row.Enterable() && !row.ViaLink
			// A folder is downloaded by the client, which lists it and fetches its files
			// one by one. Not through a link: links can lead back up.
			flags[dbfs.NavigatorCapabilityDownloadFile] = row.Enterable() && row.Link == nil
		} else {
			flags[dbfs.NavigatorCapabilityDownloadFile] = row.Native == "" || len(connected.ExportFormats[row.Native]) > 0
		}
	}
	if !row.ReadOnly() && row.Path != "" {
		flags[dbfs.NavigatorCapabilityRenameFile] = true
		flags[dbfs.NavigatorCapabilityDeleteFile] = true
	}
	boolset.Sets(flags, set)
	return set
}

// connectedFile builds the answer for one row.
func connectedFile(req *connectedRequest, row *connected.Row, extended bool) *FileResponse {
	res := &FileResponse{
		Type:          int(types.FileTypeFile),
		ID:            connected.FileID(req.conn.ID, row),
		Name:          row.Name,
		CreatedAt:     row.ModTime,
		UpdatedAt:     row.ModTime,
		Size:          row.Size,
		Metadata:      map[string]string{},
		Path:          connectedRowURI(req.conn.ID, row.Path),
		Capability:    connectedCapability(row),
		PrimaryEntity: connected.VersionID(req.conn.ID, row),
		// The storage is the account's own. What can be done with a row is in its capability.
		Owned: true,
	}
	if row.IsDir {
		res.Type = int(types.FileTypeFolder)
	} else {
		// No thumbnails yet: clients do not ask for one when this is set.
		res.Metadata[dbfs.ThumbDisabledKey] = ""
	}
	if row.Path == "" {
		res.Name = req.conn.Name
	}

	switch {
	case row.Unsupported != "":
		res.Metadata[connectedMetaUnsupported] = row.Unsupported
	case row.Native != "":
		// It opens at the provider. Where it can be exported, a download gives that;
		// a form or a notebook has no export, and nothing to download.
		res.Metadata[connectedMetaExternalURL] = row.WebURL
		res.Metadata[connectedMetaSizeUnknown] = "1"
		if formats := connected.ExportFormats[row.Native]; len(formats) > 0 {
			res.Metadata[connectedMetaExport] = formats[0]
		}
	}
	// A link itself, and what was reached through one further up: a client that walks
	// folders by itself stops at links (they can lead back up) and goes on below them.
	if row.ViaLink && row.Link == nil {
		res.Metadata[connectedMetaViaLink] = "1"
	}
	if row.Link != nil {
		res.Metadata[connectedMetaLink] = "1"
		if row.Link != nil && !row.Enterable() && row.IsDir {
			// A folder in another drive: it opens at the provider.
			res.Metadata[connectedMetaExternalURL] = row.WebURL
		}
		if row.Link != nil && !row.IsDir && row.Version == "" {
			res.Metadata[connectedMetaSizeUnknown] = "1"
		}
	}

	if extended {
		res.ExtendedInfo = &ExtendedInfo{
			StorageUsed:   row.Size,
			StoragePolicy: &StoragePolicy{ID: "connected", Name: req.conn.Name, Type: types.PolicyTypeLocal},
		}
	}
	return res
}

// connectedList answers GET /file for a folder of a connected storage.
func connectedList(c *gin.Context, uri *fs.URI, s *ListFileService) (*ListResponse, error) {
	if len(uri.U.Query()) > 0 {
		// Search and recursive listing are not there yet.
		return nil, connected.ErrUnsupported
	}

	req, err := openConnected(c, uri)
	if err != nil {
		return nil, err
	}
	defer req.done()

	// What the request leaves out comes from the view stored for the storage, as for
	// folders of Fylvo. Later pages are asked for the same way, so they agree.
	view, err := req.m.View(c, req.user.ID, req.conn.ID)
	if err != nil {
		// Not answered in another order than the pages before: that would end the listing.
		return nil, connected.Error(c, err)
	}
	by, direction, size := s.OrderBy, s.OrderDirection, s.PageSize
	if view != nil {
		if by == "" {
			by = view.Order
		}
		if direction == "" {
			direction = view.OrderDirection
		}
		if size <= 0 {
			size = view.PageSize
		}
	}
	order := connected.Order{By: by, Desc: direction == "desc"}
	switch order.By {
	case "name", "size", "updated_at":
	default:
		order.By = "name"
	}
	if size <= 0 {
		size = 100
	}
	size = min(size, connectedMaxPageSize)

	page, err := req.m.Tree().List(req.ctx, req.conn, req.path, order, size, s.NextPageToken)
	if err != nil {
		return nil, connected.Error(c, err)
	}

	parent := page.Parent
	if parent == nil {
		// A further page: the folder was checked when the listing started.
		parent = &connected.Row{Item: provider.Item{IsDir: true}, Path: req.path}
		if req.path != "" {
			_, parent.Name = connected.ParentOf(req.path)
		}
	}

	res := &ListResponse{
		Files: make([]FileResponse, 0, len(page.Rows)),
		Pagination: &inventory.PaginationResults{
			PageSize:      size,
			TotalItems:    page.Total,
			NextPageToken: page.Next,
			IsCursor:      true,
		},
		Props: &fs.NavigatorProps{
			Capability:            connectedCapability(page.Parent),
			MaxPageSize:           connectedMaxPageSize,
			OrderByOptions:        []string{"name", "size", "updated_at"},
			OrderDirectionOptions: []string{"asc", "desc"},
		},
		Connected: &ConnectedListInfo{
			ID:       req.conn.ID,
			Provider: req.conn.Provider,
			ReadOnly: page.Parent.ViaLink,
			Omitted:  page.Omitted,
		},
	}
	for _, row := range page.Rows {
		res.Files = append(res.Files, *connectedFile(req, row, false))
	}
	if page.Parent != nil {
		res.Parent = *connectedFile(req, parent, false)
	} else {
		res.Parent = FileResponse{
			Type: int(types.FileTypeFolder), Name: parent.Name, Path: connectedRowURI(req.conn.ID, req.path),
			Capability: res.Props.Capability,
		}
	}
	res.View = view
	return res, nil
}

// connectedInfo answers GET /file/info for an entry of a connected storage.
func connectedInfo(c *gin.Context, uri *fs.URI, extended bool) (*FileResponse, error) {
	req, err := openConnected(c, uri)
	if err != nil {
		return nil, err
	}
	defer req.done()

	row, err := req.m.Tree().Resolve(req.ctx, req.conn, req.path)
	if err != nil {
		return nil, connected.Error(c, err)
	}
	return connectedFile(req, row, extended), nil
}

// connectedURLs answers POST /file/url: one address per file, each carrying a sealed
// grant for exactly that file in the state it has now.
func connectedURLs(c *gin.Context, uris []*fs.URI, s *FileURLService) (*FileURLResponse, error) {
	if s.Archive {
		return nil, connected.ErrUnsupported
	}

	dep := dependency.FromContext(c)
	expire := time.Now().Add(dep.SettingProvider().EntityUrlValidDuration(c))
	base := dep.SettingProvider().SiteURL(c)
	if s.UsePrimarySiteURL {
		base = dep.SettingProvider().SiteURL(setting.UseFirstSiteUrl(c))
	}

	res := make([]manager.EntityUrl, len(uris))
	ae := serializer.NewAggregateError()
	for i, uri := range uris {
		address, err := connectedURL(c, uri, s, base, expire)
		if err != nil {
			ae.Add(uri.String(), connected.Public(c, err))
			continue
		}
		res[i] = manager.EntityUrl{Url: address}
	}
	if err := ae.Aggregate(); err != nil && !s.SkipError {
		return nil, err
	}

	if s.Redirect && len(uris) == 1 {
		c.Redirect(http.StatusFound, res[0].Url)
		return nil, nil
	}
	return &FileURLResponse{Urls: res, Expires: &expire}, nil
}

func connectedURL(c *gin.Context, uri *fs.URI, s *FileURLService, base *url.URL, expire time.Time) (string, error) {
	req, err := openConnected(c, uri)
	if err != nil {
		return "", err
	}
	defer req.done()

	row, err := req.m.Tree().Resolve(req.ctx, req.conn, req.path)
	if err != nil {
		return "", connected.Error(c, err)
	}
	if row.IsDir || row.Unsupported != "" {
		return "", fs.ErrEntityNotExist
	}
	if row.Native != "" && len(connected.ExportFormats[row.Native]) == 0 {
		// A form, a notebook: it has no bytes and no export. It opens at the provider.
		return "", connected.ErrUnsupported
	}
	// The state as it is now, not as a listing of some seconds ago told it.
	if err := connected.Fresh(req.ctx, req.conn.P, row); err != nil {
		return "", connected.Error(c, err)
	}
	if row.Version == "" {
		// Without a marker for the state of the content an address could go on to
		// deliver something else than it was made for.
		return "", fs.ErrEntityNotExist
	}
	// The client names the version it means. It gets that one or nothing.
	if s.Entity != "" && s.Entity != connected.VersionID(req.conn.ID, row) {
		return "", fs.ErrEntityNotExist
	}

	grant := &connected.Grant{
		Kind:        connected.GrantContent,
		User:        req.user.ID,
		Fingerprint: inventory.PasswordFingerprint(req.user),
		Conn:        req.conn.ID,
		Generation:  req.conn.Generation,
		Item:        connected.GrantItemOf(row),
		Download:    s.Download,
		Expires:     expire.Unix(),
	}
	name := row.Name
	if row.Native != "" {
		grant.Export = connected.ExportFormats[row.Native][0]
		name = connected.ExportName(row.Name, row.Native, grant.Export)
	}

	sealed, err := req.m.SealGrant(c, grant)
	if err != nil {
		return "", serializer.NewError(serializer.CodeInternalSetting, "Failed to create the download address", err)
	}
	// The name at the end is for the browser only; the grant says what is read.
	return base.ResolveReference(&url.URL{Path: connectedContentPath + sealed + "/" + name}).String(), nil
}

// connectedPatchView stores the view setting of a connected storage. There is one per
// connection, kept with the connection.
func connectedPatchView(c *gin.Context, uri *fs.URI, view *types.ExplorerView) error {
	req, err := openConnected(c, uri)
	if err != nil {
		return err
	}
	defer req.done()
	return req.m.SetView(c, req.user.ID, req.conn.ID, req.conn.Generation, view)
}

// ConnectedCapacityResponse is the fill level of a connected storage. Total is 0 when the
// provider does not tell.
type ConnectedCapacityResponse struct {
	Used  int64 `json:"used"`
	Total int64 `json:"total"`
}

// ConnectedCapacity answers GET /fylvo/connected/:id/capacity.
func ConnectedCapacity(c *gin.Context, id string) (*ConnectedCapacityResponse, error) {
	uri, err := fs.NewAnyUriFromString(connected.RootURI(id))
	if err != nil {
		return nil, connected.ErrNoStorage
	}
	req, err := openConnected(c, uri)
	if err != nil {
		return nil, err
	}
	defer req.done()

	usage, err := req.m.Capacity(req.ctx, req.conn)
	if err != nil {
		return nil, connected.Error(c, err)
	}
	return &ConnectedCapacityResponse{Used: usage.Used, Total: usage.Total}, nil
}

// ServeConnectedContent answers GET and HEAD /fylvo/connected/content/:grant/:name.
//
// The request carries no sign-in. The grant in its path is the permission, and it is
// checked in full every time: the account it was made for, that account's password and
// group, the connection and its token, and the state of the file. The name in the path is
// ignored. Errors are HTTP status codes, since a player or a download would store an
// error answer as the file.
func ServeConnectedContent(c *gin.Context) {
	dep := dependency.FromContext(c)
	m := connected.For(dep)
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")

	grant, err := m.OpenGrant(c, c.Param("grant"))
	if err != nil || grant.Kind != connected.GrantContent {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	user, err := dep.UserClient().GetLoginUserByID(c, grant.User)
	if err != nil || !inventory.SessionStillValid(user, grant.Fingerprint) || !mayUseConnected(user) {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	conn, ctx, done, err := m.Use(c, user.ID, grant.Conn)
	if err != nil || conn.Generation != grant.Generation {
		if err == nil {
			done()
		}
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	defer done()

	// A download waits for its place before the file is looked at, so the look is not
	// minutes old when the bytes are read. HEAD reads no bytes and needs no place.
	if c.Request.Method != http.MethodHead {
		release, err := connected.AcquireStream(ctx)
		if err != nil {
			c.Header("Retry-After", "5")
			c.AbortWithStatus(http.StatusServiceUnavailable)
			return
		}
		defer release()
	}

	item := grant.Item.ProviderItem()
	conn.P.Known(item)
	// The file as it is now. A grant is for one state of one item: when the content
	// changed since, the client has to ask for a new address. For a link that is the
	// state of what it points to, and it must still point there.
	current, err := conn.P.Get(ctx, item.ID)
	if err == nil && item.Link != nil {
		if current.Link == nil || *current.Link != *item.Link {
			c.AbortWithStatus(http.StatusConflict)
			return
		}
		current, err = conn.P.Target(ctx, current)
	}
	if err != nil {
		connectedContentError(c, err)
		return
	}
	if item.Version == "" || current.Version != item.Version {
		c.AbortWithStatus(http.StatusConflict)
		return
	}

	name := item.Name
	if item.Native != "" {
		name = connected.ExportName(item.Name, item.Native, grant.Export)
	}
	disposition := "inline"
	if grant.Download {
		disposition = "attachment"
	}
	c.Header("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": name}))
	c.Header("Content-Type", dep.MimeDetector(c).TypeByName(name))
	if item.Native == "" {
		c.Header("Accept-Ranges", "bytes")
	}
	if c.Request.Method == http.MethodHead {
		// Answered from what is known of the file: no download is started for it.
		if item.Native == "" {
			c.Header("Content-Length", fmt.Sprint(current.Size))
		}
		c.Status(http.StatusOK)
		c.Writer.WriteHeaderNow()
		return
	}

	// The transfer ends when either side stands still for too long, so a place is
	// never held by a download that no longer moves. The watch runs from here on: it
	// also covers a provider that stalls before the first byte.
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	watch := time.AfterFunc(connectedIdle, stop)
	defer watch.Stop()
	var content *provider.Content
	if item.Native != "" {
		content, err = conn.P.Export(ctx, item, grant.Export)
	} else {
		content, err = conn.P.Open(ctx, item, singleRange(c.GetHeader("Range")))
	}
	if err != nil {
		connectedContentError(c, err)
		return
	}
	defer content.Body.Close()

	if content.Length >= 0 {
		c.Header("Content-Length", fmt.Sprint(content.Length))
	}
	status := http.StatusOK
	if content.Partial {
		status = http.StatusPartialContent
		c.Header("Content-Range", content.ContentRange)
	}
	c.Status(status)
	c.Writer.WriteHeaderNow()
	if err := copyMoving(c.Writer, content.Body, watch); err != nil {
		// The provider's stream broke off or stood still, or the client went away. An
		// answer without a length would otherwise end like a complete one, and the
		// client would keep a cut-off file for the whole.
		panic(http.ErrAbortHandler)
	}
}

// connectedIdle is how long a transfer may stand still, on either side, before it ends.
var connectedIdle = 2 * time.Minute

// copyMoving copies src to the client as long as both sides keep moving. watch ends the
// read from the provider when it runs out; it is wound up before each read and stopped
// after it, so it measures the provider alone. A client that takes no bytes for
// connectedIdle ends the copy through the deadline of each write.
func copyMoving(w gin.ResponseWriter, src io.Reader, watch *time.Timer) error {
	out := http.NewResponseController(w)
	buf := make([]byte, 64*1024)
	for {
		watch.Reset(connectedIdle)
		n, err := src.Read(buf)
		watch.Stop()
		if n > 0 {
			// Not every writer can take a deadline (a recorder in a test cannot).
			_ = out.SetWriteDeadline(time.Now().Add(connectedIdle))
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// singleRange returns a Range header that asks for one range of bytes, and nothing for
// any other: the whole file is then the answer, which every client can handle.
func singleRange(header string) string {
	spec, ok := strings.CutPrefix(header, "bytes=")
	if !ok || strings.Contains(spec, ",") {
		return ""
	}
	return header
}

func connectedContentError(c *gin.Context, err error) {
	var failure *provider.Error
	switch {
	case errors.Is(err, context.Canceled):
		c.AbortWithStatus(499)
	case errors.Is(err, context.DeadlineExceeded):
		// The provider took too long to answer: come back later, as for "busy".
		c.Header("Retry-After", "5")
		c.AbortWithStatus(http.StatusServiceUnavailable)
	case errors.As(err, &failure) && failure.Status == http.StatusRequestedRangeNotSatisfiable:
		// A range past the end of the file, as a player may ask for.
		c.AbortWithStatus(http.StatusRequestedRangeNotSatisfiable)
	case provider.KindOf(err) == provider.KindNotFound:
		c.AbortWithStatus(http.StatusNotFound)
	case provider.KindOf(err) == provider.KindStale:
		c.AbortWithStatus(http.StatusConflict)
	case provider.KindOf(err) == provider.KindBusy, provider.KindOf(err) == provider.KindIncomplete:
		c.Header("Retry-After", "5")
		c.AbortWithStatus(http.StatusServiceUnavailable)
	case provider.KindOf(err) == provider.KindForbidden, provider.KindOf(err) == provider.KindFlagged,
		provider.KindOf(err) == provider.KindExportTooLarge, provider.KindOf(err) == provider.KindDailyLimit:
		c.AbortWithStatus(http.StatusForbidden)
	case provider.KindOf(err) == provider.KindReconnect, provider.KindOf(err) == provider.KindSetup:
		c.AbortWithStatus(http.StatusNotFound)
	default:
		c.AbortWithStatus(http.StatusBadGateway)
	}
}
