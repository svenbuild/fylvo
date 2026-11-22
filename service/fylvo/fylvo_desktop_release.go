package fylvo

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/pkg/util"
)

// In-app updates of Fylvo Desktop. scripts/publish-desktop.sh writes into
// <data>/desktop/<platform>/:
//   - the package, named after its SHA-256: <sha256>.pkg
//   - release.json: {"version":"0.3.0","sha256":"<64 hex>","size":123,"file_name":"Fylvo-Desktop-0.3.0-x64.msix"}
//
// The format of the package depends on the platform (Windows: a signed MSIX). The app checks
// size and checksum after the download; Windows checks the signature before it installs.
const (
	desktopPackageExt = ".pkg"
	// maxDesktopPackageSize guards against a wrong file in the folder; the app is about 12 MB.
	maxDesktopPackageSize = 500 << 20
)

// DesktopPlatforms are the platforms that can have a release. Anything else is never used in
// a path.
var DesktopPlatforms = []string{"windows-x64", "windows-arm64", "linux-x64", "linux-arm64"}

// ErrNoDesktopRelease means nothing (valid) is published; the app treats it as "no update".
var ErrNoDesktopRelease = serializer.NewError(serializer.CodeNotFound, "No desktop release is published", nil)

var desktopFileName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,80}$`)

// DesktopRelease is the published version of the app for one platform.
type DesktopRelease struct {
	Version  string `json:"version"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
	FileName string `json:"file_name"`
}

func (r DesktopRelease) checksum() string { return r.SHA256 }
func (r DesktopRelease) fileSize() int64  { return r.Size }
func (r DesktopRelease) valid() bool {
	return ValidDesktopVersion(r.Version) && desktopFileName.MatchString(r.FileName) &&
		r.FileName != "." && r.FileName != ".."
}

// ValidDesktopVersion accepts 1 to 4 numbers separated by dots, each 0 to 65535 (the parts of a
// Windows package version). Apps compare part by part, a missing part counts as 0.
func ValidDesktopVersion(v string) bool {
	parts := strings.Split(v, ".")
	if len(parts) < 1 || len(parts) > 4 {
		return false
	}
	for _, p := range parts {
		if p == "" || len(p) > 5 || strings.Trim(p, "0123456789") != "" {
			return false
		}
		if _, err := strconv.ParseUint(p, 10, 16); err != nil {
			return false
		}
	}
	return true
}

// DesktopReleases reads the published releases, one folder per platform.
type DesktopReleases struct {
	stores map[string]*releaseStore[DesktopRelease]
}

// NewDesktopReleases reads releases from dir/<platform>.
func NewDesktopReleases(dir string) *DesktopReleases {
	d := &DesktopReleases{stores: map[string]*releaseStore[DesktopRelease]{}}
	for _, p := range DesktopPlatforms {
		d.stores[p] = newReleaseStore[DesktopRelease](filepath.Join(dir, p), desktopPackageExt,
			maxDesktopPackageSize, ErrNoDesktopRelease)
	}
	return d
}

var (
	defaultDesktopReleases     *DesktopReleases
	defaultDesktopReleasesOnce sync.Once
)

// DefaultDesktopReleases reads from <data>/desktop.
func DefaultDesktopReleases() *DesktopReleases {
	defaultDesktopReleasesOnce.Do(func() {
		defaultDesktopReleases = NewDesktopReleases(util.DataPath("desktop"))
	})
	return defaultDesktopReleases
}

// Latest returns the published release of a platform, or ErrNoDesktopRelease.
func (d *DesktopReleases) Latest(platform string) (*DesktopRelease, error) {
	s, ok := d.stores[platform]
	if !ok {
		return nil, ErrNoDesktopRelease
	}
	r, err := s.latest()
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// OpenPackage opens the package of the published release of a platform, but only when its
// checksum is sha256. The caller closes the file.
func (d *DesktopReleases) OpenPackage(platform, sha256 string) (*DesktopRelease, *os.File, error) {
	s, ok := d.stores[platform]
	if !ok {
		return nil, nil, ErrNoDesktopRelease
	}
	r, f, err := s.openFile(sha256)
	if err != nil {
		return nil, nil, err
	}
	return &r, f, nil
}
