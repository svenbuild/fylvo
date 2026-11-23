package connected

import (
	"context"
	"errors"
	"hash"
	"io"
	"strconv"
	"strings"
	"time"
)

// Copying between Fylvo and connected storages (see workflows/fylvo_connected_copy.go):
// what the copy task needs of a connected storage beyond what is there for clients.

// ErrCopyChanged: the file changed while it was copied, or its bytes were not those of the
// version that was read. Nothing of it may be kept.
var ErrCopyChanged = errors.New("the file changed while it was copied")

// CopyName is name as a new entry may carry it, in Fylvo and in a connected storage: the
// characters no new name may have become "_", nothing is left that is not plain text, and
// it is not longer than a name may be. A name with nothing left is "_".
func CopyName(name string) string {
	base, ext := splitName(name)
	out := besideName(legalNamePart(base), "", legalNamePart(ext))
	if out == "" || out == "." || out == ".." {
		return "_"
	}
	return out
}

// NumberedName is name with " (n)" before its extension, for n from 2: "letter (2).docx".
func NumberedName(name string, n int) string {
	if n < 2 {
		return name
	}
	base, ext := splitName(name)
	return besideName(base, " ("+strconv.Itoa(n)+")", ext)
}

func splitName(name string) (base, ext string) {
	if dot := strings.LastIndex(name, "."); dot > 0 {
		return name[:dot], name[dot:]
	}
	return name, ""
}

// CopyReader hands out the bytes of one version of a file of a connected storage. When its
// last byte is read it checks that they were all of that version: the length, the
// checksum the provider tells for it (where it tells one), and that the provider still
// has that version. If not, that last read answers ErrCopyChanged instead of the last
// bytes, so whatever takes the bytes never ends up with a whole file.
type CopyReader struct {
	m       *Manager
	c       *Conn
	ctx     context.Context
	body    io.ReadCloser
	release func()
	stop    context.CancelFunc
	watch   *time.Timer

	id, version, want string
	size, read        int64
	sum               hash.Hash
	text              func(hash.Hash) string
	checked           bool
}

// waitForStream takes one of the server's places for streams, waiting for as long as
// ctx lasts: a copy is work in the background, nobody waits for an answer.
func waitForStream(ctx context.Context) (func(), error) {
	for {
		release, err := AcquireStream(ctx)
		if !errors.Is(err, ErrBusy) {
			return release, err
		}
	}
}

// OpenCopy opens the file of row for copying, in its state of now. Links and documents
// that exist only at the provider are not opened here. The caller closes the reader.
func (m *Manager) OpenCopy(ctx context.Context, c *Conn, row *Row) (*CopyReader, error) {
	if row.IsDir || row.Link != nil || row.Native != "" || row.Unsupported != "" {
		return nil, ErrUnsupported
	}
	release, err := waitForStream(ctx)
	if err != nil {
		return nil, err
	}
	c.P.Known(&row.Item)
	item, err := c.P.Get(ctx, row.ID)
	if err != nil {
		release()
		return nil, Error(ctx, err)
	}
	if item.IsDir || item.Native != "" || item.Link != nil {
		release()
		return nil, ErrCopyChanged
	}
	if item.Version == "" {
		// Without a marker for its state nothing would show that the bytes are of one
		// version.
		release()
		return nil, ErrUnsupported
	}
	parent := ctx
	// The read ends when the provider stands still for too long.
	ctx, stop := context.WithCancel(ctx)
	watch := time.AfterFunc(uploadStall, stop)
	content, err := c.P.Open(ctx, item, "")
	if err != nil {
		watch.Stop()
		stop()
		release()
		return nil, Error(ctx, err)
	}
	r := &CopyReader{
		m: m, c: c, ctx: parent, body: content.Body, release: release, stop: stop, watch: watch,
		id: item.ID, version: item.Version, want: item.Hash, size: item.Size,
	}
	if r.want != "" {
		r.sum, r.text = checksum(c.Provider)
	}
	return r, nil
}

// Size is the length of the version that is read.
func (r *CopyReader) Size() int64 { return r.size }

// Hash is the checksum the provider tells for the version that is read, "" if none.
func (r *CopyReader) Hash() string { return r.want }

