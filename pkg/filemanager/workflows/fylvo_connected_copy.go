package workflows

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudreve/Cloudreve/v4/application/constants"
	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/task"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs/dbfs"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/manager"
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/queue"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/pkg/util"
)

// Fylvo: "Copy to" between Fylvo and connected storages, in every direction (Fylvo to a
// connected storage, back, and between connected storages). It only copies: nothing at the
// source is changed, nothing at the target is replaced.
//
// The task reads its sources first, at most copyMaxEntries entries, then copies one file
// after the other. A name that is taken at the target is "already there" when it is a file
// of the same size whose checksum (where both sides tell one) does not say otherwise, and
// that this run did not write itself; else the copy gets "name (2)". So a task that was
// stopped can simply run again: what it copied before is found and passed over. That is
// what happens when the queue resumes it after the server ended.

const (
	// copyMaxEntries is the most entries (files and folders) one task copies.
	copyMaxEntries = 20000
	// copyMaxNumbers is how many numbered names are tried for a copy whose name is taken.
	copyMaxNumbers = 100
	// copyMaxNotes is how many entries the report names; the rest are counted.
	copyMaxNotes = 100
	// copyMaxExport is the most an exported document may have.
	copyMaxExport = 100 << 20
	// copyWalkTime is how long the reading of a folder of a connected storage with
	// everything below it may take.
	copyWalkTime = 30 * time.Minute
	// copyWaitForSlot is how long a task waits before it tries again for a place.
	copyWaitForSlot = 30 * time.Second

	ProgressTypeCopyCount = "copy_count"
	ProgressTypeCopySize  = "copy_size"

	SummaryKeyConnectedCopy = "connected_copy"
)

// Reasons in the report of a copy.
const (
	CopyNoteSame       = "same"        // a file of that name and size is there already
	CopyNoteLink       = "link"        // a shortcut, or reached through one: not followed
	CopyNoteDuplicate  = "duplicate"   // the provider has several entries of that name
	CopyNoteNoExport   = "no_export"   // a document of the provider that cannot be exported
	CopyNoteChanged    = "changed"     // it changed while it was copied
	CopyNoteFull       = "full"        // no room at the target
	CopyNoteTooLarge   = "too_large"   // larger than the target takes
	CopyNoteNoName     = "no_name"     // no free name at the target
	CopyNoteFailed     = "failed"      // anything else
	CopyNoteNotReached = "not_reached" // the bound of entries was reached, or a folder could not be read whole
	CopyNoteStopped    = "stopped"     // the task was stopped (cancelled, connection ended)
)

// ConnectedCopyMaxEntries is the most entries one task copies (a variable for tests).
var ConnectedCopyMaxEntries = copyMaxEntries

// copySlots is how many copy tasks run at once on the server. A task that finds none
// free does not hold a worker of the queue: it is suspended and tries again later.
var copySlots = make(chan struct{}, 2)

type (
	ConnectedCopyTask struct {
		*queue.DBTask

		l        logging.Logger
		state    *ConnectedCopyState
		progress queue.Progresses

		stopMu    sync.Mutex
		stop      context.CancelFunc
		halted    bool
		connEnded bool
	}

	ConnectedCopyState struct {
		Uris []string `json:"uris"`
		Dst  string   `json:"dst"`
		// Copied, Skipped and Failed count files; Folders the folders made or found at
		// the target.
		Copied  int `json:"copied"`
		Skipped int `json:"skipped"`
		Failed  int `json:"failed"`
		Folders int `json:"folders"`
		// Notes are the entries the report names, with their reason; NotesLeft counts
		// those beyond copyMaxNotes.
		Notes     []CopyNote `json:"notes,omitempty"`
		NotesLeft int        `json:"notes_left,omitempty"`
		// Stopped tells why the task ended before its end: CopyNoteStopped, CopyNoteFull.
		Stopped string `json:"stopped,omitempty"`
	}

	CopyNote struct {
		Path   string `json:"path"`
		Reason string `json:"reason"`
	}
)

func init() {
	queue.RegisterResumableTaskFactory(queue.ConnectedCopyTaskType, NewConnectedCopyTaskFromModel)
}

// NewConnectedCopyTask makes a task that copies the entries at uris into the folder dst.
// The caller has checked the addresses.
func NewConnectedCopyTask(ctx context.Context, uris []string, dst string) (queue.Task, error) {
	state, err := json.Marshal(&ConnectedCopyState{Uris: uris, Dst: dst})
	if err != nil {
		return nil, err
	}
	return &ConnectedCopyTask{DBTask: &queue.DBTask{
		Task: &ent.Task{
			Type:          queue.ConnectedCopyTaskType,
			CorrelationID: logging.CorrelationID(ctx),
			PrivateState:  string(state),
			PublicState:   &types.TaskPublicState{},
		},
		DirectOwner: inventory.UserFromContext(ctx),
	}}, nil
}

