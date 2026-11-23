package manager

import (
	"context"
	"math"
	"time"

	"github.com/cloudreve/Cloudreve/v4/application/constants"
	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/user"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs/dbfs"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/util"
)

// Fylvo: thumbnails made ahead of time. Without them a folder of photos opened for the first time
// waits until every thumbnail is made. A single background loop on the master has the thumbnails
// of files that have none made, the same way as when a file is viewed: through the thumbnail
// queue, so all generators together never exceed the queue's workers, and waiting for the result,
// so at most one of its tasks is queued or running. After each thumbnail it pauses as long as
// making it took.
//
// A full pass walks all files without a thumbnail, newest first; it runs shortly after the start
// and then every hour, and so also catches files that got a new version. In between, a wake-up
// after an upload only looks at files newer than any it has seen. Nothing is kept between
// restarts except what is in the database.

const (
	thumbPregenBatch      = 100
	thumbPregenFirstDelay = time.Minute
	thumbPregenFullEvery  = time.Hour
	thumbPregenMinPause   = 200 * time.Millisecond
	// thumbPregenItemTimeout ends the wait for one thumbnail (the queue's own limit is shorter).
	thumbPregenItemTimeout = 15 * time.Minute
	thumbPregenLogEvery    = 500
)

var thumbPregenWake = make(chan struct{}, 1)

// WakeThumbPregen tells the background loop that new files are there.
func WakeThumbPregen() {
	select {
	case thumbPregenWake <- struct{}{}:
	default:
	}
}

// StartThumbPregen starts the background loop. It ends with ctx; the returned channel is closed
// when it has ended.
func StartThumbPregen(ctx context.Context, dep dependency.Dep) <-chan struct{} {
	l := dep.Logger().CopyWithPrefix("[ThumbPregen]")
	ctx = dep.ForkWithLogger(ctx, l)
	ctx = context.WithValue(ctx, logging.LoggerCtx{}, l)
	p := &thumbPregen{dep: dep, l: l, failedFiles: map[[2]int]struct{}{}, top: -1}
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.run(ctx)
	}()
	return done
}

type thumbPregen struct {
	dep dependency.Dep
	l   logging.Logger
	// failedFiles holds file id and version of files whose thumbnail failed in this run of the
	// server; they are left to the first view.
	failedFiles map[[2]int]struct{}
	// top is the highest file id seen; a wake-up looks only above it. -1: no pass yet.
	top int
	// Per batch.
	owners map[int]*ent.User
	// Per walk, for the log.
	made, failed, lastLogged int
}

