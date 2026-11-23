package connected

import (
	"context"
	"errors"
	"strings"
	"sync"

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

// writer returns the changing half of the provider of a connection.
func writer(c *Conn) (provider.Writer, error) {
	w, ok := c.P.(provider.Writer)
	if !ok {
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

// folder resolves the folder something is to be put into.
func (m *Manager) folder(ctx context.Context, c *Conn, path string) (*Row, error) {
	row, err := m.tree.ResolveNow(ctx, c, path)
	if err != nil {
		return nil, err
	}
	if !row.Enterable() {
		return nil, ErrNotFound
	}
	if row.ViaLink {
		// Reached through a link: read-only all the way down.
		return nil, ErrUnsupported
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
		return nil
	case errors.Is(err, ErrAmbiguous):
		return ErrNameTaken
	case err != nil:
		return err
	case self != "" && row.ID == self:
		return nil
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
	case errors.Is(err, ErrAmbiguous):
		return nil, ErrNameTaken
	case err != nil:
		return nil, err
	case existing.Name != name:
		return nil, ErrCaseTaken
	case errOnConflict || existing.IsDir != dir || existing.Native != "":
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
		below = IsBelow(strings.ToLower(dstPath), strings.ToLower(src.Path))
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