func NewConnectedCopyTaskFromModel(t *ent.Task) queue.Task {
	return &ConnectedCopyTask{DBTask: &queue.DBTask{Task: t}}
}

// Halt stops a running task. What is copied stays; the file under way is dropped.
func (m *ConnectedCopyTask) Halt() {
	m.stopMu.Lock()
	defer m.stopMu.Unlock()
	m.halted = true
	if m.stop != nil {
		m.stop()
	}
}

// connectionEnded stops the task because a connection it uses ended.
func (m *ConnectedCopyTask) connectionEnded() {
	m.stopMu.Lock()
	defer m.stopMu.Unlock()
	m.connEnded = true
	if m.stop != nil {
		m.stop()
	}
}

func (m *ConnectedCopyTask) note(path, reason string) {
	m.Lock()
	defer m.Unlock()
	if len(m.state.Notes) < copyMaxNotes {
		m.state.Notes = append(m.state.Notes, CopyNote{Path: path, Reason: reason})
	} else {
		m.state.NotesLeft++
	}
}

// count changes the counts of the report.
func (m *ConnectedCopyTask) count(change func(s *ConnectedCopyState)) {
	m.Lock()
	defer m.Unlock()
	change(m.state)
}

func (m *ConnectedCopyTask) Do(ctx context.Context) (task.Status, error) {
	dep := dependency.FromContext(ctx)
	m.l = dep.Logger()
	state := &ConnectedCopyState{}
	if err := json.Unmarshal([]byte(m.State()), state); err != nil {
		return task.StatusError, fmt.Errorf("failed to unmarshal state: %w", err)
	}
	// A run again (resumed) starts its report anew: what it copied before is found at
	// the target and reported as there already.
	m.Lock()
	m.state = &ConnectedCopyState{Uris: state.Uris, Dst: state.Dst}
	m.progress = queue.Progresses{
		ProgressTypeCopyCount: &queue.Progress{},
		ProgressTypeCopySize:  &queue.Progress{},
	}
	m.Unlock()

	ctx, stop := context.WithCancel(ctx)
	defer stop()
	m.stopMu.Lock()
	m.stop = stop
	halted := m.halted
	m.connEnded = false
	m.stopMu.Unlock()
	if halted {
		stop()
	}

	status, err := m.run(ctx, dep)
	m.Lock()
	if saved, merr := json.Marshal(m.state); merr == nil {
		m.Task.PrivateState = string(saved)
	}
	m.Unlock()
	return status, err
}

