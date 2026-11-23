package explorer

import (
	"context"
	"encoding/json"
	"io"
	"sort"
	"strings"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/task"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/manager"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/workflows"
	"github.com/cloudreve/Cloudreve/v4/pkg/queue"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/stretchr/testify/require"
)

// Fylvo: "Copy to" between Fylvo and connected storages.

// fylvoPut stores a file of u's own at path ("Docs/a.txt").
func (e *connectedEnv) fylvoPut(u *ent.User, path string, content []byte) {
	e.t.Helper()
	fm := manager.NewFileManager(e.dep, u)
	defer fm.Recycle()
	uri, err := fs.NewUriFromString("cloudreve://my/" + path)
	require.NoError(e.t, err)
	_, err = fm.Update(e.as(u), &fs.UploadRequest{
		Props: &fs.UploadProps{Uri: uri, Size: int64(len(content))},
		File:  io.NopCloser(strings.NewReader(string(content))),
	})
	require.NoError(e.t, err)
}

func (e *connectedEnv) fylvoMkdir(u *ent.User, path string) {
	e.t.Helper()
	fm := manager.NewFileManager(e.dep, u)
	defer fm.Recycle()
	uri, err := fs.NewUriFromString("cloudreve://my/" + path)
	require.NoError(e.t, err)
	_, err = fm.Create(e.as(u), uri, types.FileTypeFolder)
	require.NoError(e.t, err)
}

// fylvoTree lists the files of u below path, with their content, and the folders with a
// slash at the end.
func (e *connectedEnv) fylvoTree(u *ent.User, path string) map[string]string {
	e.t.Helper()
	fm := manager.NewFileManager(e.dep, u)
	defer fm.Recycle()
	uri, err := fs.NewUriFromString("cloudreve://my/" + path)
	require.NoError(e.t, err)
	root := len(uri.Elements())
	out := map[string]string{}
	err = fm.Walk(e.as(u), uri, 100, func(f fs.File, level int) error {
		rel := strings.Join(f.Uri(false).Elements()[root:], "/")
		if rel == "" {
			return nil
		}
		if f.Type() == types.FileTypeFolder {
			out[rel+"/"] = ""
			return nil
		}
		es, err := fm.GetEntitySource(e.as(u), f.PrimaryEntityID())
		require.NoError(e.t, err)
		defer es.Close()
		data, err := io.ReadAll(es)
		require.NoError(e.t, err)
		out[rel] = string(data)
		return nil
	})
	require.NoError(e.t, err)
	return out
}

// driveTree lists what is below a folder of the connected storage, as fylvoTree does.
func (e *connectedEnv) driveTree(path string) map[string]string {
	e.t.Helper()
	e.m.Tree().Forget(e.id)
	out := map[string]string{}
	var walk func(rel string)
	walk = func(rel string) {
		uri := e.root
		if rel != "" {
			uri += "/" + rel
		}
		res, err := e.list(e.alex, uri, 0, "")
		require.NoError(e.t, err)
		for _, f := range res.Files {
			child := f.Name
			if rel != "" {
				child = rel + "/" + f.Name
			}
			if f.Type == int(types.FileTypeFolder) {
				out[strings.TrimPrefix(strings.TrimPrefix(child, path), "/")+"/"] = ""
				if _, link := f.Metadata[connectedMetaLink]; !link {
					walk(child)
				}
				continue
			}
			out[strings.TrimPrefix(strings.TrimPrefix(child, path), "/")] = string(e.download(e.root + "/" + child))
		}
	}
	walk(path)
	return out
}

// runCopy runs a copy as the task would, and returns its end and its report.
func (e *connectedEnv) runCopy(u *ent.User, src []string, dst string, before func(*workflows.ConnectedCopyTask)) (task.Status, *workflows.ConnectedCopyState) {
	e.t.Helper()
	ctx := e.as(u)
	q, err := workflows.NewConnectedCopyTask(ctx, src, dst)
	require.NoError(e.t, err)
	copyTask := q.(*workflows.ConnectedCopyTask)
	if before != nil {
		before(copyTask)
	}
	status, _ := copyTask.Do(ctx)
	state := &workflows.ConnectedCopyState{}
	require.NoError(e.t, json.Unmarshal([]byte(copyTask.Model().PrivateState), state))
	return status, state
}

