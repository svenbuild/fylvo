package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"

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
// of the same size whose checksum (where both sides tell one) does not say otherwise; else
// the copy gets "name (2)". So a task that was stopped can simply run again: what it
// copied before is found and passed over. That is what happens after a restart of the
// server as well.

const (
	// copyMaxEntries is the most entries (files and folders) one task copies.
	copyMaxEntries = 20000
	// copyMaxNumbers is how many numbered names are tried for a copy whose name is taken.
	copyMaxNumbers = 100
	// copyMaxNotes is how many entries the report names; the rest are counted.
	copyMaxNotes = 100
	// copyMaxExport is the most an exported document may have.
	copyMaxExport = 100 << 20

	ProgressTypeCopyCount = "copy_count"
	ProgressTypeCopySize  = "copy_size"

	SummaryKeyConnectedCopy = "connected_copy"
)

// Reasons in the report of a copy.
const (
	CopyNoteSame       = "same"        // a file of that name and size is there already
	CopyNoteLink       = "link"        // a shortcut: not followed
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

// copySlots is how many copy tasks run at once on the server.
var copySlots = make(chan struct{}, 2)

type (
	ConnectedCopyTask struct {
		*queue.DBTask

		l        logging.Logger
		state    *ConnectedCopyState
		progress queue.Progresses

		stopMu sync.Mutex
		stop   context.CancelFunc
		halted bool
	}

	ConnectedCopyState struct {
		Uris []string `json:"uris"`
		Dst  string   `json:"dst"`
		// Copied, Skipped and Failed count files; Folders the folders made at the target.
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

func (m *ConnectedCopyTask) note(path, reason string) {
	m.Lock()
	defer m.Unlock()
	if len(m.state.Notes) < copyMaxNotes {
		m.state.Notes = append(m.state.Notes, CopyNote{Path: path, Reason: reason})
	} else {
		m.state.NotesLeft++
	}
}

func (m *ConnectedCopyTask) Do(ctx context.Context) (task.Status, error) {
	dep := dependency.FromContext(ctx)
	m.l = dep.Logger()
	state := &ConnectedCopyState{}
	if err := json.Unmarshal([]byte(m.State()), state); err != nil {
		return task.StatusError, fmt.Errorf("failed to unmarshal state: %w", err)
	}
	// A run again (after a restart) starts its report anew: what it copied before is
	// found at the target and reported as there already.
	m.state = &ConnectedCopyState{Uris: state.Uris, Dst: state.Dst}
	m.Lock()
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
	m.stopMu.Unlock()
	if halted {
		stop()
	}

	status, err := m.run(ctx, dep)
	if saved, merr := json.Marshal(m.state); merr == nil {
		m.Task.PrivateState = string(saved)
	}
	return status, err
}

func (m *ConnectedCopyTask) run(ctx context.Context, dep dependency.Dep) (task.Status, error) {
	select {
	case copySlots <- struct{}{}:
		defer func() { <-copySlots }()
	case <-ctx.Done():
		return m.ended(ctx)
	}
	if ctx.Err() != nil {
		return m.ended(ctx)
	}

	user := inventory.UserFromContext(ctx)
	fm := manager.NewFileManager(dep, user)
	defer fm.Recycle()
	cm := connected.For(dep)
	sides := &copySides{fm: fm, cm: cm, user: user, conns: map[string]*copyConn{}}
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
			m.note(raw, copyReason(err))
			continue
		}
		items = append(items, found...)
	}
	var total int64
	files := 0
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

	// Fylvo's own room, before anything is written.
	if room, ok := dst.room(ctx); ok && total > room {
		m.state.Stopped = CopyNoteFull
		m.note(m.state.Dst, CopyNoteFull)
		return task.StatusError, fmt.Errorf("not enough room at the target (%w)", queue.CriticalErr)
	}

	// The folders of the sources at the target, by their path below the sources.
	folders := map[string]string{"": ""}
	for _, it := range items {
		if ctx.Err() != nil {
			return m.ended(ctx)
		}
		parent, ok := folders[it.parent]
		if !ok {
			// Its folder could not be made at the target.
			m.state.Failed++
			m.note(it.show, CopyNoteFailed)
			continue
		}
		if it.skip != "" {
			if !it.dir {
				m.state.Skipped++
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
					m.state.Stopped = CopyNoteFull
					return task.StatusError, fmt.Errorf("the target is full (%w)", queue.CriticalErr)
				}
				continue
			}
			folders[it.rel] = made
			m.state.Folders++
			continue
		}
		reason, err := m.copyFile(ctx, sides, dst, parent, it)
		count := m.progress[ProgressTypeCopyCount]
		atomic.AddInt64(&count.Current, 1)
		switch {
		case ctx.Err() != nil:
			return m.ended(ctx)
		case err != nil:
			m.state.Failed++
			m.note(it.show, copyReason(err))
			m.l.Warning("Connected copy: %s could not be copied: %s", it.show, err)
			if errors.Is(err, fs.ErrInsufficientCapacity) || copyReason(err) == CopyNoteFull {
				// No room for anything more.
				m.state.Stopped = CopyNoteFull
				return task.StatusError, fmt.Errorf("the target is full (%w)", queue.CriticalErr)
			}
		case reason != "":
			m.state.Skipped++
			m.note(it.show, reason)
		default:
			m.state.Copied++
		}
	}
	return task.StatusCompleted, nil
}

