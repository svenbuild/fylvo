// Package fylvo holds small features that only Fylvo has.
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
	"github.com/cloudreve/Cloudreve/v4/pkg/util"
)

// In-app updates of the Fylvo Android app. scripts/publish-android.sh writes into <data>/android/:
//   - the APK, named after its SHA-256: <sha256>.apk. A published APK is never changed.
//   - release.json, written last and in one step:
//     {"version_code":3,"version_name":"1.1.0","sha256":"<64 hex>","size":6234643}
//
// The server only reads them. An APK is served only when it is the one release.json names and
// its size and checksum match, so the app never gets a version number paired with another file.
const (
	AndroidReleaseFile = "release.json"
	// maxApkSize guards against a wrong file in the folder; the app is about 6 MB.
	maxApkSize = 200 << 20
)

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ErrNoAndroidRelease means nothing (valid) is published; the app treats it as "no update".
var ErrNoAndroidRelease = serializer.NewError(serializer.CodeNotFound, "No Android release is published", nil)

// AndroidRelease is the published version of the app.
type AndroidRelease struct {
	VersionCode int    `json:"version_code"`
	VersionName string `json:"version_name"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
}

// AndroidReleases reads the published release from one folder. It remembers which APKs it has
// checked, so the checksum of a file is computed only once.
type AndroidReleases struct {
	dir string

	mu       sync.Mutex
	verified map[string]bool // sha256 -> the file <sha256>.apk had exactly this checksum
}

// NewAndroidReleases reads releases from dir.
func NewAndroidReleases(dir string) *AndroidReleases {
	return &AndroidReleases{dir: dir, verified: map[string]bool{}}
}

var (
	defaultReleases     *AndroidReleases
	defaultReleasesOnce sync.Once
)

// DefaultAndroidReleases reads from <data>/android.
func DefaultAndroidReleases() *AndroidReleases {
	defaultReleasesOnce.Do(func() {
		defaultReleases = NewAndroidReleases(util.DataPath("android"))
	})
	return defaultReleases
}

// Latest returns the published release, or ErrNoAndroidRelease when there is none or it does
// not match its APK.
func (s *AndroidReleases) Latest() (*AndroidRelease, error) {
	r, f, err := s.open()
	if err != nil {
		return nil, err
	}
	f.Close()
	return r, nil
}

// OpenApk opens the APK of the published release, but only when its checksum is sha256 (the one
// the app was told by Latest). The caller closes the file.
func (s *AndroidReleases) OpenApk(sha256 string) (*AndroidRelease, *os.File, error) {
	r, f, err := s.open()
	if err != nil {
		return nil, nil, err
	}
	if r.SHA256 != sha256 {
		f.Close()
		return nil, nil, ErrNoAndroidRelease
	}
	return r, f, nil
}

func (s *AndroidReleases) open() (*AndroidRelease, *os.File, error) {
	raw, err := os.ReadFile(filepath.Join(s.dir, AndroidReleaseFile))
	if err != nil {
		return nil, nil, ErrNoAndroidRelease
	}
	var r AndroidRelease
	if err := json.Unmarshal(raw, &r); err != nil || r.VersionCode <= 0 || r.VersionName == "" ||
		len(r.VersionName) > 32 || !sha256Hex.MatchString(r.SHA256) || r.Size <= 0 || r.Size > maxApkSize {
		return nil, nil, ErrNoAndroidRelease
	}

	// The name is built from the checked hex checksum only, so it cannot leave the folder.
	path := filepath.Join(s.dir, r.SHA256+".apk")
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		return nil, nil, ErrNoAndroidRelease
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, ErrNoAndroidRelease
	}
	if info, err := f.Stat(); err != nil || info.Size() != r.Size {
		f.Close()
		return nil, nil, ErrNoAndroidRelease
	}
	if ok, err := s.check(f, r.SHA256); err != nil || !ok {
		f.Close()
		if err != nil {
			return nil, nil, serializer.NewError(serializer.CodeIOFailed, "Failed to read the Android release", err)
		}
		return nil, nil, ErrNoAndroidRelease
	}
	return &r, f, nil
}

// check tells whether the open file has the checksum want, and leaves it at the start.
func (s *AndroidReleases) check(f *os.File, want string) (bool, error) {
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
		return false, fmt.Errorf("rewind the APK: %w", err)
	}
	if hex.EncodeToString(h.Sum(nil)) != want {
		return false, nil
	}
	s.verified[want] = true
	return true, nil
}
