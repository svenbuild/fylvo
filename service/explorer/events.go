package explorer

import (
	"context"
	"time"

	"github.com/cloudreve/Cloudreve/v4/application/constants"
	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/pkg/auth/requestinfo"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/manager"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
)

type (
	ExplorerEventService struct {
		Uri string `form:"uri" binding:"required"`
	}
	ExplorerEventParamCtx struct{}
)

// canStillListen reports whether the user may still open the folder behind uri and whether
// it is still the folder the subscription was made for.
func canStillListen(c *gin.Context, dep dependency.Dep, user *ent.User, uri *fs.URI, folderID int) bool {
	// The account as it is now: it can have been deactivated or moved to another group.
	current, err := dep.UserClient().GetActiveByID(context.WithValue(c, inventory.LoadUserGroup{}, true), user.ID)
	if err != nil {
		return false
	}

	m := manager.NewFileManager(dep, current)
	defer m.Recycle()

	parent, _, err := m.List(c, uri, &manager.ListArgs{
		Page:     0,
		PageSize: 1,
	})

	return err == nil && parent.ID() == folderID
}

func (s *ExplorerEventService) HandleExplorerEventsPush(c *gin.Context) error {
	dep := dependency.FromContext(c)
	user := inventory.UserFromContext(c)
	m := manager.NewFileManager(dep, user)
	l := logging.FromContext(c)
	defer m.Recycle()

	uri, err := fs.NewUriFromString(s.Uri)
	if err != nil {
		return serializer.NewError(serializer.CodeParamErr, "Unknown uri", err)
	}

	// Make sure target is a valid folder that the user can listen to
	parent, listRes, err := m.List(c, uri, &manager.ListArgs{
		Page:     0,
		PageSize: 1,
	})
	if err != nil {
		return serializer.NewError(serializer.CodeParamErr, "Requested uri not available", err)
	}

	// Reject event subscriptions on single-file views (e.g. single-file shares).
	// The listed parent is the underlying owner-side folder containing the file,
	// while the subscriber is only authorized to observe the shared file itself.
	// Subscribing to that folder topic would leak events about unshared siblings.
	if listRes != nil && listRes.SingleFileView {
		return serializer.NewError(serializer.CodeNoPermissionErr, "Event subscriptions are not supported on this view", nil)
	}

	requestInfo := requestinfo.RequestInfoFromContext(c)
	if requestInfo.ClientID == "" {
		return serializer.NewError(serializer.CodeParamErr, "Client ID is required", nil)
	}

	// Client ID must be a valid UUID
	if _, err := uuid.FromString(requestInfo.ClientID); err != nil {
		return serializer.NewError(serializer.CodeParamErr, "Invalid client ID", err)
	}

	// Subscribe
	eventHub := dep.EventHub()
	rx, resumed, err := eventHub.Subscribe(c, parent.ID(), requestInfo.ClientID)
	if err != nil {
		return serializer.NewError(serializer.CodeInternalSetting, "Failed to subscribe to events", err)
	}

	// Fylvo: events of a share are not collected while the client is away. A subscriber that
	// is only marked offline keeps collecting, and what it collects is handed out by client ID
	// later, also after the user has lost access to the share. A client that comes back
	// subscribes anew and syncs from scratch.
	leave := func() {
		if uri.FileSystem() == constants.FileSystemShare {
			eventHub.Remove(c, parent.ID(), requestInfo.ClientID)
			return
		}

		eventHub.Unsubscribe(c, parent.ID(), requestInfo.ClientID)
	}

	// SSE Headers
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")

	keepAliveTicker := time.NewTicker(30 * time.Second)
	defer keepAliveTicker.Stop()

	if resumed {
		c.SSEvent("resumed", nil)
		c.Writer.Flush()
	} else {
		c.SSEvent("subscribed", nil)
		c.Writer.Flush()
	}

	for {
		select {
		// TODO: close connection after access token expired
		case <-c.Request.Context().Done():
			// Server shutdown or request cancelled
			leave()
			l.Debug("Request context done, unsubscribed from event hub")
			return nil
		case <-c.Writer.CloseNotify():
			leave()
			l.Debug("Unsubscribed from event hub")
			return nil
		case evt, ok := <-rx:
			if !ok {
				// Channel closed, EventHub is shutting down
				l.Debug("Event hub closed, disconnecting client")
				return nil
			}
			c.SSEvent("event", evt)
			l.Debug("Event sent: %+v", evt)
			c.Writer.Flush()
			if c.Errors.Last() != nil {
				l.Error("Error occurred: %+v", c.Errors.Last().Error())
				leave()
				l.Debug("Unsubscribed from event hub")
				return nil
			}
		case <-keepAliveTicker.C:
			// Fylvo: access to a share can end while the stream is open (member removed,
			// share deleted or expired, folder moved away). Check again with every keep-alive
			// and stop sending events then.
			if uri.FileSystem() == constants.FileSystemShare && !canStillListen(c, dep, user, uri, parent.ID()) {
				leave()
				l.Debug("Access to the share ended, unsubscribed from event hub")
				return nil
			}

			c.SSEvent("keep-alive", nil)
			c.Writer.Flush()
		}
	}
}