func (m *ConnectedCopyTask) run(ctx context.Context, dep dependency.Dep) (task.Status, error) {
	if ctx.Err() != nil {
		return m.ended(ctx)
	}
	select {
	case copySlots <- struct{}{}:
		defer func() { <-copySlots }()
	default:
		// No place free: the worker is given back, and the task tries again later.
		m.ResumeAfter(copyWaitForSlot)
		return task.StatusSuspending, nil
	}

	// The account as it is now: it may have lost the right since the task was made.
	owner := inventory.UserFromContext(ctx)
	if owner == nil {
		return task.StatusError, fmt.Errorf("the task has no owner (%w)", queue.CriticalErr)
	}
	user, err := dep.UserClient().GetLoginUserByID(ctx, owner.ID)
	if err != nil || user.Edges.Group == nil || user.Edges.Group.Permissions == nil ||
		!user.Edges.Group.Permissions.Enabled(int(types.GroupPermissionConnectedStorage)) {
		return task.StatusError, fmt.Errorf("the account may not use connected storages (%w)", queue.CriticalErr)
	}
	ctx = context.WithValue(ctx, inventory.UserCtx{}, user)

	fm := manager.NewFileManager(dep, user)
	defer fm.Recycle()
	sides := &copySides{fm: fm, cm: connected.For(dep), user: user, conns: map[string]*copyConn{},
		sourceFolders: map[string]map[string]bool{}, onEnd: m.connectionEnded}
	defer sides.close()

	dst, err := sides.target(ctx, m.state.Dst)
	if err != nil {
		if ctx.Err() != nil {
			return m.ended(ctx)
		}
		return task.StatusError, fmt.Errorf("the target cannot be used: %w (%w)", err, queue.CriticalErr)
	}

	// What to copy, at most copyMaxEntries entries.
	var items []*copyItem
	for _, raw := range m.state.Uris {
		if ctx.Err() != nil {
			return m.ended(ctx)
		}
		found, err := sides.list(ctx, raw, ConnectedCopyMaxEntries-len(items), m.note)
		if err != nil {
			if ctx.Err() != nil {
				return m.ended(ctx)
			}
			m.note(raw, copyReason(err))
			continue
		}
		items = append(items, found...)
	}
	files := 0
	var total int64
	for _, it := range items {
		if !it.dir && it.skip == "" {
			files++
			total += max(it.size, 0)
		}
	}
	m.Lock()
	m.progress[ProgressTypeCopyCount].Total = int64(files)
	m.progress[ProgressTypeCopySize].Total = total
	m.Unlock()

	// The room the account has left in Fylvo, before anything is written. Sources whose
	// name is free at the target are new there and count whole; the others may be a
	// run again of this copy, and their files are found as there already, or not.
	if room, ok := dst.room(ctx); ok {
		var needed int64
		fresh := map[string]bool{}
		for _, it := range items {
			if it.parent == "" {
				there, err := dst.look(ctx, "", it.name)
				fresh[it.rel] = err == nil && there == nil
			}
			top, _, _ := strings.Cut(it.rel, "/")
			if fresh[top] && !it.dir && it.skip == "" {
				needed += max(it.size, 0)
			}
		}
		if needed > room {
			m.count(func(s *ConnectedCopyState) { s.Stopped = CopyNoteFull })
			m.note(m.state.Dst, CopyNoteFull)
			return task.StatusError, fmt.Errorf("not enough room at the target (%w)", queue.CriticalErr)
		}
	}

	// The folders of the sources at the target, by their path below the sources.
	folders := map[string]string{"": ""}
	// written are the names this run wrote, per folder of the target: a file there of
	// the same name was not there before, so it is never "already there".
	written := map[string]bool{}
	for _, it := range items {
		if ctx.Err() != nil {
			return m.ended(ctx)
		}
		parent, ok := folders[it.parent]
		if !ok {
			// Its folder could not be made at the target.
			if !it.dir {
				m.count(func(s *ConnectedCopyState) { s.Failed++ })
			}
			m.note(it.show, CopyNoteFailed)
			continue
		}
		if it.skip != "" {
			if !it.dir {
				m.count(func(s *ConnectedCopyState) { s.Skipped++ })
			}
			m.note(it.show, it.skip)
			continue
		}
		if it.dir {
			made, err := dst.folder(ctx, parent, it.name)
			if err != nil {
				if ctx.Err() != nil {
					return m.ended(ctx)
				}
				m.l.Warning("Connected copy: the folder %s could not be made: %s", it.show, err)
				m.note(it.show, copyReason(err))
				if copyReason(err) == CopyNoteFull {
					m.count(func(s *ConnectedCopyState) { s.Stopped = CopyNoteFull })
					return task.StatusError, fmt.Errorf("the target is full (%w)", queue.CriticalErr)
				}
				continue
			}
			folders[it.rel] = made
			m.count(func(s *ConnectedCopyState) { s.Folders++ })
			continue
		}
		reason, err := m.copyFile(ctx, sides, dst, parent, it, written)
		count := m.progress[ProgressTypeCopyCount]
		atomic.AddInt64(&count.Current, 1)
		switch {
		case ctx.Err() != nil:
			return m.ended(ctx)
		case err != nil:
			m.count(func(s *ConnectedCopyState) { s.Failed++ })
			m.note(it.show, copyReason(err))
			m.l.Warning("Connected copy: %s could not be copied: %s", it.show, err)
			if copyReason(err) == CopyNoteFull {
				// No room for anything more.
				m.count(func(s *ConnectedCopyState) { s.Stopped = CopyNoteFull })
				return task.StatusError, fmt.Errorf("the target is full (%w)", queue.CriticalErr)
			}
		case reason == CopyNoteNoName:
			m.count(func(s *ConnectedCopyState) { s.Failed++ })
			m.note(it.show, reason)
		case reason != "":
			m.count(func(s *ConnectedCopyState) { s.Skipped++ })
			m.note(it.show, reason)
		default:
			m.count(func(s *ConnectedCopyState) { s.Copied++ })
		}
	}
	return task.StatusCompleted, nil
}

