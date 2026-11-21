package dbfs

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/stretchr/testify/require"
)

// peakHeap runs f and returns how much the heap grew at most while it ran, and how long it took.
func peakHeap(f func()) (uint64, time.Duration) {
	// Collect often, so the peak is close to what is really held.
	defer debug.SetGCPercent(debug.SetGCPercent(10))
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	base := m.HeapInuse
	var peak atomic.Uint64
	done := make(chan struct{})
	go func() {
		var s runtime.MemStats
		for {
			select {
			case <-done:
				return
			case <-time.After(20 * time.Millisecond):
				runtime.ReadMemStats(&s)
				if s.HeapInuse > peak.Load() {
					peak.Store(s.HeapInuse)
				}
			}
		}
	}()
	start := time.Now()
	f()
	took := time.Since(start)
	close(done)
	runtime.ReadMemStats(&m)
	if m.HeapInuse > peak.Load() {
		peak.Store(m.HeapInuse)
	}
	if peak.Load() < base {
		return 0, took
	}
	return peak.Load() - base, took
}

// Measures memory and time of walking one folder with many files. Only runs with
// FYLVO_MEASURE=<count>.
func TestMeasureBigFolder(t *testing.T) {
	count, _ := strconv.Atoi(os.Getenv("FYLVO_MEASURE"))
	if count == 0 {
		t.Skip("set FYLVO_MEASURE")
	}
	e := newSharedFolderEnv(t)
	owner := e.as(e.owner)
	e.mkdir(owner, "cloudreve://my/Big")
	e.mkdir(owner, "cloudreve://my/Copy")
	big, err := owner.Get(e.ctx, e.uri("cloudreve://my/Big"))
	require.NoError(t, err)

	const batch = 500
	for start := 0; start < count; start += batch {
		n := min(batch, count-start)
		entities := make([]*ent.EntityCreate, 0, n)
		for i := 0; i < n; i++ {
			entities = append(entities, e.client.Entity.Create().SetType(int(types.EntityTypeVersion)).
				SetSource(fmt.Sprintf("Nextcloud/Fotos/2019/IMG_%06d.jpg", start+i)).SetSize(3_000_000).
				SetStoragePolicyEntities(1).SetCreatedBy(e.owner.ID))
		}
		created, err := e.client.Entity.CreateBulk(entities...).Save(e.ctx)
		require.NoError(t, err)
		files := make([]*ent.FileCreate, 0, n)
		for i := 0; i < n; i++ {
			files = append(files, e.client.File.Create().SetType(int(types.FileTypeFile)).
				SetName(fmt.Sprintf("IMG_%06d.jpg", start+i)).SetOwnerID(e.owner.ID).
				SetFileChildren(big.ID()).SetSize(3_000_000).SetStoragePolicyFiles(1).
				SetPrimaryEntity(created[i].ID).AddEntityIDs(created[i].ID))
		}
		madeFiles, err := e.client.File.CreateBulk(files...).Save(e.ctx)
		require.NoError(t, err)
		metadata := make([]*ent.MetadataCreate, 0, n*4)
		for _, f := range madeFiles {
			for _, name := range []string{"exif:camera_make", "exif:camera_model", "exif:taken_at", "thumb:disabled"} {
				metadata = append(metadata, e.client.Metadata.Create().SetFileID(f.ID).SetName(name).
					SetValue("Apple iPhone 12 Pro 2019-07-14T12:00:00Z").SetIsPublic(true))
			}
		}
		require.NoError(t, e.client.Metadata.CreateBulk(metadata...).Exec(e.ctx))
	}

	// No limit for the walk.
	group, err := e.client.Group.Get(e.ctx, 2)
	require.NoError(t, err)
	settings := *group.Settings
	settings.MaxWalkedFiles = 1_000_000
	require.NoError(t, e.client.Group.UpdateOne(group).SetSettings(&settings).SetMaxStorage(1<<50).Exec(e.ctx))
	e.kv.Delete("", "group_2") // fresh group in the next reload, whatever the cache key

	grew, took := peakHeap(func() {
		f, err := e.as(e.owner).Get(e.ctx, e.uri("cloudreve://my/Big"), WithLoadFolderSummary())
		require.NoError(t, err)
		require.True(t, f.FolderSummary().Completed)
		require.Equal(t, count, f.FolderSummary().Files)
	})
	t.Logf("folder size: heap +%d MB in %s", grew>>20, took)

	grew, took = peakHeap(func() {
		n := 0
		require.NoError(t, e.as(e.owner).Walk(e.ctx, e.uri("cloudreve://my/Big"), 1<<30, func(f fs.File, level int) error {
			n++
			if n == count*3/4 && os.Getenv("FYLVO_PROFILE") != "" {
				runtime.GC()
				out, _ := os.Create(os.Getenv("FYLVO_PROFILE"))
				_ = pprof.WriteHeapProfile(out)
				_ = out.Close()
			}
			return nil
		}))
		require.Equal(t, count+1, n)
	})
	t.Logf("walk (ZIP): heap +%d MB in %s", grew>>20, took)
	if os.Getenv("FYLVO_PROFILE") != "" {
		return
	}

	grew, took = peakHeap(func() {
		_, err := e.as(e.owner).MoveOrCopy(e.ctx, []*fs.URI{e.uri("cloudreve://my/Big")}, e.uri("cloudreve://my/Copy"), true)
		require.NoError(t, err)
	})
	t.Logf("copy: heap +%d MB in %s", grew>>20, took)

	grew, took = peakHeap(func() {
		_, _, err := e.as(e.owner).Delete(e.ctx, []*fs.URI{e.uri("cloudreve://my/Copy/Big")}, fs.WithSkipSoftDelete(true))
		require.NoError(t, err)
	})
	t.Logf("delete for good: heap +%d MB in %s", grew>>20, took)
}