func notes(state *workflows.ConnectedCopyState) []string {
	var out []string
	for _, n := range state.Notes {
		parts := strings.Split(n.Path, "@connected")
		out = append(out, n.Reason+" "+parts[len(parts)-1])
	}
	sort.Strings(out)
	return out
}

func TestConnectedCopyFromFylvo(t *testing.T) {
	e := newConnectedEnv(t)
	e.fylvoPut(e.alex, "Docs/a.txt", []byte("aaa"))
	e.fylvoPut(e.alex, "Docs/sub/b.txt", []byte("bbbb"))
	e.fylvoMkdir(e.alex, "Docs/Empty")
	e.fylvoPut(e.alex, "loose.txt", []byte("loose"))

	status, state := e.runCopy(e.alex, []string{"cloudreve://my/Docs", "cloudreve://my/loose.txt"}, e.root, nil)
	require.Equal(t, task.StatusCompleted, status)
	require.Equal(t, 3, state.Copied)
	require.Equal(t, 0, state.Skipped+state.Failed)
	require.Equal(t, 3, state.Folders)
	require.Empty(t, state.Notes)
	require.Equal(t, map[string]string{
		"Docs/": "", "Docs/a.txt": "aaa", "Docs/sub/": "", "Docs/sub/b.txt": "bbbb", "Docs/Empty/": "", "loose.txt": "loose",
	}, e.driveTree(""))

	// Once more: everything is there already, nothing is copied twice.
	status, state = e.runCopy(e.alex, []string{"cloudreve://my/Docs", "cloudreve://my/loose.txt"}, e.root, nil)
	require.Equal(t, task.StatusCompleted, status)
	require.Equal(t, 0, state.Copied)
	require.Equal(t, 3, state.Skipped)
	require.Equal(t, []string{"same /Docs/a.txt", "same /Docs/sub/b.txt", "same /loose.txt"}, trimMy(notes(state)))
	require.Len(t, e.driveTree(""), 6)

	// A file of the same name with other content: both are kept.
	e.fylvoPut(e.alex, "loose (other).txt", []byte("x"))
	fm := manager.NewFileManager(e.dep, e.alex)
	uri, _ := fs.NewUriFromString("cloudreve://my/loose.txt")
	_, err := fm.Update(e.as(e.alex), &fs.UploadRequest{
		Props: &fs.UploadProps{Uri: uri, Size: 9},
		File:  io.NopCloser(strings.NewReader("changed!!")),
	})
	fm.Recycle()
	require.NoError(t, err)
	status, state = e.runCopy(e.alex, []string{"cloudreve://my/loose.txt"}, e.root, nil)
	require.Equal(t, task.StatusCompleted, status)
	require.Equal(t, 1, state.Copied)
	tree := e.driveTree("")
	require.Equal(t, "loose", tree["loose.txt"])
	require.Equal(t, "changed!!", tree["loose (2).txt"])
	// And again: found under its number.
	_, state = e.runCopy(e.alex, []string{"cloudreve://my/loose.txt"}, e.root, nil)
	require.Equal(t, 0, state.Copied)
	require.Equal(t, 1, state.Skipped)

	// Into a folder below, where a file has the folder's name: the folder gets a number.
	sub := e.sim.Find(e.sim.Find("", "Docs"), "sub")
	e.sim.AddFile(sub, "Docs", []byte("a file, not a folder"))
	_, state = e.runCopy(e.alex, []string{"cloudreve://my/Docs"}, e.root+"/Docs/sub", nil)
	require.Equal(t, 2, state.Copied)
	tree = e.driveTree("Docs/sub")
	require.Equal(t, "a file, not a folder", tree["Docs"])
	require.Equal(t, "aaa", tree["Docs (2)/a.txt"])
}

// trimMy shortens addresses of Fylvo's own files in a report.
func trimMy(list []string) []string {
	out := make([]string, len(list))
	for i, l := range list {
		out[i] = strings.Replace(l, "cloudreve://my", "", 1)
	}
	return out
}