// ended answers for a task that was stopped: by its stop button, because a connection
// it uses ended, or because the server ends. In the last case it is resumed when the
// server runs again (and passes over what is copied).
func (m *ConnectedCopyTask) ended(ctx context.Context) (task.Status, error) {
	m.stopMu.Lock()
	halted, connEnded := m.halted, m.connEnded
	m.stopMu.Unlock()
	switch {
	case halted:
		m.count(func(s *ConnectedCopyState) { s.Stopped = CopyNoteStopped })
		return task.StatusCanceled, nil
	case connEnded:
		m.count(func(s *ConnectedCopyState) { s.Stopped = CopyNoteStopped })
		return task.StatusError, fmt.Errorf("a connected storage of the copy was disconnected (%w)", queue.CriticalErr)
	}
	m.ResumeAfter(0)
	return task.StatusSuspending, nil
}

// copyFile copies one file into the folder parent of the target. It answers a reason
// when it passed the file over. The target is looked at before the source is opened.
func (m *ConnectedCopyTask) copyFile(ctx context.Context, sides *copySides, dst copyTarget, parent string, it *copyItem, written map[string]bool) (string, error) {
	size, sum := it.size, it.sum
	var (
		spooled *copySpool
		err     error
	)
	if it.export != "" {
		// A document of the provider: its length is known only once it is exported.
		if spooled, err = sides.export(ctx, it); err != nil {
			return "", err
		}
		defer spooled.drop()
		size = spooled.size
	}
	sizeProgress := m.progress[ProgressTypeCopySize]

	for n := 1; n <= copyMaxNumbers; n++ {
		name := connected.NumberedName(it.name, n)
		key := parent + "/" + connected.Fold(name)
		there, err := dst.look(ctx, parent, name)
		if err != nil {
			return "", err
		}
		if there != nil {
			if !written[key] && !there.dir && there.size == size && !(sum.value != "" && there.sum.value != "" &&
				sum.kind == there.sum.kind && sum.value != there.sum.value) {
				return CopyNoteSame, nil
			}
			continue
		}
		var (
			body      io.Reader
			closeBody = func() {}
		)
		if spooled != nil {
			if _, err := spooled.file.Seek(0, io.SeekStart); err != nil {
				return "", err
			}
			body = spooled.file
		} else {
			body, size, closeBody, err = sides.open(ctx, it)
			if err != nil {
				return "", err
			}
		}
		err = dst.write(ctx, parent, name, size, &countingReader{r: body, n: &sizeProgress.Current})
		closeBody()
		if errors.Is(err, errTargetTaken) {
			// Taken in the moment it was written: the next name, with the bytes read anew.
			continue
		}
		if err == nil {
			written[key] = true
		}
		return "", err
	}
	return CopyNoteNoName, nil
}

// copyReason is the reason in the report for an error.
func copyReason(err error) string {
	switch {
	case errors.Is(err, connected.ErrCopyChanged):
		return CopyNoteChanged
	case errors.Is(err, errExportTooLarge):
		return CopyNoteTooLarge
	case errors.Is(err, context.Canceled):
		return CopyNoteStopped
	}
	var app serializer.AppError
	if !errors.As(err, &app) {
		// An error of a provider, in the words of the API.
		err = connected.Error(context.Background(), err)
	}
	switch {
	case errors.Is(err, fs.ErrInsufficientCapacity):
		return CopyNoteFull
	case errors.Is(err, fs.ErrFileSizeTooBig):
		return CopyNoteTooLarge
	case errors.As(err, &app) && app.Code == serializer.CodeConnectedFull:
		return CopyNoteFull
	case errors.As(err, &app) && app.Code == serializer.CodeFileTooLarge:
		return CopyNoteTooLarge
	}
	return CopyNoteFailed
}

// Progress is a copy of the progress, as it is in this moment.
func (m *ConnectedCopyTask) Progress(ctx context.Context) queue.Progresses {
	m.Lock()
	defer m.Unlock()
	out := make(queue.Progresses, len(m.progress))
	for k, p := range m.progress {
		out[k] = &queue.Progress{
			Total:      atomic.LoadInt64(&p.Total),
			Current:    atomic.LoadInt64(&p.Current),
			Identifier: p.Identifier,
		}
	}
	return out
}

func (m *ConnectedCopyTask) Summarize(hasher hashid.Encoder) *queue.Summary {
	m.Lock()
	defer m.Unlock()
	if m.state == nil {
		state := &ConnectedCopyState{}
		if err := json.Unmarshal([]byte(m.Task.PrivateState), state); err != nil {
			return nil
		}
		m.state = state
	}
	return &queue.Summary{
		Props: map[string]any{
			SummaryKeySrcMultiple: append([]string(nil), m.state.Uris...),
			SummaryKeyDst:         m.state.Dst,
			SummaryKeyConnectedCopy: map[string]any{
				"copied":     m.state.Copied,
				"skipped":    m.state.Skipped,
				"failed":     m.state.Failed,
				"folders":    m.state.Folders,
				"notes":      append([]CopyNote(nil), m.state.Notes...),
				"notes_left": m.state.NotesLeft,
				"stopped":    m.state.Stopped,
			},
		},
	}
}

