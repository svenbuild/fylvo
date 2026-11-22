// Package fylvo holds small features that only Fylvo has.
package fylvo

import (
	"os"
	"sync"

	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/pkg/util"
)

// In-app updates of the Fylvo Android app. scripts/publish-android.sh writes into <data>/android/:
//   - the APK, named after its SHA-256: <sha256>.apk. A published APK is never changed.
//   - release.json, written last and in one step:
//     {"version_code":3,"version_name":"1.1.0","sha256":"<64 hex>","size":6234643}
//
// See fylvo_release.go for how the files are checked.
const (
	AndroidReleaseFile = releaseFile
	// maxApkSize guards against a wrong file in the folder; the app is about 6 MB.
	maxApkSize = 200 << 20
)

// ErrNoAndroidRelease means nothing (valid) is published; the app treats it as "no update".
var ErrNoAndroidRelease = serializer.NewError(serializer.CodeNotFound, "No Android release is published", nil)

// AndroidRelease is the published version of the app.
type AndroidRelease struct {
	VersionCode int    `json:"version_code"`
	VersionName string `json:"version_name"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
}

func (r AndroidRelease) checksum() string { return r.SHA256 }
func (r AndroidRelease) fileSize() int64  { return r.Size }
func (r AndroidRelease) valid() bool {
	return r.VersionCode > 0 && r.VersionName != "" && len(r.VersionName) <= 32
}

// AndroidReleases reads the published release from one folder.
type AndroidReleases struct {
	store *releaseStore[AndroidRelease]
}

// NewAndroidReleases reads releases from dir.
func NewAndroidReleases(dir string) *AndroidReleases {
	return &AndroidReleases{store: newReleaseStore[AndroidRelease](dir, ".apk", maxApkSize, ErrNoAndroidRelease)}
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
	r, err := s.store.latest()
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// OpenApk opens the APK of the published release, but only when its checksum is sha256 (the one
// the app was told by Latest). The caller closes the file.
func (s *AndroidReleases) OpenApk(sha256 string) (*AndroidRelease, *os.File, error) {
	r, f, err := s.store.openFile(sha256)
	if err != nil {
		return nil, nil, err
	}
	return &r, f, nil
}
