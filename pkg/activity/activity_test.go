package activity

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/enttest"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/pkg/auth/requestinfo"
	"github.com/cloudreve/Cloudreve/v4/pkg/cache"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/stretchr/testify/require"
)

func newLog(t *testing.T) (*ent.Client, *Recorder) {
	logger := logging.NewConsoleLogger(logging.LevelError)
	client := enttest.Open(t, "sqlite3", filepath.Join(t.TempDir(), "fylvo.db"))
	t.Cleanup(func() { _ = client.Close() })
	client, err := inventory.InitializeDBClient(logger, client, cache.NewMemoStore("", logger), "4.19.1")
	require.NoError(t, err)
	r := Start(inventory.NewActivityClient(client), logger)
	t.Cleanup(r.Close)
	return client, r
}

func count(t *testing.T, client *ent.Client) int {
	n, err := client.Activity.Query().Count(context.Background())
	require.NoError(t, err)
	return n
}

func TestRecordWritesWithAddressAndAfterCommit(t *testing.T) {
	client, r := newLog(t)
	ctx := context.WithValue(context.Background(), requestinfo.RequestInfoCtx{},
		&requestinfo.RequestInfo{IP: "192.0.2.9", UserAgent: "Mozilla/5.0"})

	// Inside a transaction: only once it is committed.
	fc, tx, txCtx, err := inventory.WithTx(ctx, inventory.NewActivityClient(client))
	require.NoError(t, err)
	_ = fc
	Record(txCtx, Entry{Type: FileUpload, UserID: 7, FileID: 3})
	r.Flush()
	require.Zero(t, count(t, client))
	require.NoError(t, inventory.Commit(tx))
	r.Flush()
	require.Equal(t, 1, count(t, client))

	// A rolled back transaction drops it, also when recorded afterwards with its context.
	_, tx, txCtx, err = inventory.WithTx(ctx, inventory.NewActivityClient(client))
	require.NoError(t, err)
	Record(txCtx, Entry{Type: FileUpload, UserID: 7, FileID: 4})
	require.NoError(t, inventory.Rollback(tx))
	Record(txCtx, Entry{Type: FileUpload, UserID: 7, FileID: 5})
	r.Flush()
	require.Equal(t, 1, count(t, client))

	entry, err := client.Activity.Query().Only(ctx)
	require.NoError(t, err)
	require.Equal(t, "192.0.2.9", entry.IP)
	require.Equal(t, "Mozilla/5.0", entry.UserAgent)
	require.Equal(t, 7, entry.UserID)
	require.Equal(t, 3, entry.FileID)
	require.Empty(t, entry.Via)
}

func TestRecordMarkers(t *testing.T) {
	client, r := newLog(t)
	ctx := context.WithValue(context.Background(), requestinfo.RequestInfoCtx{}, &requestinfo.RequestInfo{IP: "192.0.2.9"})

	Record(Skip(ctx), Entry{Type: FileUpload, UserID: 1})
	Record(System(ctx), Entry{Type: FileDelete, UserID: 1})
	Record(WithVia(ctx, ViaWebDAV), Entry{Type: FileUpdate, UserID: 1})
	Record(ctx, Entry{Type: FileUpload, ShareID: 4})
	r.Flush()

	entries, err := client.Activity.Query().Order(ent.Asc("id")).All(ctx)
	require.NoError(t, err)
	require.Len(t, entries, 3)
	require.Zero(t, entries[0].UserID, "the system did it")
	require.Equal(t, ViaSystem, entries[0].Via)
	require.Empty(t, entries[0].IP)
	require.Equal(t, ViaWebDAV, entries[1].Via)
	require.Equal(t, ViaLink, entries[2].Via)
}

func TestCloseWritesTheRestAndDropsLaterEntries(t *testing.T) {
	client, r := newLog(t)
	for i := 0; i < batchSize+5; i++ {
		Record(context.Background(), Entry{Type: Login, UserID: 1, OwnerID: 1})
	}
	r.Close()
	require.Equal(t, batchSize+5, count(t, client))

	Record(context.Background(), Entry{Type: Login, UserID: 1, OwnerID: 1})
	r.Flush()
	require.Equal(t, batchSize+5, count(t, client))
}

func TestFullBufferDropsInsteadOfWaiting(t *testing.T) {
	logger := logging.NewConsoleLogger(logging.LevelError)
	// A writer that never runs: the buffer fills up.
	r := &Recorder{l: logger, rows: make(chan *row, 2), flushes: make(chan chan struct{}), done: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		for i := 0; i < 5; i++ {
			r.push(&row{})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("push blocked on a full buffer")
	}
	require.EqualValues(t, 3, r.dropped.Load())
}

func TestFailureThrottle(t *testing.T) {
	now := time.Now()
	th := &throttle{now: func() time.Time { return now }, seen: make(map[string]time.Time)}
	require.True(t, th.allow("a"))
	require.False(t, th.allow("a"))
	require.True(t, th.allow("b"))
	now = now.Add(failureQuiet)
	require.True(t, th.allow("a"))
}

func TestTruncateKeepsUTF8(t *testing.T) {
	require.Equal(t, "abc", truncate("abc", 5))
	require.Equal(t, "a", truncate("aä", 2), "the two bytes of ä are not cut apart")
	require.Equal(t, "aä", truncate("aäb", 3))
}