type countingReader struct {
	r io.Reader
	n *int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	atomic.AddInt64(c.n, int64(n))
	return n, err
}

// --- What is copied

type (
	copyItem struct {
		// rel is the path below the sources ("Docs/letter.txt"), parent that of its
		// folder ("" for a source itself), name its name at the target, show the address
		// it has at the source, for the report.
		rel, parent, name, show string
		dir                     bool
		size                    int64
		// sum is the checksum the source told when it was listed, "" if none.
		sum copySum
		// skip is set for an entry that is reported and not copied.
		skip string

		fylvo  fs.File
		conn   *copyConn
		row    *connected.Row
		export string
	}

	copySum struct{ kind, value string }

	copyConn struct {
		c    *connected.Conn
		ctx  context.Context
		done func()
	}

	copySides struct {
		fm    manager.FileManager
		cm    *connected.Manager
		user  *ent.User
		conns map[string]*copyConn
		// sourceFolders are the ids of the folders that are copied, per connection: none
		// of them is ever taken as a folder of the target.
		sourceFolders map[string]map[string]bool
		// onEnd is called when a connection the task uses ends.
		onEnd func()
	}
)

// conn admits the task to a connection, once for its whole run. A connection that ends
// ends the task.
func (s *copySides) conn(ctx context.Context, id string) (*copyConn, error) {
	if c := s.conns[id]; c != nil {
		return c, c.ctx.Err()
	}
	c, opCtx, done, err := s.cm.Use(ctx, s.user.ID, id)
	if err != nil {
		return nil, err
	}
	stopWatch := context.AfterFunc(opCtx, func() {
		if ctx.Err() == nil && s.onEnd != nil {
			s.onEnd()
		}
	})
	cc := &copyConn{c: c, ctx: opCtx, done: func() { stopWatch(); done() }}
	s.conns[id] = cc
	return cc, nil
}

func (s *copySides) close() {
	for _, c := range s.conns {
		c.done()
	}
}

func (s *copySides) markSource(connection, folderID string) {
	if s.sourceFolders[connection] == nil {
		s.sourceFolders[connection] = map[string]bool{}
	}
	s.sourceFolders[connection][folderID] = true
}

// connectedAddress splits an address of a connected storage into its storage and path.
func connectedAddress(raw string) (id, path string, ok bool, err error) {
	uri, err := fs.NewAnyUriFromString(raw)
	if err != nil {
		return "", "", false, err
	}
	if uri.FileSystem() != constants.FileSystemConnected {
		return "", "", false, nil
	}
	path, err = connected.CheckPath(uri.U.Path)
	if err != nil {
		return "", "", true, err
	}
	if uri.U.User != nil {
		id = uri.U.User.Username()
	}
	return id, path, true, nil
}

func joinRel(parent, name string) string {
	switch {
	case parent == "":
		return name
	case name == "":
		return parent
	}
	return parent + "/" + name
}

func splitRel(rel string) (parent, name string) {
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		return rel[:i], rel[i+1:]
	}
	return "", rel
}

// list reads one source with everything below it, at most limit entries. Entries it
// cannot reach are noted.
func (s *copySides) list(ctx context.Context, raw string, limit int, note func(string, string)) ([]*copyItem, error) {
	if limit <= 0 {
		note(raw, CopyNoteNotReached)
		return nil, nil
	}
	id, path, isConnected, err := connectedAddress(raw)
	if err != nil {
		return nil, err
	}
	if isConnected {
		return s.listConnected(ctx, raw, id, path, limit, note)
	}
	return s.listFylvo(ctx, raw, limit, note)
}

