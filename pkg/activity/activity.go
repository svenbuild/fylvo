// Package activity is the Fylvo activity log: file changes, share changes and sign-ins.
//
// Record never touches the database. It hangs the entry on the transaction in the context, so
// it only counts once the change is really stored, and then hands it to a buffer. One writer
// stores the buffer in batches (SQLite has a single connection). When the buffer is full,
// entries are dropped with a warning: a file operation never waits for the log.
package activity

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/auth/requestinfo"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
)

// Type of an entry. The numbers are stored in the database: never change them.
type Type int

const (
	FileCreate     Type = 1  // empty file or folder, without upload
	FileUpload     Type = 2  // new file uploaded
	FileUpdate     Type = 3  // new version of an existing file
	FileRename     Type = 4  //
	FileMove       Type = 5  //
	FileCopy       Type = 6  // FileID is the new copy
	FileTrash      Type = 7  //
	FileRestore    Type = 8  //
	FileDelete     Type = 9  // for good
	VersionRestore Type = 10 //
	VersionDelete  Type = 11 //
	ShareCreate    Type = 12 //
	ShareUpdate    Type = 13 //
	ShareDelete    Type = 14 //
	GuestDownload  Type = 15 // a guest of a share link downloads a file

	Login          Type = 30 //
	LoginFailed    Type = 31 // wrong password or code for a known account
	LoginLocked    Type = 32 // a try refused because of too many wrong guesses
	PasswordChange Type = 33 //
	TwoFAEnable    Type = 34 //
	TwoFADisable   Type = 35 //
	AppRevoked     Type = 36 // access of a connected app removed
	AccountDelete  Type = 37 // an admin deleted an account
)

// AccountTypes are the entries shown under "recent sign-ins".
var AccountTypes = []int{int(Login), int(LoginFailed), int(LoginLocked), int(PasswordChange),
	int(TwoFAEnable), int(TwoFADisable), int(AppRevoked)}

// Ways a change came in, besides the web app and the apps (told apart by the user agent).
const (
	ViaWebDAV = "webdav"
	ViaOffice = "office"
	ViaLink   = "link"
	ViaSystem = "system"
)

// Entry is one log entry. Callers fill in everything they know; Record adds address, device
// and the way the request came in.
type Entry struct {
	Type Type
	// UserID did it; 0 for the system and for guests of a share link.
	UserID int
	// OwnerID is the owner of the file or the account the entry is about.
	OwnerID      int
	FileID       int
	ParentID     int
	FromParentID int
	ShareID      int
	Content      types.ActivityContent
}

type (
	skipCtx   struct{}
	systemCtx struct{}
	viaCtx    struct{}
)

// Skip returns a context in which nothing is recorded (cancelled uploads, imports, extracting).
func Skip(ctx context.Context) context.Context {
	return context.WithValue(ctx, skipCtx{}, true)
}

// System returns a context whose entries are done by the system, not by the user in it.
func System(ctx context.Context) context.Context {
	return context.WithValue(ctx, systemCtx{}, true)
}

// WithVia returns a context whose entries came in the given way (ViaWebDAV, ViaOffice).
func WithVia(ctx context.Context, via string) context.Context {
	return context.WithValue(ctx, viaCtx{}, via)
}

// Skipped reports whether nothing is recorded in ctx.
func Skipped(ctx context.Context) bool {
	v, _ := ctx.Value(skipCtx{}).(bool)
	return v
}

// Record logs e once the changes made with ctx are stored.
func Record(ctx context.Context, e Entry) {
	if Skipped(ctx) {
		return
	}

	via, _ := ctx.Value(viaCtx{}).(string)
	if system, _ := ctx.Value(systemCtx{}).(bool); system {
		e.UserID = 0
		via = ViaSystem
	} else if via == "" && e.UserID == 0 && e.ShareID > 0 {
		via = ViaLink
	}

	r := &row{Entry: e, at: time.Now(), via: via}
	if info := requestinfo.RequestInfoFromContext(ctx); info != nil && via != ViaSystem {
		r.ip = info.IP
		r.userAgent = truncate(info.UserAgent, 256)
	}

	inventory.AfterCommit(ctx, func() { current().push(r) })
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	// Do not cut a UTF-8 sequence in half.
	for max > 0 && s[max]&0xC0 == 0x80 {
		max--
	}
	return s[:max]
}