func TestConnectedCopyToFylvo(t *testing.T) {
	e := newConnectedEnv(t)
	docs := e.sim.AddFolder("", "Docs")
	e.sim.AddFile(docs, "a.txt", []byte("aaa"))
	work := e.sim.AddFolder(docs, "Work")
	e.sim.AddFile(work, "deep.txt", []byte("deep"))
	e.sim.AddNative(docs, "Budget", "gsheet")
	e.sim.AddOther(docs, "Feedback", "application/vnd.google-apps.form")
	e.sim.AddShortcut(docs, "to work", work)
	e.sim.AddFile(docs, "twice.txt", []byte("one"))
	e.sim.AddFile(docs, "twice.txt", []byte("two"))
	e.sim.AddFile(docs, "a:b.txt", []byte("odd name"))
	e.fylvoMkdir(e.alex, "In")

	status, state := e.runCopy(e.alex, []string{e.root + "/Docs"}, "cloudreve://my/In", nil)
	require.Equal(t, task.StatusCompleted, status)
	tree := e.fylvoTree(e.alex, "In")
	require.Equal(t, "aaa", tree["Docs/a.txt"])
	require.Equal(t, "deep", tree["Docs/Work/deep.txt"])
	require.Equal(t, "odd name", tree["Docs/a_b.txt"])
	require.Contains(t, tree, "Docs/Budget.xlsx", "a document of the provider in its usual format")
	require.NotEmpty(t, tree["Docs/Budget.xlsx"])
	require.NotContains(t, tree, "Docs/twice.txt")
	require.NotContains(t, tree, "Docs/to work/")
	require.Equal(t, 4, state.Copied)
	require.Equal(t, []string{"duplicate /Docs/twice.txt", "link /Docs/to%20work", "no_export /Docs/Feedback.gform"}, notes(state))
	require.Equal(t, 2, state.Skipped, "the link is a folder and not counted as a file; the name twice is one row")

	// Again: what is there is passed over, the exported document too when its export
	// has the same length.
	_, state = e.runCopy(e.alex, []string{e.root + "/Docs"}, "cloudreve://my/In", nil)
	require.Equal(t, 4, countReason(state, workflows.CopyNoteSame))
	require.Equal(t, 0, state.Copied)
}

func countReason(state *workflows.ConnectedCopyState, reason string) int {
	n := 0
	for _, note := range state.Notes {
		if note.Reason == reason {
			n++
		}
	}
	return n
}

func TestConnectedCopyWithinTheStorage(t *testing.T) {
	e := newConnectedEnv(t)
	docs := e.sim.AddFolder("", "Docs")
	e.sim.AddFile(docs, "a.txt", []byte("aaa"))
	e.sim.AddFolder("", "Backup")
	status, state := e.runCopy(e.alex, []string{e.root + "/Docs"}, e.root+"/Backup", nil)
	require.Equal(t, task.StatusCompleted, status)
	require.Equal(t, 1, state.Copied)
	require.Equal(t, "aaa", e.driveTree("Backup")["Docs/a.txt"])
	require.Equal(t, "aaa", e.driveTree("Docs")["a.txt"], "the source is as it was")
}

func TestConnectedCopyStops(t *testing.T) {
	e := newConnectedEnv(t)
	e.fylvoPut(e.alex, "Docs/a.txt", []byte("aaa"))
	e.fylvoPut(e.alex, "Docs/b.txt", []byte("bbb"))

	// Stopped before it ran: nothing is copied.
	status, state := e.runCopy(e.alex, []string{"cloudreve://my/Docs"}, e.root, func(c *workflows.ConnectedCopyTask) { c.Halt() })
	require.Equal(t, task.StatusCanceled, status)
	require.Equal(t, workflows.CopyNoteStopped, state.Stopped)
	require.Empty(t, e.driveTree(""))

	// The storage is full: nothing of a file is kept, and the task ends there.
	e.sim.SetFull(true)
	status, state = e.runCopy(e.alex, []string{"cloudreve://my/Docs"}, e.root, nil)
	require.Equal(t, task.StatusError, status)
	require.Equal(t, workflows.CopyNoteFull, state.Stopped)
	require.Equal(t, []string{"full /Docs"}, trimMy(notes(state)))
	e.sim.SetFull(false)
	require.Empty(t, e.driveTree(""))
	// Room for the folder, none for the file: the folder is there, the file is not.
	e.sim.AddFolder("", "Docs")
	e.sim.SetFull(true)
	status, state = e.runCopy(e.alex, []string{"cloudreve://my/Docs"}, e.root, nil)
	require.Equal(t, task.StatusError, status)
	require.Equal(t, workflows.CopyNoteFull, state.Stopped)
	require.Equal(t, 1, state.Failed, "it ends at the first file")
	e.sim.SetFull(false)
	require.Equal(t, map[string]string{"Docs/": ""}, e.driveTree(""))

	// The bound of entries: what is beyond it is reported as not reached.
	limit := workflows.ConnectedCopyMaxEntries
	workflows.ConnectedCopyMaxEntries = 2
	defer func() { workflows.ConnectedCopyMaxEntries = limit }()
	status, state = e.runCopy(e.alex, []string{"cloudreve://my/Docs"}, e.root, nil)
	require.Equal(t, task.StatusCompleted, status)
	require.Equal(t, 1, state.Copied)
	require.Equal(t, 1, countReason(state, workflows.CopyNoteNotReached))
}

