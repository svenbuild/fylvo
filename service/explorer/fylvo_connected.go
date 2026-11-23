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
)

// ConnectedListInfo tells a client what the connected storage it is listing can do, so
// it does not have to guess from capability bits.
type ConnectedListInfo struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	// ReadOnly: nothing can be changed in this folder.
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
		return nil, connected.Error(err)
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
		} else {
			flags[dbfs.NavigatorCapabilityDownloadFile] = true
		}
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
		res.Metadata[connectedMetaExternalURL] = row.WebURL
		res.Metadata[connectedMetaExport] = connected.ExportFormats[row.Native][0]
		res.Metadata[connectedMetaSizeUnknown] = "1"
	}
	if row.ViaLink {
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

	order := connected.Order{By: s.OrderBy, Desc: s.OrderDirection == "desc"}
	switch order.By {
	case "", "name", "size", "updated_at":
	default:
		order.By = "name"
	}
	size := s.PageSize
	if size <= 0 {
		size = 100
	}
	size = min(size, connectedMaxPageSize)

	page, err := req.m.Tree().List(req.ctx, req.conn, req.path, order, size, s.NextPageToken)
	if err != nil {
		return nil, connected.Error(err)
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
			Capability:            connectedCapability(&connected.Row{Item: provider.Item{IsDir: true}}),
			MaxPageSize:           connectedMaxPageSize,
			OrderByOptions:        []string{"name", "size", "updated_at"},
			OrderDirectionOptions: []string{"asc", "desc"},
		},
		Connected: &ConnectedListInfo{
			ID:       req.conn.ID,
			Provider: req.conn.Provider,
			ReadOnly: true,
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
	if view, err := req.m.View(c, req.user.ID, req.conn.ID); err == nil {
		res.View = view
	}
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
		return nil, connected.Error(err)
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
			ae.Add(uri.String(), err)
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
		return "", connected.Error(err)
	}
	if row.IsDir || row.Unsupported != "" {
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
	return req.m.SetView(c, req.user.ID, req.conn.ID, view)
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
		return nil, connected.Error(err)
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

	release, err := connected.AcquireStream(ctx)
	if err != nil {
		c.Header("Retry-After", "5")
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	defer release()

	item := grant.Item.ProviderItem()
	// The file as it is now. A grant is for one state of one item: when the content
	// changed since, the client has to ask for a new address.
	if grant.Item.Version != "" {
		current, err := conn.P.Get(ctx, item.ID)
		if err != nil {
			connectedContentError(c, err)
			return
		}
		if current.Version != grant.Item.Version {
			c.AbortWithStatus(http.StatusConflict)
			return
		}
	}

	name := item.Name
	var content *provider.Content
	if item.Native != "" {
		name = connected.ExportName(item.Name, item.Native, grant.Export)
		content, err = conn.P.Export(ctx, item, grant.Export)
	} else {
		content, err = conn.P.Open(ctx, item, c.GetHeader("Range"))
	}
	if err != nil {
		connectedContentError(c, err)
		return
	}
	defer content.Body.Close()

	disposition := "inline"
	if grant.Download {
		disposition = "attachment"
	}
	c.Header("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": name}))
	c.Header("Content-Type", dep.MimeDetector(c).TypeByName(name))
	if item.Native == "" {
		c.Header("Accept-Ranges", "bytes")
	}
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
	if c.Request.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(c.Writer, content.Body)
}

func connectedContentError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, context.Canceled):
		c.AbortWithStatus(499)
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