func (s *copySides) listFylvo(ctx context.Context, raw string, limit int, note func(string, string)) ([]*copyItem, error) {
	uri, err := fs.NewUriFromString(raw)
	if err != nil {
		return nil, err
	}
	root, err := s.fm.Get(ctx, uri, dbfs.WithFileEntities(), dbfs.WithRequiredCapabilities(dbfs.NavigatorCapabilityDownloadFile))
	if err != nil {
		return nil, err
	}
	depth := len(root.Uri(false).Elements())
	if depth == 0 {
		// The root of a file system: no source of a copy.
		return nil, fs.ErrPathNotExist
	}
	rootName := connected.CopyName(root.DisplayName())
	if root.Type() != types.FileTypeFolder {
		return []*copyItem{{rel: rootName, name: rootName, show: raw, size: root.Size(), fylvo: root}}, nil
	}
	var items []*copyItem
	errFull := errors.New("full")
	err = s.fm.Walk(ctx, root.Uri(false), 1<<30, func(f fs.File, level int) error {
		if len(items) >= limit {
			return errFull
		}
		if f.IsSymbolic() {
			note(f.Uri(false).String(), CopyNoteLink)
			return nil
		}
		parts := append([]string(nil), f.Uri(false).Elements()[depth-1:]...)
		for i := range parts {
			parts[i] = connected.CopyName(parts[i])
		}
		rel := strings.Join(parts, "/")
		parent, name := splitRel(rel)
		items = append(items, &copyItem{rel: rel, parent: parent, name: name, show: f.Uri(false).String(),
			dir: f.Type() == types.FileTypeFolder, size: f.Size(), fylvo: f})
		return nil
	}, dbfs.WithFileEntities(), dbfs.WithRequiredCapabilities(dbfs.NavigatorCapabilityDownloadFile))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !errors.Is(err, errFull) && len(items) == 0 {
			return nil, err
		}
		// The bound of this task, or of the walk of the account's group: what was
		// listed is copied, the rest is reported as not reached.
		note(raw, CopyNoteNotReached)
	}
	return items, nil
}

func (s *copySides) listConnected(ctx context.Context, raw, id, path string, limit int, note func(string, string)) ([]*copyItem, error) {
	cc, err := s.conn(ctx, id)
	if err != nil {
		return nil, err
	}
	if path == "" {
		// The root of a storage: no source of a copy.
		return nil, connected.ErrUnsupported
	}
	row, err := s.cm.Tree().ResolveNow(cc.ctx, cc.c, path)
	if err != nil {
		return nil, err
	}
	if row.ViaLink || row.Link != nil {
		// A shortcut, or something reached through one.
		note(raw, CopyNoteLink)
		return nil, nil
	}
	rootName := connected.CopyName(row.Name)
	item := func(r *connected.Row, rel string) *copyItem {
		parent, name := splitRel(rel)
		it := &copyItem{rel: rel, parent: parent, name: name, show: connectedShow(id, r.Path),
			dir: r.IsDir, size: r.Size, sum: copySum{kind: cc.c.Provider, value: r.Hash}, conn: cc, row: r}
		switch {
		case r.Unsupported != "":
			it.skip = CopyNoteDuplicate
		case r.Link != nil:
			it.skip = CopyNoteLink
		case r.Native != "":
			formats := connected.ExportFormats[r.Native]
			if len(formats) == 0 {
				it.skip = CopyNoteNoExport
			} else {
				it.export = formats[0]
				it.name = connected.CopyName(connected.ExportName(r.Name, r.Native, it.export))
				it.rel = joinRel(parent, it.name)
				it.size, it.sum = -1, copySum{}
			}
		case r.IsDir:
			s.markSource(cc.c.ID, r.ContentID())
		}
		return it
	}
	if !row.IsDir {
		row.Path = path
		return []*copyItem{item(row, rootName)}, nil
	}
	s.markSource(cc.c.ID, row.ContentID())
	page, err := s.cm.Tree().WalkWithin(cc.ctx, cc.c, path, connected.Scan{}, connected.Order{By: "name"}, limit+1, copyWalkTime)
	if err != nil {
		return nil, err
	}
	items := []*copyItem{{rel: rootName, name: rootName, show: raw, dir: true, conn: cc, row: row}}
	rows := page.Rows
	// Folders before what is in them: a row's folder is always shorter in path.
	depth := func(r *connected.Row) int { return strings.Count(r.Path, "/") }
	sort.SliceStable(rows, func(i, j int) bool { return depth(rows[i]) < depth(rows[j]) })
	for _, r := range rows {
		if len(items) >= limit {
			break
		}
		sub := strings.TrimPrefix(strings.TrimPrefix(r.Path, path), "/")
		parts := strings.Split(sub, "/")
		for i := range parts {
			parts[i] = connected.CopyName(parts[i])
		}
		items = append(items, item(r, rootName+"/"+strings.Join(parts, "/")))
	}
	if len(rows)+1 > limit || page.Missed {
		note(raw, CopyNoteNotReached)
	}
	return items, nil
}

func connectedShow(id, path string) string {
	uri, err := fs.NewAnyUriFromString(connected.RootURI(id))
	if err != nil || path == "" {
		return connected.RootURI(id)
	}
	return uri.Join(strings.Split(path, "/")...).String()
}

var errExportTooLarge = errors.New("the exported document is too large")

