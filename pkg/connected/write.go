package connected

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
)

// Changes in a connected storage: create, rename, move, trash.
//
// A path only finds the entry. What is changed is named to the provider by its id, and
// only after the client's own id for the row (FileID, as last listed) was compared with
// what the path leads to now: a row that was replaced under its name since is refused,
// not changed. Names are checked against the folder as the provider has it at that
// moment, and nothing is ever replaced or merged. The changes of one connection run
// one after the other, so two of them cannot both find a name free.

var (
	// ErrNameTaken: an entry with this name is already in the folder.
	ErrNameTaken = serializer.NewError(serializer.CodeObjectExist, "Object existed", nil)
	// ErrCaseTaken: on a provider that ignores case, a name that differs only in case
	// is in the folder.
	ErrCaseTaken = serializer.NewError(serializer.CodeNameCaseTaken, "A name that differs only in case exists", nil)
	// ErrIllegalName: not a name an entry can get.
	ErrIllegalName = serializer.NewError(serializer.CodeIllegalObjectName, "Invalid object name", nil)
)

// maxNameLength is the longest name an entry can get, in bytes, as for files of Fylvo.
const maxNameLength = 255

// MaxBatch is how many entries one request can move or delete. Each one costs calls to
// the provider, under the lock of the account: a request that takes minutes holds up
// every other change, and its answer may find nobody waiting for it.
const MaxBatch = 100

// Source names the entry a change is about: its path and the id the client has for the
// row, from the listing it last saw.
type Source struct {
	Path string
	ID   string
}

// checkNewName applies the rules for names of Fylvo's own files to a name an entry is
// to get. Entries that exist at the provider under other names stay usable.
func checkNewName(name string) error {
	if len(name) > maxNameLength || !plainName(name) || strings.ContainsAny(name, `:*?"<>|`) {
		return ErrIllegalName
	}
	// OneDrive refuses these, and so do the file systems files end up on: a name that
	// starts with a space or ends with a space or a period.
	if strings.HasPrefix(name, " ") || strings.HasSuffix(name, " ") || strings.HasSuffix(name, ".") {
		return ErrIllegalName
	}
	return nil
}

// EnvWrite is the switch for changes in connected storages. An installation only reads
// them unless its operator sets the variable to "1": nothing is written to somebody's
// Google Drive or OneDrive because of an update alone.
const EnvWrite = "FYLVO_CONNECTED_WRITE"

// Writes tells whether this installation changes connected storages at all.
func Writes() bool {
	return os.Getenv(EnvWrite) == "1"
}

// writer returns the changing half of the provider of a connection. Every change and
// every upload gets it here, so with the switch off none of them reaches a provider.
func writer(c *Conn) (provider.Writer, error) {
	w, ok := c.P.(provider.Writer)
	if !ok || !Writes() {
		return nil, ErrUnsupported
	}
	return w, nil
}

// writing lets one change of an account at a provider run at a time: also when two
// accounts of Fylvo connected the same one, they must not both find a name free.
func (m *Manager) writing(c *Conn) (done func()) {
	lock, _ := m.writes.LoadOrStore(c.AccountKey, &sync.Mutex{})
	lock.(*sync.Mutex).Lock()
	return lock.(*sync.Mutex).Unlock
}

// unsettledFor is how long a name stays closed after a change about it whose outcome is
// open: the provider may still be carrying it out.
const unsettledFor = 2 * time.Minute

// openNames are names a change was about that the provider did not answer clearly, with
// the time each opens again. Until then nothing new gets such a name: a second request
// would find it free while the first is still on its way, and a provider that does not
// keep names unique would end up with both.
type openNames struct {
	mu    sync.Mutex
	until map[string]time.Time
}

func nameKey(account, folderID, name string) string {
	return account + "\x00" + folderID + "\x00" + Fold(name)
}

// unsettle closes a name in a folder for a while. The caller holds the lock of the
// account, or the work lock of an upload that just became open in its middle.
func (m *Manager) unsettle(account, folderID, name string) {
	now := m.uploads.now()
	m.unsettled.mu.Lock()
	defer m.unsettled.mu.Unlock()
	if m.unsettled.until == nil {
		m.unsettled.until = map[string]time.Time{}
	}
	for key, until := range m.unsettled.until {
		if !now.Before(until) {
			delete(m.unsettled.until, key)
		}
	}
	m.unsettled.until[nameKey(account, folderID, name)] = now.Add(unsettledFor)
}

// settled answers ErrBusy for a name that is closed right now.
func (m *Manager) settled(account, folderID, name string) error {
	m.unsettled.mu.Lock()
	defer m.unsettled.mu.Unlock()
	if until, closed := m.unsettled.until[nameKey(account, folderID, name)]; closed && m.uploads.now().Before(until) {
		return ErrBusy
	}
	return nil
}