// ended answers for a task that was stopped: by a cancel, or because a connection it
// uses ended.
func (m *ConnectedCopyTask) ended(ctx context.Context) (task.Status, error) {
	m.state.Stopped = CopyNoteStopped
	m.stopMu.Lock()
	halted := m.halted
	m.stopMu.Unlock()
	if halted {
		return task.StatusCanceled, nil
	}
	return task.StatusError, fmt.Errorf("the copy was stopped: %w (%w)", context.Cause(ctx), queue.CriticalErr)
}

// copyFile copies one file into the folder parent of the target. It answers a reason
// when it passed the file over.
func (m *ConnectedCopyTask) copyFile(ctx context.Context, sides *copySides, dst copyTarget, parent string, it *copyItem) (string, error) {
	body, size, sum, closeBody, err := sides.open(ctx, it)
	if err != nil {
		return "", err
	}
	defer closeBody()
	sizeProgress := m.progress[ProgressTypeCopySize]
	counted := &countingReader{r: body, n: &sizeProgress.Current}

	for n := 1; n <= copyMaxNumbers; n++ {
		name := connected.NumberedName(it.name, n)
		there, err := dst.look(ctx, parent, name)
		if err != nil {
			return "", err
		}
		if there != nil {
			if !there.dir && there.size == size && !(sum.value != "" && there.sum.value != "" &&
				sum.kind == there.sum.kind && sum.value != there.sum.value) {
				return CopyNoteSame, nil
			}
			continue
		}
		err = dst.write(ctx, parent, name, size, counted)
		if errors.Is(err, errTargetTaken) {
			// Taken in the moment it was written: the next name. The bytes are not
			// read again, so this one is not tried twice.
			if counted.read > 0 {
				return "", err
			}
			continue
		}
		return "", err
	}
	return CopyNoteNoName, nil
}

// copyReason is the reason in the report for an error.
func copyReason(err error) string {
	var app serializer.AppError
	if !errors.As(err, &app) {
		// An error of a provider, in the words of the API.
		err = connected.Error(context.Background(), err)
	}
	switch {
	case errors.Is(err, connected.ErrCopyChanged):
		return CopyNoteChanged
	case errors.Is(err, fs.ErrInsufficientCapacity):
		return CopyNoteFull
	case errors.Is(err, fs.ErrFileSizeTooBig), errors.Is(err, errExportTooLarge):
		return CopyNoteTooLarge
	case errors.Is(err, context.Canceled):
		return CopyNoteStopped
	case errors.As(err, &app) && app.Code == serializer.CodeConnectedFull:
		return CopyNoteFull
	case errors.As(err, &app) && app.Code == serializer.CodeFileTooLarge:
		return CopyNoteTooLarge
	}
	return CopyNoteFailed
}

func (m *ConnectedCopyTask) Progress(ctx context.Context) queue.Progresses {
	m.Lock()
	defer m.Unlock()
	return m.progress
}

func (m *ConnectedCopyTask) Summarize(hasher hashid.Encoder) *queue.Summary {
	if m.state == nil {
		if err := json.Unmarshal([]byte(m.State()), &m.state); err != nil {
			return nil
		}
	}
	m.Lock()
	defer m.Unlock()
	return &queue.Summary{
		Props: map[string]any{
			SummaryKeySrcMultiple: m.state.Uris,
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
	r    io.Reader
	n    *int64
	read int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.read += int64(n)
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
	}
)

// conn admits the task to a connection, once for its whole run. A connection that ends
// ends what is done with it.
func (s *copySides) conn(ctx context.Context, id string) (*copyConn, error) {
	if c := s.conns[id]; c != nil {
		return c, c.ctx.Err()
	}
	c, opCtx, done, err := s.cm.Use(ctx, s.user.ID, id)
	if err != nil {
		return nil, err
	}
	cc := &copyConn{c: c, ctx: opCtx, done: done}
	s.conns[id] = cc
	return cc, nil
}

func (s *copySides) close() {
	for _, c := range s.conns {
		c.done()
	}
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
	rootName := connected.CopyName(root.DisplayName())
	if root.Type() != types.FileTypeFolder {
		return []*copyItem{{rel: rootName, name: rootName, show: raw, size: root.Size(), fylvo: root}}, nil
	}
	depth := len(root.Uri(false).Elements())
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
		parts := f.Uri(false).Elements()[depth-1:]
		for i := range parts {
			parts[i] = connected.CopyName(parts[i])
		}
		rel := strings.Join(parts, "/")
		parent, name := "", rel
		if i := strings.LastIndex(rel, "/"); i >= 0 {
			parent, name = rel[:i], rel[i+1:]
		}
		items = append(items, &copyItem{rel: rel, parent: parent, name: name, show: f.Uri(false).String(),
			dir: f.Type() == types.FileTypeFolder, size: f.Size(), fylvo: f})
		return nil
	}, dbfs.WithFileEntities(), dbfs.WithRequiredCapabilities(dbfs.NavigatorCapabilityDownloadFile))
	if errors.Is(err, errFull) {
		note(raw, CopyNoteNotReached)
		err = nil
	}
	return items, err
}

