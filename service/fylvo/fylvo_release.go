package fylvo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
)

// Shared part of the in-app updates (Android and desktop). A publish script writes into one
// folder per app (and platform):
//   - the file, named after its SHA-256: <sha256><ext>. A published file is never changed.
//   - release.json, written last and in one step. Its fields depend on the app, but every
//     release names the checksum and size of its file.
//
// The server only reads them. A file is served only when it is the one release.json names and
// its size and checksum match, so an app never gets a version number paired with another file.
const releaseFile = "release.json"

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// releaseInfo is what every release.json provides.
type releaseInfo interface {
	checksum() string
	fileSize() int64
	// valid checks the app specific fields.
	valid() bool
}

// releaseStore reads the published release from one folder. It remembers which files it has
// checked, so the checksum of a file is computed only once.
type releaseStore[T releaseInfo] struct {
	dir      string
	ext      string
	maxSize  int64
	notFound error

	mu       sync.Mutex
	verified map[string]bool // sha256 -> the file <sha256><ext> had exactly this checksum
}

func newReleaseStore[T releaseInfo](dir, ext string, maxSize int64, notFound error) *releaseStore[T] {
	return &releaseStore[T]{dir: dir, ext: ext, maxSize: maxSize, notFound: notFound, verified: map[string]bool{}}
}

// latest returns the published release, or notFound when there is none or it does not match
// its file.
func (s *releaseStore[T]) latest() (T, error) {
	r, f, err := s.open()
	if err == nil {
		f.Close()
	}
	return r, err
}

// openFile opens the file of the published release, but only when its checksum is sha256 (the
// one the app was told by latest). The caller closes the file.
func (s *releaseStore[T]) openFile(sha256 string) (T, *os.File, error) {
	r, f, err := s.open()
	if err != nil {
		return r, nil, err
	}
	if r.checksum() != sha256 {
		f.Close()
		var none T
		return none, nil, s.notFound
	}
	return r, f, nil
}

func (s *releaseStore[T]) open() (T, *os.File, error) {
	var none T
	raw, err := os.ReadFile(filepath.Join(s.dir, releaseFile))
	if err != nil {
		return none, nil, s.notFound
	}
	var r T
	if err := json.Unmarshal(raw, &r); err != nil || !r.valid() || !sha256Hex.MatchString(r.checksum()) ||
		r.fileSize() <= 0 || r.fileSize() > s.maxSize {
		return none, nil, s.notFound
	}

	// The name is built from the checked hex checksum only, so it cannot leave the folder.
	path := filepath.Join(s.dir, r.checksum()+s.ext)
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		return none, nil, s.notFound
	}
	f, err := os.Open(path)
	if err != nil {
		return none, nil, s.notFound
	}
	if info, err := f.Stat(); err != nil || info.Size() != r.fileSize() {
		f.Close()
		return none, nil, s.notFound
	}
	if ok, err := s.check(f, r.checksum()); err != nil || !ok {
		f.Close()
		if err != nil {
			return none, nil, serializer.NewError(serializer.CodeIOFailed, "Failed to read the release", err)
		}
		return none, nil, s.notFound
	}
	return r, f, nil
}

// check tells whether the open file has the checksum want, and leaves it at the start.
func (s *releaseStore[T]) check(f *os.File, want string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.verified[want] {
		return true, nil
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return false, fmt.Errorf("rewind the file: %w", err)
	}
	if hex.EncodeToString(h.Sum(nil)) != want {
		return false, nil
	}
	s.verified[want] = true
	return true, nil
}