// unclear tells whether a failed change may have been carried out all the same, or may
// still be: the answer was lost, time ran out, or the provider failed while it worked.
// A clear "no" of the provider is not unclear.
func unclear(err error) bool {
	if err == nil || !provider.Sent(err) {
		// Also not when the call never left: without a token there was no request.
		return false
	}
	var e *provider.Error
	if !errors.As(err, &e) {
		return true
	}
	if e.Status >= 400 && e.Status < 500 {
		return false
	}
	return e.Kind == provider.KindOther || e.Kind == provider.KindBusy || e.Kind == provider.KindOpen
}

// folder resolves the folder something is to be put into.
func (m *Manager) folder(ctx context.Context, c *Conn, path string) (*Row, error) {
	row, err := m.tree.ResolveNow(ctx, c, path)
	if err != nil {
		return nil, err
	}
	if row.ViaLink {
		// A link, or reached through one: read-only all the way down, whether it can
		// be entered or not.
		return nil, ErrUnsupported
	}
	if !row.Enterable() {
		return nil, ErrNotFound
	}
	return row, nil
}

// entry resolves the entry a change is about and makes sure it is the one the client
// means and one that can be changed.
func (m *Manager) entry(ctx context.Context, c *Conn, src Source) (row, parent *Row, err error) {
	if src.Path == "" {
		// The root of a storage is not an entry of anything.
		return nil, nil, ErrUnsupported
	}
	// From the provider, not from a listing in memory: the entry at the path now is
	// the one that is compared, also when another one was there seconds ago. Its
	// folder is found once and the entry looked up in it by the folder's id, so
	// everything that follows is about that one folder, whatever becomes of the path.
	parentPath, name := ParentOf(src.Path)
	parent, err = m.tree.ResolveNow(ctx, c, parentPath)
	if err != nil {
		return nil, nil, err
	}
	if !parent.Enterable() {
		return nil, nil, ErrNotFound
	}
	row, err = m.tree.named(ctx, c, parent.ContentID(), name)
	if err != nil {
		return nil, nil, err
	}
	row.Path, row.ViaLink = joinPath(parentPath, row.Name), parent.ViaLink || row.Link != nil
	if src.ID == "" || FileID(c.ID, row) != src.ID {
		return nil, nil, ItemError(ReasonChanged)
	}
	if row.ReadOnly() {
		return nil, nil, ErrUnsupported
	}
	return row, parent, nil
}

// nameFree tells whether an entry can get name in the folder with the given id, asking
// the provider now. self is the id of the entry that is being renamed, which may keep
// its own name in another spelling.
func (m *Manager) nameFree(ctx context.Context, c *Conn, folderID, name, self string) error {
	row, err := m.tree.named(ctx, c, folderID, name)
	switch {
	case errors.Is(err, ErrNotFound):
		return m.settled(c.AccountKey, folderID, name)
	case errors.Is(err, ErrAmbiguous):
		return ErrNameTaken
	case err != nil:
		return err
	case self != "" && row.ID == self:
		// Its own name in another spelling. Not while a change about that name may
		// still be on its way.
		return m.settled(c.AccountKey, folderID, name)
	case row.Name != name:
		return ErrCaseTaken
	}
	return ErrNameTaken
}

// rowOf is the row of an entry the provider just answered a change with.
func rowOf(item *provider.Item, path string) *Row {
	_, name := ParentOf(path)
	row := &Row{Item: *item, Path: path}
	row.Name = name
	return row
}

// CreateDocument makes a file with the given content at path: a new document that is
// not empty from its first moment (see BlankDocument). Like CreateEntry it never
// replaces anything: a file that is already there under the name is returned as it is,
// unless errOnConflict asks for an error.
func (m *Manager) CreateDocument(ctx context.Context, c *Conn, path string, content []byte, errOnConflict bool) (*Row, error) {
	// What is asked first for an empty file is asked first here too, so both answer
	// the same to a request that cannot be: changes switched off, then the name.
	if _, err := writer(c); err != nil {
		return nil, err
	}
	_, name := ParentOf(path)
	if path == "" {
		return nil, ErrNameTaken
	}
	if err := checkNewName(name); err != nil {
		return nil, err
	}
	row, err := m.Save(ctx, c, UploadRequest{Path: path, Size: int64(len(content))}, bytes.NewReader(content))
	var app serializer.AppError
	if err != nil && !errOnConflict && errors.As(err, &app) && app.Code == serializer.CodeObjectExist {
		// The name is taken. What is there decides, as for an empty file.
		return m.CreateEntry(ctx, c, path, false, false)
	}
	return row, err
}

