package manager

import (
	"context"
	"sync"
	"time"

	"github.com/cloudreve/Cloudreve/v4/application/constants"
	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/pkg/crontab"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs/dbfs"
	"github.com/cloudreve/Cloudreve/v4/pkg/setting"
)

// Fylvo: an upload that is abandoned (tab closed, browser crashed, right taken away) leaves a placeholder
// in the owner's files, marked as uploading. A recurring sweep removes such placeholders once their upload
// can no longer go on, the same way as cancelling does, as the owner of the file. It keeps nothing in
// memory between runs; everything it needs is in the database.

// staleUploadGrace is waited on top of the lifetime of an upload session. A session never lives longer
// than its lifetime from the start of the upload; the extra day covers a request that was still running
// when the session ran out, and clocks that are off. Waiting longer only keeps some reserved space.
const staleUploadGrace = 24 * time.Hour

func init() {
	crontab.Register(setting.CronTypeFylvoStaleUploads, CronRemoveStaleUploads)
}

var uploadRequests = struct {
	sync.Mutex
	running map[string]int
}{running: map[string]int{}}

// TrackUploadRequest marks a request that writes to an upload session as running, until the returned
// function is called. The sweep leaves such an upload alone, however old it is.
func TrackUploadRequest(sessionID string) func() {
	uploadRequests.Lock()
	uploadRequests.running[sessionID]++
	uploadRequests.Unlock()

	return func() {
		uploadRequests.Lock()
		defer uploadRequests.Unlock()
		if uploadRequests.running[sessionID]--; uploadRequests.running[sessionID] <= 0 {
			delete(uploadRequests.running, sessionID)
		}
	}
}

func uploadRequestRunning(sessionID string) bool {
	uploadRequests.Lock()
	defer uploadRequests.Unlock()
	return uploadRequests.running[sessionID] > 0
}

// CronRemoveStaleUploads removes the placeholders of uploads that were started long enough ago that they
// can no longer be finished.
func CronRemoveStaleUploads(ctx context.Context) {
	dep := dependency.FromContext(ctx)
	l := dep.Logger()

	startedBefore := time.Now().Add(-(max(dep.SettingProvider().UploadSessionTTL(ctx), 24*time.Hour) + staleUploadGrace))
	removed, found, err := sweepStaleUploads(ctx, dep, startedBefore)
	if err != nil {
		l.Error("Failed to find placeholders of abandoned uploads: %s", err)
		return
	}

	if removed > 0 {
		l.Info("Removed %d of %d placeholders of abandoned uploads.", removed, found)
	} else if found > 0 {
		l.Debug("None of %d placeholders of abandoned uploads could be removed yet.", found)
	}
}

// sweepStaleUploads removes the placeholders of uploads started before the given time whose upload is
// surely over. It returns how many it removed and how many it found.
func sweepStaleUploads(ctx context.Context, dep dependency.Dep, startedBefore time.Time) (int, int, error) {
	placeholders, err := dep.FileClient().StaleUploads(ctx, startedBefore)
	if err != nil {
		return 0, 0, err
	}

	removed := 0
	for _, placeholder := range placeholders {
		if removeStaleUpload(ctx, dep, placeholder, startedBefore) {
			removed++
		}
	}

	return removed, len(placeholders), nil
}

// removeStaleUpload removes one placeholder if its upload is surely over. It reports whether it did.
func removeStaleUpload(ctx context.Context, dep dependency.Dep, placeholder *ent.Entity, startedBefore time.Time) bool {
	l := dep.Logger()
	if placeholder.UploadSessionID == nil || len(placeholder.Edges.File) != 1 || !placeholder.CreatedAt.Before(startedBefore) {
		return false
	}

	sessionID := placeholder.UploadSessionID.String()
	file := placeholder.Edges.File[0]

	// The session is gone from the cache, and no request is writing to it.
	if _, ok := dep.KV().Get(UploadSessionCachePrefix + sessionID); ok || uploadRequestRunning(sessionID) {
		return false
	}

	ctx = context.WithValue(ctx, inventory.LoadUserGroup{}, true)
	owner, err := dep.UserClient().GetByID(ctx, file.OwnerID)
	if err != nil {
		l.Warning("Abandoned upload %q: failed to get owner %d: %s", sessionID, file.OwnerID, err)
		return false
	}

	ctx = context.WithValue(ctx, inventory.UserCtx{}, owner)
	ctx = context.WithValue(ctx, inventory.LoadFileMetadata{}, true)
	fm := NewFileManager(dep, owner).(*manager)
	defer fm.Recycle()

	target, err := fm.fs.TraverseFile(ctx, file.ID)
	if err != nil {
		l.Warning("Abandoned upload %q: failed to find file %d: %s", sessionID, file.ID, err)
		return false
	}

	// A file in the trash goes as a whole when the trash is emptied.
	uri := target.Uri(true)
	if uri.FileSystem() == constants.FileSystemTrash {
		return false
	}

	session := &fs.UploadSession{
		Props:          &fs.UploadProps{Uri: uri, UploadSessionID: sessionID},
		FileID:         file.ID,
		UID:            owner.ID,
		NewFileCreated: target.Metadata()[dbfs.MetadataUploadNewFile] == sessionID,
	}
	if err := fm.removeUploadPlaceholder(ctx, uri, sessionID, session); err != nil {
		l.Warning("Abandoned upload %q: failed to remove the placeholder of %q: %s", sessionID, uri.String(), err)
		return false
	}

	releaseUploadSessionLock(sessionID)
	l.Info("Removed the placeholder of abandoned upload %q in %q.", sessionID, uri.String())
	return true
}