// What is checked before a task is made.
func TestConnectedCopyChecks(t *testing.T) {
	e := newConnectedEnv(t)
	docs := e.sim.AddFolder("", "Docs")
	e.sim.AddFile(docs, "a.txt", []byte("aaa"))
	e.fylvoPut(e.alex, "mine.txt", []byte("mine"))
	tasks := func() int {
		n, err := e.client.Task.Query().Count(e.ctx)
		require.NoError(t, err)
		return n
	}
	before := tasks()
	create := func(u *ent.User, src []string, dst string) error {
		_, err := (&ConnectedCopyService{Src: src, Dst: dst}).CreateConnectedCopy(e.as(u))
		return err
	}

	// Into itself or below itself.
	require.Equal(t, serializer.CodeParamErr, connectedCode(create(e.alex, []string{e.root + "/Docs"}, e.root+"/Docs")))
	e.sim.AddFolder(docs, "Sub")
	require.Equal(t, serializer.CodeParamErr, connectedCode(create(e.alex, []string{e.root + "/Docs"}, e.root+"/Docs/Sub")))
	// Nothing of a connected storage: that is the ordinary copy.
	e.fylvoMkdir(e.alex, "Out")
	require.Equal(t, serializer.CodeParamErr, connectedCode(create(e.alex, []string{"cloudreve://my/mine.txt"}, "cloudreve://my/Out")))
	// A target that is a file, or not there.
	require.Error(t, create(e.alex, []string{"cloudreve://my/mine.txt"}, e.root+"/Docs/a.txt"))
	require.Error(t, create(e.alex, []string{"cloudreve://my/mine.txt"}, e.root+"/Missing"))
	// Somebody else's storage, or files.
	require.Equal(t, serializer.CodeParentNotExist, connectedCode(create(e.sam, []string{e.root + "/Docs"}, "cloudreve://my")))
	require.Error(t, create(e.sam, []string{"cloudreve://my/mine.txt"}, e.root))
	// Files of Fylvo into Fylvo, also beside a source of a storage.
	require.Equal(t, serializer.CodeParamErr, connectedCode(create(e.alex, []string{"cloudreve://my/mine.txt", e.root + "/Docs"}, "cloudreve://my/Out")))
	// Whole file systems, and what is reached through a shortcut.
	require.Equal(t, serializer.CodeParamErr, connectedCode(create(e.alex, []string{"cloudreve://my"}, e.root)))
	require.Equal(t, serializer.CodeConnectedUnsupported, connectedCode(create(e.alex, []string{e.root}, "cloudreve://my/Out")))
	e.sim.AddShortcut("", "to docs", docs)
	require.Equal(t, serializer.CodeConnectedUnsupported, connectedCode(create(e.alex, []string{e.root + "/to docs/a.txt"}, "cloudreve://my/Out")))
	// Into Fylvo only into the account's own files.
	require.Equal(t, serializer.CodeParamErr, connectedCode(create(e.alex, []string{e.root + "/Docs"}, "cloudreve://my/Out?category=image")))
	// Into a connected storage while changes are switched off.
	t.Setenv(connected.EnvWrite, "")
	require.Equal(t, serializer.CodeConnectedUnsupported, connectedCode(create(e.alex, []string{"cloudreve://my/mine.txt"}, e.root)))
	require.Equal(t, before, tasks(), "no task was made")
	_ = queue.ConnectedCopyTaskType
}