// CreateEntry makes a folder or an empty file at path. An entry that is already there
// under the name is returned as it is when it is of the same kind, unless errOnConflict
// asks for an error; an entry of the other kind, or one that differs in case only, is
// always an error.
func (m *Manager) CreateEntry(ctx context.Context, c *Conn, path string, dir, errOnConflict bool) (*Row, error) {
	w, err := writer(c)
	if err != nil {
		return nil, err
	}
	parentPath, name := ParentOf(path)
	if path == "" {
		return nil, ErrNameTaken
	}
	if err := checkNewName(name); err != nil {
		return nil, err
	}
	defer m.writing(c)()

	parent, err := m.folder(ctx, c, parentPath)
	if err != nil {
		return nil, err
	}
	existing, err := m.tree.named(ctx, c, parent.ContentID(), name)
	switch {
	case errors.Is(err, ErrNotFound):
		if err := m.settled(c.AccountKey, parent.ContentID(), name); err != nil {
			return nil, err
		}
	case errors.Is(err, ErrAmbiguous):
		return nil, ErrNameTaken
	case err != nil:
		return nil, err
	case existing.Name != name:
		return nil, ErrCaseTaken
	case errOnConflict || existing.IsDir != dir || existing.Native != "" || existing.Link != nil:
		// Also a link to a folder: it is not a folder anything can be put into.
		return nil, ErrNameTaken
	default:
		existing.Path, existing.ViaLink = path, existing.Link != nil
		return existing, nil
	}

	var item *provider.Item
	if dir {
		item, err = w.CreateFolder(ctx, parent.ContentID(), name)
	} else {
		item, err = w.CreateEmpty(ctx, parent.ContentID(), name)
	}
	m.tree.Changed(c, parent.ContentID())
	if err != nil {
		if unclear(err) {
			m.unsettle(c.AccountKey, parent.ContentID(), name)
		}
		return nil, err
	}
	return rowOf(item, path), nil
}

// RenameEntry gives an entry a new name in its folder.
func (m *Manager) RenameEntry(ctx context.Context, c *Conn, src Source, newName string) (*Row, error) {
	w, err := writer(c)
	if err != nil {
		return nil, err
	}
	if err := checkNewName(newName); err != nil {
		return nil, err
	}
	defer m.writing(c)()

	row, parent, err := m.entry(ctx, c, src)
	if err != nil {
		return nil, err
	}
	if row.Name == newName {
		return row, nil
	}
	parentPath, _ := ParentOf(src.Path)
	if err := m.nameFree(ctx, c, parent.ContentID(), newName, row.ID); err != nil {
		return nil, err
	}

	item, err := w.Update(ctx, row.ID, provider.Change{Name: newName})
	m.tree.Changed(c, parent.ContentID())
	if err != nil {
		if unclear(err) {
			m.unsettle(c.AccountKey, parent.ContentID(), newName)
		}
		return nil, err
	}
	return rowOf(item, joinPath(parentPath, newName)), nil
}

// MoveEntries moves entries into another folder of the same storage. It returns one
// result per source, in their order: nil for an entry that was moved, the error for one
// that was not. dst.ID may be empty; given, it has to be the folder the path leads to.
func (m *Manager) MoveEntries(ctx context.Context, c *Conn, sources []Source, dst Source) ([]error, error) {
	w, err := writer(c)
	if err != nil {
		return nil, err
	}
	defer m.writing(c)()

	target, err := m.folder(ctx, c, dst.Path)
	if err != nil {
		return nil, err
	}
	if dst.ID != "" && FileID(c.ID, target) != dst.ID {
		return nil, ItemError(ReasonChanged)
	}

	results := make([]error, len(sources))
	for i, src := range sources {
		results[i] = m.move(ctx, c, w, src, dst.Path, target)
	}
	m.tree.Changed(c, target.ContentID())
	return results, nil
}

func (m *Manager) move(ctx context.Context, c *Conn, w provider.Writer, src Source, dstPath string, target *Row) error {
	row, parent, err := m.entry(ctx, c, src)
	if err != nil {
		return err
	}
	if parent.ContentID() == target.ContentID() {
		// It is there already, however the two paths are spelled.
		return nil
	}
	below := IsBelow(dstPath, src.Path)
	if c.P.CaseInsensitive() {
		below = IsBelow(Fold(dstPath), Fold(src.Path))
	}
	if row.IsDir && below {
		return serializer.NewError(serializer.CodeParamErr, "A folder cannot be moved into itself", nil)
	}
	// Never onto a name that is taken: nothing is replaced, no folders are merged.
	if err := m.nameFree(ctx, c, target.ContentID(), row.Name, ""); err != nil {
		return err
	}
	_, err = w.Update(ctx, row.ID, provider.Change{Parent: target.ContentID(), OldParent: parent.ContentID()})
	m.tree.Changed(c, parent.ContentID())
	if unclear(err) {
		m.unsettle(c.AccountKey, target.ContentID(), row.Name)
	}
	return err
}

// TrashEntries moves entries into the trash of the provider. It returns one result per
// source, in their order: nil for an entry that was trashed, the error for one that was not.
func (m *Manager) TrashEntries(ctx context.Context, c *Conn, sources []Source) ([]error, error) {
	w, err := writer(c)
	if err != nil {
		return nil, err
	}
	defer m.writing(c)()

	results := make([]error, len(sources))
	for i, src := range sources {
		row, parent, err := m.entry(ctx, c, src)
		if err == nil {
			err = w.Trash(ctx, row.ID)
			m.tree.Changed(c, parent.ContentID())
		}
		results[i] = err
	}
	return results, nil
}