type row struct {
	Entry
	at        time.Time
	via       string
	ip        string
	userAgent string
}

const (
	bufferSize    = 1000
	batchSize     = 100
	flushInterval = 3 * time.Second
)

// Recorder buffers entries and writes them in batches.
type Recorder struct {
	client  inventory.ActivityClient
	l       logging.Logger
	mu      sync.RWMutex
	closed  bool
	rows    chan *row
	flushes chan chan struct{}
	done    chan struct{}
	dropped atomic.Int64
}

var (
	defaultMu       sync.RWMutex
	defaultRecorder *Recorder
)

func current() *Recorder {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultRecorder
}

// Start starts the writer and makes it the one Record uses. It replaces an earlier one, which
// is closed first.
func Start(client inventory.ActivityClient, l logging.Logger) *Recorder {
	r := &Recorder{
		client:  client,
		l:       l,
		rows:    make(chan *row, bufferSize),
		flushes: make(chan chan struct{}),
		done:    make(chan struct{}),
	}
	go r.run()

	defaultMu.Lock()
	old := defaultRecorder
	defaultRecorder = r
	defaultMu.Unlock()
	old.Close()
	return r
}

// Close stops taking entries, writes what is left and waits for the writer. Entries recorded
// after Close are dropped.
func (r *Recorder) Close() {
	if r == nil {
		return
	}

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		<-r.done
		return
	}
	r.closed = true
	close(r.rows)
	r.mu.Unlock()
	<-r.done
}

// Flush waits until everything recorded so far is written. Used by tests.
func (r *Recorder) Flush() {
	if r == nil {
		return
	}

	r.mu.RLock()
	if r.closed {
		r.mu.RUnlock()
		return
	}
	reply := make(chan struct{})
	r.flushes <- reply
	r.mu.RUnlock()
	<-reply
}

func (r *Recorder) push(e *row) {
	if r == nil {
		return
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return
	}

	select {
	case r.rows <- e:
	default:
		if n := r.dropped.Add(1); n == 1 || n%100 == 0 {
			r.l.Warning("Activity log buffer is full, %d entries dropped so far.", n)
		}
	}
}

func (r *Recorder) run() {
	defer close(r.done)
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	batch := make([]*row, 0, batchSize)
	for {
		select {
		case e, ok := <-r.rows:
			if !ok {
				r.write(batch)
				return
			}
			batch = append(batch, e)
			if len(batch) >= batchSize {
				r.write(batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			r.write(batch)
			batch = batch[:0]
		case reply := <-r.flushes:
			// Take what is already waiting, then write.
			for drained := false; !drained; {
				select {
				case e := <-r.rows:
					batch = append(batch, e)
				default:
					drained = true
				}
			}
			r.write(batch)
			batch = batch[:0]
			close(reply)
		}
	}
}

func (r *Recorder) write(batch []*row) {
	for start := 0; start < len(batch); start += batchSize {
		end := min(start+batchSize, len(batch))
		creates := make([]*ent.ActivityCreate, 0, end-start)
		for _, e := range batch[start:end] {
			creates = append(creates, r.client.GetClient().Activity.Create().
				SetCreatedAt(e.at).
				SetType(int(e.Type)).
				SetNillableUserID(nonZero(e.UserID)).
				SetNillableOwnerID(nonZero(e.OwnerID)).
				SetNillableFileID(nonZero(e.FileID)).
				SetNillableParentID(nonZero(e.ParentID)).
				SetNillableFromParentID(nonZero(e.FromParentID)).
				SetNillableShareID(nonZero(e.ShareID)).
				SetIP(e.ip).
				SetUserAgent(e.userAgent).
				SetVia(e.via).
				SetContent(&e.Content))
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := r.client.CreateBulk(ctx, creates); err != nil {
			r.l.Warning("Failed to write %d activity log entries: %s", len(creates), err)
		}
		cancel()
	}
}

func nonZero(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}