// A folder copied into the folder it is in: the copy is a folder of its own, and nothing
// is put into the folder that is read.
func TestConnectedCopyIntoItsParent(t *testing.T) {
	e := newConnectedEnv(t)
	docs := e.sim.AddFolder("", "Docs")
	e.sim.AddFile(docs, "a.txt", []byte("aaa"))
	e.sim.AddNative(docs, "Budget", "gsheet")
	status, state := e.runCopy(e.alex, []string{e.root + "/Docs"}, e.root, nil)
	require.Equal(t, task.StatusCompleted, status)
	require.Equal(t, 2, state.Copied)
	tree := e.driveTree("")
	require.NotContains(t, tree, "Docs/Budget.xlsx", "the source is as it was")
	require.Equal(t, "aaa", tree["Docs (2)/a.txt"])
	require.Contains(t, tree, "Docs (2)/Budget.xlsx")
}

// Names that become one at the target are both kept; empty files are copied.
func TestConnectedCopyNamesAndEmptyFiles(t *testing.T) {
	e := newConnectedEnv(t)
	docs := e.sim.AddFolder("", "Docs")
	e.sim.AddFile(docs, "a:b.txt", []byte("one"))
	e.sim.AddFile(docs, "a_b.txt", []byte("two"))
	e.sim.AddFile(docs, "empty.txt", nil)
	e.fylvoMkdir(e.alex, "In")
	status, state := e.runCopy(e.alex, []string{e.root + "/Docs"}, "cloudreve://my/In", nil)
	require.Equal(t, task.StatusCompleted, status)
	require.Equal(t, 3, state.Copied)
	tree := e.fylvoTree(e.alex, "In")
	require.ElementsMatch(t, []string{"one", "two"}, []string{tree["Docs/a_b.txt"], tree["Docs/a_b (2).txt"]})
	require.Contains(t, tree, "Docs/empty.txt")
	require.Equal(t, "", tree["Docs/empty.txt"])
	// Again: each of the two finds its own copy, nothing is copied twice or left out.
	_, state = e.runCopy(e.alex, []string{e.root + "/Docs"}, "cloudreve://my/In", nil)
	require.Equal(t, 0, state.Copied, "%+v", state.Notes)
	require.Equal(t, 3, state.Skipped)
	require.Len(t, e.fylvoTree(e.alex, "In"), 4)

	// An empty file of Fylvo, which never had content, into the storage.
	fm := manager.NewFileManager(e.dep, e.alex)
	uri, _ := fs.NewUriFromString("cloudreve://my/In/new.txt")
	_, err := fm.Create(e.as(e.alex), uri, types.FileTypeFile)
	fm.Recycle()
	require.NoError(t, err)
	status, state = e.runCopy(e.alex, []string{"cloudreve://my/In/new.txt"}, e.root, nil)
	require.Equal(t, task.StatusCompleted, status)
	require.Equal(t, 1, state.Copied, "%+v", state.Notes)
	require.Contains(t, e.driveTree(""), "new.txt")
}

// The server ends while a copy is under way: it is resumed later, not ended.
func TestConnectedCopyResumes(t *testing.T) {
	e := newConnectedEnv(t)
	e.fylvoPut(e.alex, "a.txt", []byte("aaa"))
	ctx, cancel := context.WithCancel(e.as(e.alex))
	cancel()
	q, err := workflows.NewConnectedCopyTask(e.as(e.alex), []string{"cloudreve://my/a.txt"}, e.root)
	require.NoError(t, err)
	status, err := q.(*workflows.ConnectedCopyTask).Do(ctx)
	require.NoError(t, err)
	require.Equal(t, task.StatusSuspending, status)
	// Run again, it copies.
	status, state := e.runCopy(e.alex, []string{"cloudreve://my/a.txt"}, e.root, nil)
	require.Equal(t, task.StatusCompleted, status)
	require.Equal(t, 1, state.Copied)
}