func (r *CopyReader) Read(p []byte) (int, error) {
	if r.checked {
		return 0, io.EOF
	}
	// Not more than the file has, and one byte more to see that it ends there.
	if left := r.size - r.read; int64(len(p)) > left {
		p = p[:left]
	}
	var (
		n   int
		err error
	)
	if len(p) > 0 {
		r.watch.Reset(uploadStall)
		n, err = r.body.Read(p)
		r.watch.Stop()
		r.read += int64(n)
		if r.sum != nil {
			r.sum.Write(p[:n])
		}
	}
	if r.read < r.size {
		if err == io.EOF {
			// The provider ended early: not the whole version.
			return 0, ErrCopyChanged
		}
		return n, err
	}
	// The last bytes: they are handed out only when the whole file was that version.
	if verr := r.verify(); verr != nil {
		return 0, verr
	}
	r.checked = true
	return n, nil
}

func (r *CopyReader) verify() error {
	// The provider must end here, cleanly: not one byte more, and no error.
	var extra [1]byte
	for tries := 0; ; tries++ {
		r.watch.Reset(uploadStall)
		more, err := r.body.Read(extra[:])
		r.watch.Stop()
		if more > 0 {
			return ErrCopyChanged
		}
		if err == io.EOF {
			break
		}
		if err != nil || tries > 3 {
			return ErrCopyChanged
		}
	}
	if r.sum != nil && r.text(r.sum) != r.want {
		return ErrCopyChanged
	}
	ctx, stop := context.WithTimeout(r.ctx, 30*time.Second)
	defer stop()
	after, err := r.c.P.Get(ctx, r.id)
	if err != nil {
		return Error(ctx, err)
	}
	if after.Version != r.version {
		return ErrCopyChanged
	}
	return nil
}

// Close ends the read and frees its place.
func (r *CopyReader) Close() error {
	r.watch.Stop()
	err := r.body.Close()
	r.stop()
	r.release()
	return err
}

// OpenExport opens a document that exists only at the provider in the given format, for
// copying. Its length is not known before; when the end is read the provider is asked
// whether the document is still in the version that was exported, and if not the end
// answers ErrCopyChanged. The caller closes the reader.
func (m *Manager) OpenExport(ctx context.Context, c *Conn, row *Row, format string) (io.ReadCloser, error) {
	if row.Native == "" || row.Unsupported != "" {
		return nil, ErrUnsupported
	}
	known := false
	for _, f := range ExportFormats[row.Native] {
		known = known || f == format
	}
	if !known {
		return nil, ErrUnsupported
	}
	release, err := waitForStream(ctx)
	if err != nil {
		return nil, err
	}
	c.P.Known(&row.Item)
	item, err := c.P.Get(ctx, row.ID)
	if err != nil {
		release()
		return nil, Error(ctx, err)
	}
	if item.Version == "" {
		release()
		return nil, ErrUnsupported
	}
	parent := ctx
	ctx, stop := context.WithCancel(ctx)
	watch := time.AfterFunc(uploadStall, stop)
	content, err := c.P.Export(ctx, item, format)
	watch.Stop()
	if err != nil {
		stop()
		release()
		return nil, Error(ctx, err)
	}
	return &exportReader{c: c, ctx: parent, body: content.Body, id: item.ID, version: item.Version,
		release: release, stop: stop, watch: watch}, nil
}

type exportReader struct {
	c       *Conn
	ctx     context.Context
	body    io.ReadCloser
	id      string
	version string
	release func()
	stop    context.CancelFunc
	watch   *time.Timer
	done    bool
}

func (r *exportReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.watch.Reset(uploadStall)
	n, err := r.body.Read(p)
	r.watch.Stop()
	if err != io.EOF {
		return n, err
	}
	ctx, stop := context.WithTimeout(r.ctx, 30*time.Second)
	defer stop()
	after, gerr := r.c.P.Get(ctx, r.id)
	if gerr != nil {
		return 0, Error(ctx, gerr)
	}
	if after.Version != r.version {
		return 0, ErrCopyChanged
	}
	r.done = true
	return n, io.EOF
}

func (r *exportReader) Close() error {
	r.watch.Stop()
	err := r.body.Close()
	r.stop()
	r.release()
	return err
}
