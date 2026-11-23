package explorer

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/cloudreve/Cloudreve/v4/application/constants"
	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/pkg/setting"
	"github.com/gin-gonic/gin"
)

// Fylvo: previews of files in connected storages (see pkg/connected/thumbs.go).
//
// A client asks GET /file/thumb for a row it shows and gets an address of this server,
// as for a file of Fylvo. The picture behind it is the provider's own preview. It is
// fetched from the provider the first time somebody loads that address, and kept.

const (
	// connectedThumbPath is where the pictures are served.
	connectedThumbPath = constants.APIPrefix + "/fylvo/connected/thumb/"
	// connectedThumbName ends the address of every preview. It is there for the client
	// only and says nothing about the file.
	connectedThumbName = "thumb.jpg"
	// The bounds of the side of a preview, whatever the installation has set.
	connectedThumbMin = 64
	connectedThumbMax = 1024
)

// connectedThumbSize is the side of the square a preview fits into: the larger side of
// the thumbnails of the installation, within bounds.
func connectedThumbSize(c *gin.Context, settings setting.Provider) int {
	w, h := settings.ThumbSize(c)
	return min(max(w, h, connectedThumbMin), connectedThumbMax)
}

// connectedThumb answers GET /file/thumb for a file of a connected storage. The
// provider is not asked here: the row is as the last listing told it, and the address
// names exactly that state of the file. It is the same address for the whole day (see
// StableThumbExpire), so a client loads the picture once.
func connectedThumb(c *gin.Context, uri *fs.URI) (*FileThumbResponse, error) {
	req, err := openConnected(c, uri)
	if err != nil {
		return nil, err
	}
	defer req.done()

	row, err := req.m.Tree().Resolve(req.ctx, req.conn, req.path)
	if err != nil {
		return nil, connected.Error(c, err)
	}
	settings := dependency.FromContext(c).SettingProvider()
	// The grant of a preview names the item and its state and nothing more: a new name
	// for the same content leaves the address what it was.
	item := connected.GrantItem{ID: row.ID, Version: row.Version, Own: row.ResourceKey}
	if !connected.HasThumbnail(row) || req.m.NoThumbnail(req.conn, item.ProviderItem(), connectedThumbSize(c, settings)) {
		// No preview of this version: a client does not ask for it again.
		return nil, fs.ErrEntityNotExist
	}

	expire := StableThumbExpire(time.Now())
	sealed, err := req.m.SealGrantStable(c, &connected.Grant{
		Kind:        connected.GrantThumb,
		User:        req.user.ID,
		Fingerprint: inventory.PasswordFingerprint(req.user),
		Conn:        req.conn.ID,
		Generation:  req.conn.Generation,
		Item:        item,
		Expires:     expire.Unix(),
	})
	if err != nil {
		return nil, serializer.NewError(serializer.CodeInternalSetting, "Failed to create the address of the preview", err)
	}
	address := settings.SiteURL(c).ResolveReference(&url.URL{Path: connectedThumbPath + sealed + "/" + connectedThumbName})
	return &FileThumbResponse{Url: address.String(), Expires: &expire}, nil
}

// ServeConnectedThumb answers GET /fylvo/connected/thumb/:grant/:name.
//
// Like the content of a file the request carries no sign-in: the grant in its path is the
// permission, for one state of one file of one account, and it is checked in full every
// time, also for a picture that is on disk already. It ends with the password of the
// account, with the right of its group, and with the connection: disconnected or
// connected again, the address is worth nothing from that moment on.
func ServeConnectedThumb(c *gin.Context) {
	dep := dependency.FromContext(c)
	m := connected.For(dep)
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")

	grant, err := m.OpenGrant(c, c.Param("grant"))
	if err != nil || grant.Kind != connected.GrantThumb {
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

	picture, err := m.Thumbnail(ctx, conn, grant.Item.ProviderItem(), connectedThumbSize(c, dep.SettingProvider()))
	switch {
	case errors.Is(err, connected.ErrNoThumbnail):
		c.AbortWithStatus(http.StatusNotFound)
		return
	case errors.Is(err, connected.ErrBusy):
		// Too many previews are being fetched right now: later, not at once.
		c.Header("Retry-After", "5")
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	case err != nil:
		connectedContentError(c, err)
		return
	case ctx.Err() != nil:
		// The connection ended, or was connected again, while this request was at it:
		// nothing of it is handed out any more.
		c.AbortWithStatus(http.StatusNotFound)
		return
	}

	// The client may keep it as long as its address is valid, a day at most, as it
	// keeps the thumbnails of Fylvo's own files.
	keep := min(thumbCacheMaxAge, time.Until(time.Unix(grant.Expires, 0)))
	c.Header("Cache-Control", fmt.Sprintf("private, max-age=%d", max(0, int(keep.Seconds()))))
	c.Data(http.StatusOK, http.DetectContentType(picture), picture)
}