func (p *thumbPregen) run(ctx context.Context) {
	nextFull := time.Now().Add(thumbPregenFirstDelay)
	for {
		full := !time.Now().Before(nextFull)
		if p.dep.SettingProvider().ThumbPregenEnabled(ctx) && (full || p.top >= 0) {
			after := p.top
			if full {
				after = 0
				nextFull = time.Now().Add(thumbPregenFullEvery)
			}
			p.made, p.failed, p.lastLogged = 0, 0, 0
			if p.walk(ctx, after) && (p.made > 0 || p.failed > 0) {
				p.l.Info("Done: %d thumbnails made, %d failed.", p.made, p.failed)
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-thumbPregenWake:
		case <-time.After(time.Until(nextFull)):
		}
	}
}

// walk handles the files without a thumbnail with an id above after, newest first. Between
// batches it does new files first when woken. It returns false when it was stopped.
func (p *thumbPregen) walk(ctx context.Context, after int) bool {
	before := math.MaxInt
	for {
		if ctx.Err() != nil || !p.dep.SettingProvider().ThumbPregenEnabled(ctx) {
			return false
		}

		// New files during a long walk (a pass can take days the first time).
		if p.top > after && before <= p.top {
			select {
			case <-thumbPregenWake:
				if !p.walk(ctx, p.top) {
					return false
				}
			default:
			}
		}

		files, err := p.dep.FileClient().ThumbCandidates(ctx, after, before, thumbPregenBatch, dbfs.ThumbDisabledKey)
		if err != nil {
			if ctx.Err() == nil {
				p.l.Warning("Failed to list files without thumbnail: %s", err)
			}
			return false
		}
		if len(files) == 0 {
			return true
		}
		if files[0].ID > p.top {
			p.top = files[0].ID
		}

		p.owners = map[int]*ent.User{}
		for _, f := range files {
			if !p.one(ctx, f) {
				return false
			}
		}
		before = files[len(files)-1].ID
	}
}

// one has the thumbnail of one file made if it is a candidate. It returns false when stopped.
func (p *thumbPregen) one(ctx context.Context, f *ent.File) bool {
	key := [2]int{f.ID, f.PrimaryEntity}
	if _, failed := p.failedFiles[key]; failed || !p.supported(ctx, f.Name) {
		return true
	}

	owner, ok := p.owner(ctx, f.OwnerID)
	if !ok {
		return true
	}

	fm := NewFileManager(p.dep, owner).(*manager)
	userCtx := context.WithValue(ctx, inventory.UserCtx{}, owner)

	// A full account gets no thumbnail (they count towards the quota).
	if capacity, err := fm.fs.Capacity(userCtx, owner); err != nil || capacity.Used >= capacity.Total {
		fm.Recycle()
		return true
	}

	target, err := fm.fs.TraverseFile(userCtx, f.ID)
	if err != nil || target.Uri(true).FileSystem() == constants.FileSystemTrash {
		fm.Recycle()
		return true
	}
	uri := target.Uri(true)

	start := time.Now()
	itemCtx, cancel := context.WithTimeout(withThumbPregen(userCtx), thumbPregenItemTimeout)
	src, err := fm.Thumbnail(itemCtx, uri)
	// Stopped waiting (timeout or shutdown): the queue's worker may still be using the file
	// manager, so it is not recycled then.
	stoppedWaiting := itemCtx.Err() != nil
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return false
		}
		p.failedFiles[key] = struct{}{}
		p.failed++
		p.l.Debug("No thumbnail for %q: %s", uri.String(), err)
	} else {
		src.Close()
		p.made++
	}
	if !stoppedWaiting {
		fm.Recycle()
	}

	if done := p.made + p.failed; done-p.lastLogged >= thumbPregenLogEvery {
		p.lastLogged = done
		p.l.Info("%d thumbnails made, %d failed so far.", p.made, p.failed)
	}

	select {
	case <-ctx.Done():
		return false
	case <-time.After(max(thumbPregenMinPause, time.Since(start))):
		return true
	}
}

// owner returns the active owner of a file, cached for one batch.
func (p *thumbPregen) owner(ctx context.Context, id int) (*ent.User, bool) {
	if u, ok := p.owners[id]; ok {
		return u, u != nil
	}
	u, err := p.dep.UserClient().GetByID(context.WithValue(ctx, inventory.LoadUserGroup{}, true), id)
	if err != nil || u.Status != user.StatusActive {
		u = nil
	}
	p.owners[id] = u
	return u, u != nil
}

// supported tells if an enabled generator makes thumbnails of this file name. Office files are
// left to the first view: LibreOffice needs more memory than a small server has to spare.
func (p *thumbPregen) supported(ctx context.Context, name string) bool {
	s := p.dep.SettingProvider()
	ext := util.Ext(name)
	switch {
	case s.BuiltinThumbGeneratorEnabled(ctx) && util.IsInExtensionListExt([]string{"jpg", "jpeg", "png", "gif"}, ext),
		s.VipsThumbGeneratorEnabled(ctx) && util.IsInExtensionListExt(s.VipsThumbExts(ctx), ext),
		s.FFMpegThumbGeneratorEnabled(ctx) && util.IsInExtensionListExt(s.FFMpegThumbExts(ctx), ext),
		s.MusicCoverThumbGeneratorEnabled(ctx) && util.IsInExtensionListExt(s.MusicCoverThumbExts(ctx), ext),
		s.LibRawThumbGeneratorEnabled(ctx) && util.IsInExtensionListExt(s.LibRawThumbExts(ctx), ext):
		return true
	}
	return false
}

type thumbPregenCtx struct{}

func withThumbPregen(ctx context.Context) context.Context {
	return context.WithValue(ctx, thumbPregenCtx{}, true)
}

func isThumbPregen(ctx context.Context) bool {
	v, _ := ctx.Value(thumbPregenCtx{}).(bool)
	return v
}
