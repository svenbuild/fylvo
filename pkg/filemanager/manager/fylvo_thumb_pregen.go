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
// waits until every thumbnail is made. A single background loop on the master walks the files
// without a thumbnail, newest first, and has each one made the same way as when it is viewed:
// through the thumbnail queue, so all generators together never exceed the queue's workers, and
// waiting for the result, so at most one of its tasks is queued or running. After each thumbnail
// it pauses as long as making it took. New files (a wake-up after an upload) are done before the
// rest of a pass; after a pass the loop sleeps, then starts again at the top, which also catches
// files that got a new version. It keeps nothing between restarts except what is in the database.

const (
	thumbPregenBatch    = 100
	thumbPregenIdle     = 10 * time.Minute
	thumbPregenMinPause = 200 * time.Millisecond
	thumbPregenLogEvery = 500
)

var thumbPregenWake = make(chan struct{}, 1)

// WakeThumbPregen tells the background loop that new files are there.
func WakeThumbPregen() {
	select {
	case thumbPregenWake <- struct{}{}:
	default:
	}
}

// StartThumbPregen starts the background loop; it ends with ctx.
func StartThumbPregen(ctx context.Context, dep dependency.Dep) {
	l := dep.Logger().CopyWithPrefix("[ThumbPregen]")
	ctx = dep.ForkWithLogger(ctx, l)
	ctx = context.WithValue(ctx, logging.LoggerCtx{}, l)
	p := &thumbPregen{dep: dep, l: l, tried: map[[2]int]struct{}{}}
	go p.run(ctx)
}

type thumbPregen struct {
	dep dependency.Dep
	l   logging.Logger
	// tried holds file id and version of files done in this run of the server. A failure is not
	// tried again before a restart; a file that got no thumbnail for another reason (a storage
	// that makes its own) is not looked at again.
	tried map[[2]int]struct{}
	// Per pass.
	owners                 map[int]*ent.User
	made, failed, lastLogged int
}

func (p *thumbPregen) run(ctx context.Context) {
	for {
		if p.dep.SettingProvider().ThumbPregenEnabled(ctx) {
			p.pass(ctx)
		}

		select {
		case <-ctx.Done():
			return
		case <-thumbPregenWake:
		case <-time.After(thumbPregenIdle):
		}
	}
}

// pass walks all files without a thumbnail once, newest first.
func (p *thumbPregen) pass(ctx context.Context) {
	p.owners = map[int]*ent.User{}
	p.made, p.failed, p.lastLogged = 0, 0, 0
	// Files above top are new since the pass started.
	top := -1
	if !p.walk(ctx, 0, math.MaxInt, &top, nil) {
		return
	}
	if p.made > 0 || p.failed > 0 {
		p.l.Info("Pass done: %d thumbnails made, %d failed.", p.made, p.failed)
	}
}

// walk handles the files without a thumbnail with after < id < before, newest first. Between
// batches it does new files first (above top) when woken; highest gets the highest id it saw.
// It returns false when it was stopped.
func (p *thumbPregen) walk(ctx context.Context, after, before int, top, highest *int) bool {
	for {
		if ctx.Err() != nil || !p.dep.SettingProvider().ThumbPregenEnabled(ctx) {
			return false
		}

		if top != nil && *top >= 0 {
			select {
			case <-thumbPregenWake:
				// A file that comes in meanwhile is above what this saw, and wakes the loop again.
				seen := *top
				if !p.walk(ctx, *top, math.MaxInt, nil, &seen) {
					return false
				}
				*top = seen
			default:
			}
		}

		files, err := p.dep.FileClient().ThumbCandidates(ctx, after, before, thumbPregenBatch, dbfs.ThumbDisabledKey)
		if err != nil {
			p.l.Warning("Failed to list files without thumbnail: %s", err)
			return false
		}
		if len(files) == 0 {
			return true
		}
		if top != nil && *top < 0 {
			*top = files[0].ID
		}
		if highest != nil && files[0].ID > *highest {
			*highest = files[0].ID
		}

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
	if _, ok := p.tried[key]; ok || !p.supported(ctx, f.Name) {
		return true
	}

	owner, ok := p.owner(ctx, f.OwnerID)
	if !ok {
		return true
	}

	fm := NewFileManager(p.dep, owner).(*manager)
	defer fm.Recycle()
	userCtx := context.WithValue(ctx, inventory.UserCtx{}, owner)

	// A full account gets no thumbnail (they count towards the quota).
	if capacity, err := fm.fs.Capacity(userCtx, owner); err != nil || capacity.Used >= capacity.Total {
		return true
	}

	target, err := fm.fs.TraverseFile(userCtx, f.ID)
	if err != nil {
		return true
	}
	uri := target.Uri(true)
	if uri.FileSystem() == constants.FileSystemTrash {
		return true
	}

	p.tried[key] = struct{}{}
	start := time.Now()
	src, err := fm.Thumbnail(withThumbPregen(userCtx), uri)
	if err != nil {
		if ctx.Err() != nil {
			return false
		}
		p.failed++
		p.l.Debug("No thumbnail for %q: %s", uri.String(), err)
	} else {
		src.Close()
		p.made++
	}

	if done := p.made + p.failed; done-p.lastLogged >= thumbPregenLogEvery {
		p.lastLogged = done
		p.l.Info("%d thumbnails made, %d failed so far in this pass.", p.made, p.failed)
	}

	select {
	case <-ctx.Done():
		return false
	case <-time.After(max(thumbPregenMinPause, time.Since(start))):
		return true
	}
}

// owner returns the active owner of a file, cached for the pass.
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