// open opens the bytes of a file to copy, with their size.
func (s *copySides) open(ctx context.Context, it *copyItem) (io.Reader, int64, func(), error) {
	if it.fylvo != nil {
		if it.fylvo.Size() == 0 && it.fylvo.PrimaryEntityID() == 0 {
			// An empty file that never had content.
			return bytes.NewReader(nil), 0, func() {}, nil
		}
		es, err := s.fm.GetEntitySource(ctx, it.fylvo.PrimaryEntityID())
		if err != nil {
			return nil, 0, nil, err
		}
		return es, it.fylvo.Size(), func() { _ = es.Close() }, nil
	}
	cc := it.conn
	if err := cc.ctx.Err(); err != nil {
		return nil, 0, nil, err
	}
	r, err := s.cm.OpenCopy(cc.ctx, cc.c, it.row)
	if err != nil {
		return nil, 0, nil, err
	}
	if r.Size() == 0 {
		// Nothing to read, but the file is checked all the same.
		if _, err := io.Copy(io.Discard, r); err != nil {
			_ = r.Close()
			return nil, 0, nil, err
		}
		_ = r.Close()
		return bytes.NewReader(nil), 0, func() {}, nil
	}
	return r, r.Size(), func() { _ = r.Close() }, nil
}

type copySpool struct {
	file *os.File
	size int64
}

func (s *copySpool) drop() {
	_ = s.file.Close()
	_ = os.Remove(s.file.Name())
}

// export takes a document of the provider in whole into the installation's temporary
// folder, checked against the version it was exported in.
func (s *copySides) export(ctx context.Context, it *copyItem) (*copySpool, error) {
	cc := it.conn
	if err := cc.ctx.Err(); err != nil {
		return nil, err
	}
	body, err := s.cm.OpenExport(cc.ctx, cc.c, it.row, it.export)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	dir := util.DataPath(dependency.FromContext(ctx).SettingProvider().TempPath(ctx))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(dir, "fylvo-copy-")
	if err != nil {
		return nil, err
	}
	spool := &copySpool{file: tmp}
	n, err := io.Copy(tmp, io.LimitReader(body, copyMaxExport+1))
	if err == nil && n > copyMaxExport {
		err = errExportTooLarge
	}
	if err != nil {
		spool.drop()
		return nil, err
	}
	spool.size = n
	return spool, nil
}

// --- Where it is copied to

var errTargetTaken = errors.New("the name was taken in the moment it was written")

type (
	copyTarget interface {
		// folder makes the folder name in the folder parent (paths below the target),
		// or takes the one there, and returns its path.
		folder(ctx context.Context, parent, name string) (string, error)
		// look tells what is at name in the folder parent; nil if nothing.
		look(ctx context.Context, parent, name string) (*copyThere, error)
		// write stores a new file; errTargetTaken if the name is taken.
		write(ctx context.Context, parent, name string, size int64, body io.Reader) error
		// room is how much the target takes yet, if it says.
		room(ctx context.Context) (int64, bool)
	}

	copyThere struct {
		dir  bool
		size int64
		sum  copySum
	}
)

func (s *copySides) target(ctx context.Context, raw string) (copyTarget, error) {
	id, path, isConnected, err := connectedAddress(raw)
	if err != nil {
		return nil, err
	}
	if isConnected {
		if !connected.Writes() {
			return nil, connected.ErrUnsupported
		}
		cc, err := s.conn(ctx, id)
		if err != nil {
			return nil, err
		}
		row, err := s.cm.Tree().ResolveNow(cc.ctx, cc.c, path)
		if err != nil {
			return nil, err
		}
		if !row.Enterable() || row.ViaLink || row.Link != nil {
			return nil, connected.ErrUnsupported
		}
		return &connectedTarget{s: s, cc: cc, base: path}, nil
	}
	uri, err := fs.NewUriFromString(raw)
	if err != nil {
		return nil, err
	}
	if uri.FileSystem() != constants.FileSystemMy {
		// Only into the account's own files: they count against its own room.
		return nil, fs.ErrPathNotExist
	}
	folder, err := s.fm.Get(ctx, uri, dbfs.WithRequiredCapabilities(dbfs.NavigatorCapabilityUploadFile))
	if err != nil {
		return nil, err
	}
	if folder.Type() != types.FileTypeFolder {
		return nil, fs.ErrPathNotExist
	}
	return &fylvoTarget{s: s, base: folder.Uri(false)}, nil
}

type fylvoTarget struct {
	s    *copySides
	base *fs.URI
}

func (t *fylvoTarget) at(parent, name string) *fs.URI {
	uri := t.base
	if parent != "" {
		uri = uri.Join(strings.Split(parent, "/")...)
	}
	return uri.Join(name)
}

