package fylvo

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// publish writes the APK under its checksum and a release.json that names it.
func publish(t *testing.T, dir string, code int, apk []byte) string {
	t.Helper()
	hash := sum(apk)
	if err := os.WriteFile(filepath.Join(dir, hash+".apk"), apk, 0644); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, dir, fmt.Sprintf(`{"version_code":%d,"version_name":"1.%d.0","sha256":"%s","size":%d}`, code, code, hash, len(apk)))
	return hash
}

func writeJSON(t *testing.T, dir, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, AndroidReleaseFile), []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLatestReturnsThePublishedRelease(t *testing.T) {
	dir := t.TempDir()
	apk := []byte("apk bytes v3")
	hash := publish(t, dir, 3, apk)

	r, err := NewAndroidReleases(dir).Latest()
	if err != nil {
		t.Fatal(err)
	}
	if r.VersionCode != 3 || r.VersionName != "1.3.0" || r.Size != int64(len(apk)) || r.SHA256 != hash {
		t.Fatalf("unexpected release %+v", r)
	}
}

func TestOpenApkServesOnlyThePublishedChecksum(t *testing.T) {
	dir := t.TempDir()
	apk := []byte("apk bytes v3")
	hash := publish(t, dir, 3, apk)
	releases := NewAndroidReleases(dir)

	_, f, err := releases.OpenApk(hash)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(f)
	f.Close()
	if string(got) != string(apk) {
		t.Fatalf("served %q", got)
	}

	for _, other := range []string{sum([]byte("other")), "../release", "", hash[:63], strings.ToUpper(hash)} {
		if _, _, err := releases.OpenApk(other); !errors.Is(err, ErrNoAndroidRelease) {
			t.Fatalf("%q: want ErrNoAndroidRelease, got %v", other, err)
		}
	}
}

func TestLatestWithoutValidReleaseIsNotFound(t *testing.T) {
	apk := []byte("apk")
	hash := sum(apk)
	release := func(code int, name, sha string, size int) string {
		return fmt.Sprintf(`{"version_code":%d,"version_name":"%s","sha256":"%s","size":%d}`, code, name, sha, size)
	}
	cases := map[string]struct {
		json    string
		withApk bool
	}{
		"nothing":       {"", false},
		"no apk":        {release(3, "1.1.0", hash, 3), false},
		"no json":       {"", true},
		"broken json":   {`{"version_code":`, true},
		"zero version":  {release(0, "1.1.0", hash, 3), true},
		"no name":       {release(3, "", hash, 3), true},
		"bad checksum":  {release(3, "1.1.0", "../../etc/passwd", 3), true},
		"upper case":    {release(3, "1.1.0", strings.ToUpper(hash), 3), true},
		"wrong size":    {release(3, "1.1.0", hash, 4), true},
		"no size":       {release(3, "1.1.0", hash, 0), true},
		"name too long": {release(3, strings.Repeat("1", 33), hash, 3), true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.withApk {
				if err := os.WriteFile(filepath.Join(dir, hash+".apk"), apk, 0644); err != nil {
					t.Fatal(err)
				}
			}
			if tc.json != "" {
				writeJSON(t, dir, tc.json)
			}
			if _, err := NewAndroidReleases(dir).Latest(); !errors.Is(err, ErrNoAndroidRelease) {
				t.Fatalf("want ErrNoAndroidRelease, got %v", err)
			}
		})
	}
}

func TestApkWithOtherContentThanItsNameIsNotServed(t *testing.T) {
	dir := t.TempDir()
	hash := sum([]byte("good"))
	// Same size, other bytes: only the checksum tells them apart.
	if err := os.WriteFile(filepath.Join(dir, hash+".apk"), []byte("evil"), 0644); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, dir, fmt.Sprintf(`{"version_code":3,"version_name":"1.1.0","sha256":"%s","size":4}`, hash))
	if _, err := NewAndroidReleases(dir).Latest(); !errors.Is(err, ErrNoAndroidRelease) {
		t.Fatalf("want ErrNoAndroidRelease, got %v", err)
	}
}

func TestANewReleaseReplacesTheOldOne(t *testing.T) {
	dir := t.TempDir()
	releases := NewAndroidReleases(dir)
	old := publish(t, dir, 3, []byte("first"))
	if _, err := releases.Latest(); err != nil {
		t.Fatal(err)
	}
	newer := publish(t, dir, 4, []byte("second, longer apk"))

	r, err := releases.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if r.VersionCode != 4 || r.SHA256 != newer {
		t.Fatalf("stale release %+v", r)
	}
	// The old file is still on disk, but no longer served.
	if _, _, err := releases.OpenApk(old); !errors.Is(err, ErrNoAndroidRelease) {
		t.Fatalf("old APK still served: %v", err)
	}
}