func (s *copySides) listConnected(ctx context.Context, raw, id, path string, limit int, note func(string, string)) ([]*copyItem, error) {
	cc, err := s.conn(ctx, id)
	if err != nil {
		return nil, err
	}
	row, err := s.cm.Tree().ResolveNow(cc.ctx, cc.c, path)
	if err != nil {
		return nil, err
	}
	rootName := connected.CopyName(row.Name)
	item := func(r *connected.Row, rel string) *copyItem {
		parent, name := "", rel
		if i := strings.LastIndex(rel, "/"); i >= 0 {
			parent, name = rel[:i], rel[i+1:]
		}
		it := &copyItem{rel: rel, parent: parent, name: name, show: connectedShow(id, r.Path),
			dir: r.IsDir, size: r.Size, conn: cc, row: r}
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
				it.size = -1
			}
		}
		return it
	}
	if !row.IsDir {
		row.Path = path
		return []*copyItem{item(row, rootName)}, nil
	}
	if row.Link != nil {
		note(raw, CopyNoteLink)
		return nil, nil
	}
	page, err := s.cm.Tree().Walk(cc.ctx, cc.c, path, connected.Scan{}, connected.Order{By: "name"}, limit+1, "")
	if err != nil {
		return nil, err
	}
	items := []*copyItem{{rel: rootName, name: rootName, show: raw, dir: true, conn: cc, row: row}}
	rows := page.Rows
	// Folders before what is in them: a row's folder is always shorter in path.
	sortByDepth(rows)
	for _, r := range rows {
		if len(items) >= limit {
			break
		}
		sub := strings.TrimPrefix(r.Path, path)
		sub = strings.TrimPrefix(sub, "/")
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

func sortByDepth(rows []*connected.Row) {
	depth := func(r *connected.Row) int { return strings.Count(r.Path, "/") }
	// A stable insertion by depth keeps the walk's order within a depth.
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && depth(rows[j]) < depth(rows[j-1]); j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
}

func connectedShow(id, path string) string {
	uri, err := fs.NewAnyUriFromString(connected.RootURI(id))
	if err != nil || path == "" {
		return connected.RootURI(id)
	}
	return uri.Join(strings.Split(path, "/")...).String()
}

var errExportTooLarge = errors.New("the exported document is too large")

// open opens the bytes of a file to copy, with their size and the checksum the source
// tells for them.
func (s *copySides) open(ctx context.Context, it *copyItem) (io.Reader, int64, copySum, func(), error) {
	if it.fylvo != nil {
		es, err := s.fm.GetEntitySource(ctx, it.fylvo.PrimaryEntityID())
		if err != nil {
			return nil, 0, copySum{}, nil, err
		}
		return es, it.fylvo.Size(), copySum{}, func() { _ = es.Close() }, nil
	}
	cc := it.conn
	if err := cc.ctx.Err(); err != nil {
		return nil, 0, copySum{}, nil, err
	}
	if it.export != "" {
		// A document of the provider: its length is known only once it is exported. It
		// is taken in whole first, into the installation's temporary folder.
		body, err := s.cm.OpenExport(cc.ctx, cc.c, it.row, it.export)
		if err != nil {
			return nil, 0, copySum{}, nil, err
		}
		defer body.Close()
		dir := util.DataPath(dependency.FromContext(ctx).SettingProvider().TempPath(ctx))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, 0, copySum{}, nil, err
		}
		tmp, err := os.CreateTemp(dir, "fylvo-copy-")
		if err != nil {
			return nil, 0, copySum{}, nil, err
		}
		drop := func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }
		n, err := io.Copy(tmp, io.LimitReader(body, copyMaxExport+1))
		if err == nil && n > copyMaxExport {
			err = errExportTooLarge
		}
		if err == nil {
			_, err = tmp.Seek(0, io.SeekStart)
		}
		if err != nil {
			drop()
			return nil, 0, copySum{}, nil, err
		}
		return tmp, n, copySum{}, drop, nil
	}
	r, err := s.cm.OpenCopy(cc.ctx, cc.c, it.row)
	if err != nil {
		return nil, 0, copySum{}, nil, err
	}
	return r, r.Size(), copySum{kind: cc.c.Provider, value: r.Hash()}, func() { _ = r.Close() }, nil
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
		if !row.IsDir {
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