func (t *fylvoTarget) folder(ctx context.Context, parent, name string) (string, error) {
	for n := 1; n <= copyMaxNumbers; n++ {
		candidate := connected.NumberedName(name, n)
		there, err := t.look(ctx, parent, candidate)
		if err != nil {
			return "", err
		}
		if there != nil && there.dir {
			return joinRel(parent, candidate), nil
		}
		if there != nil {
			continue
		}
		if _, err := t.s.fm.Create(ctx, t.at(parent, candidate), types.FileTypeFolder); err != nil {
			if errors.Is(err, fs.ErrFileExisted) {
				// Taken in this moment, or by another spelling of the name: the next one.
				continue
			}
			return "", err
		}
		return joinRel(parent, candidate), nil
	}
	return "", errors.New("no free name for the folder")
}

func (t *fylvoTarget) look(ctx context.Context, parent, name string) (*copyThere, error) {
	f, err := t.s.fm.Get(ctx, t.at(parent, name))
	if err != nil {
		if ent.IsNotFound(err) || errors.Is(err, fs.ErrPathNotExist) {
			return nil, nil
		}
		var app serializer.AppError
		if errors.As(err, &app) && app.Code == serializer.CodeParentNotExist {
			return nil, nil
		}
		return nil, err
	}
	return &copyThere{dir: f.Type() == types.FileTypeFolder, size: f.Size()}, nil
}

func (t *fylvoTarget) write(ctx context.Context, parent, name string, size int64, body io.Reader) error {
	_, err := t.s.fm.Update(ctx, &fs.UploadRequest{
		Props: &fs.UploadProps{Uri: t.at(parent, name), Size: size},
		File:  io.NopCloser(body),
	}, fs.WithNoEntityType())
	if errors.Is(err, fs.ErrFileExisted) {
		return errTargetTaken
	}
	return err
}

func (t *fylvoTarget) room(ctx context.Context) (int64, bool) {
	c, err := t.s.fm.Capacity(ctx)
	if err != nil || c.Total <= 0 {
		return 0, false
	}
	return max(c.Total-c.Used, 0), true
}

type connectedTarget struct {
	s    *copySides
	cc   *copyConn
	base string
}

func (t *connectedTarget) path(parent, name string) string {
	return joinRel(joinRel(t.base, parent), name)
}

func (t *connectedTarget) folder(ctx context.Context, parent, name string) (string, error) {
	sources := t.s.sourceFolders[t.cc.c.ID]
	for n := 1; n <= copyMaxNumbers; n++ {
		candidate := connected.NumberedName(name, n)
		row, err := t.s.cm.CreateEntry(t.cc.ctx, t.cc.c, t.path(parent, candidate), true, false)
		var app serializer.AppError
		if errors.As(err, &app) && (app.Code == serializer.CodeObjectExist || app.Code == serializer.CodeNameCaseTaken) {
			continue
		}
		if err != nil {
			return "", err
		}
		if !row.IsDir || sources[row.ContentID()] {
			// Not a folder, or a folder that is being copied: nothing is put into what
			// is read.
			continue
		}
		return joinRel(parent, candidate), nil
	}
	return "", errors.New("no free name for the folder")
}

func (t *connectedTarget) look(ctx context.Context, parent, name string) (*copyThere, error) {
	row, err := t.s.cm.Tree().ResolveNow(t.cc.ctx, t.cc.c, t.path(parent, name))
	switch {
	case errors.Is(err, connected.ErrNotFound):
		return nil, nil
	case errors.Is(err, connected.ErrAmbiguous):
		// Several entries of that name: taken, and none of them is the copy.
		return &copyThere{size: -2}, nil
	case err != nil:
		return nil, err
	}
	if row.Link != nil || row.Native != "" {
		return &copyThere{size: -2}, nil
	}
	return &copyThere{dir: row.IsDir, size: row.Size, sum: copySum{kind: t.cc.c.Provider, value: row.Hash}}, nil
}

func (t *connectedTarget) write(ctx context.Context, parent, name string, size int64, body io.Reader) error {
	_, err := t.s.cm.Save(t.cc.ctx, t.cc.c, connected.UploadRequest{Path: t.path(parent, name), Size: size}, body)
	var app serializer.AppError
	if errors.As(err, &app) && (app.Code == serializer.CodeObjectExist || app.Code == serializer.CodeNameCaseTaken) {
		return errTargetTaken
	}
	return err
}

func (t *connectedTarget) room(ctx context.Context) (int64, bool) { return 0, false }
