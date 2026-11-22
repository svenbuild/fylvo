package fylvo

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// publishDesktop writes a package under its checksum and a release.json that names it.
func publishDesktop(t *testing.T, dir, platform, version string, pkg []byte) string {
	t.Helper()
	hash := sum(pkg)
	p := filepath.Join(dir, platform)
	if err := os.MkdirAll(p, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, hash+".pkg"), pkg, 0644); err != nil {
		t.Fatal(err)
	}
	writeDesktopJSON(t, dir, platform, fmt.Sprintf(`{"version":"%s","sha256":"%s","size":%d,"file_name":"Fylvo.msix"}`,
		version, hash, len(pkg)))
	return hash
}

func writeDesktopJSON(t *testing.T, dir, platform, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, platform, releaseFile), []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestDesktopReleasesArePerPlatform(t *testing.T) {
	dir := t.TempDir()
	win := publishDesktop(t, dir, "windows-x64", "0.3.0.0", []byte("msix"))
	lin := publishDesktop(t, dir, "linux-x64-rpm", "0.3.1", []byte("linux package"))
	d := NewDesktopReleases(dir)

	r, err := d.Latest("windows-x64")
	if err != nil || r.Version != "0.3.0.0" || r.SHA256 != win || r.Size != 4 || r.FileName != "Fylvo.msix" {
		t.Fatalf("windows: %+v %v", r, err)
	}
	r, err = d.Latest("linux-x64-rpm")
	if err != nil || r.Version != "0.3.1" || r.SHA256 != lin {
		t.Fatalf("linux: %+v %v", r, err)
	}
	if _, err := d.Latest("windows-arm64"); err != ErrNoDesktopRelease {
		t.Fatalf("nothing published must be not found, got %v", err)
	}
	for _, p := range []string{"", "..", "../windows-x64", "windows-x64/", "WINDOWS-X64", "mac-x64", "linux-x64"} {
		if _, err := d.Latest(p); err != ErrNoDesktopRelease {
			t.Fatalf("platform %q must be not found, got %v", p, err)
		}
	}

	// The package of one platform is not served under another one.
	if _, _, err := d.OpenPackage("linux-x64-rpm", win); err != ErrNoDesktopRelease {
		t.Fatalf("wrong platform must be not found, got %v", err)
	}
	_, f, err := d.OpenPackage("windows-x64", win)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func TestDesktopReleaseRejectsBadFields(t *testing.T) {
	dir := t.TempDir()
	pkg := []byte("msix")
	hash := publishDesktop(t, dir, "windows-x64", "0.3.0", pkg)
	d := NewDesktopReleases(dir)
	for _, text := range []string{
		fmt.Sprintf(`{"version":"","sha256":"%s","size":4,"file_name":"a.msix"}`, hash),
		fmt.Sprintf(`{"version":"0.3.0.0.1","sha256":"%s","size":4,"file_name":"a.msix"}`, hash),
		fmt.Sprintf(`{"version":"0.65536","sha256":"%s","size":4,"file_name":"a.msix"}`, hash),
		fmt.Sprintf(`{"version":"0.3-beta","sha256":"%s","size":4,"file_name":"a.msix"}`, hash),
		fmt.Sprintf(`{"version":"0.3.0","sha256":"%s","size":4,"file_name":""}`, hash),
		fmt.Sprintf(`{"version":"0.3.0","sha256":"%s","size":4,"file_name":"a\"b.msix"}`, hash),
		fmt.Sprintf(`{"version":"0.3.0","sha256":"%s","size":4,"file_name":"../a.msix"}`, hash),
		fmt.Sprintf(`{"version":"0.3.0","sha256":"%s","size":5,"file_name":"a.msix"}`, hash),
		`{"version":"0.3.0","sha256":"../../etc/passwd","size":4,"file_name":"a.msix"}`,
		fmt.Sprintf(`{"version":"0.3.0","sha256":"%s","size":4,"file_name":"a.msix","signature":"bm90IDY0IGJ5dGVz"}`, hash),
		fmt.Sprintf(`{"version":"0.3.0","sha256":"%s","size":4,"file_name":"a.msix","signature":"not base64!"}`, hash),
		`not json`,
	} {
		writeDesktopJSON(t, dir, "windows-x64", text)
		if _, err := d.Latest("windows-x64"); err != ErrNoDesktopRelease {
			t.Fatalf("%s: want not found, got %v", text, err)
		}
	}
}

func TestValidDesktopVersion(t *testing.T) {
	for v, want := range map[string]bool{
		"1": true, "0.3.0": true, "0.3.0.0": true, "65535.65535.65535.65535": true, "00010.2": true,
		"": false, ".": false, "1.": false, ".1": false, "1..2": false, "1.2.3.4.5": false,
		"65536": false, "+1": false, "-1": false, " 1": false, "1.2a": false, "000001": false,
	} {
		if got := ValidDesktopVersion(v); got != want {
			t.Errorf("ValidDesktopVersion(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestDesktopReleaseCarriesItsSignature(t *testing.T) {
	dir := t.TempDir()
	pkg := []byte("rpm")
	hash := publishDesktop(t, dir, "linux-x64-rpm", "0.3.0", pkg)
	sig := strings.Repeat("A", 86) + "=="
	writeDesktopJSON(t, dir, "linux-x64-rpm", fmt.Sprintf(`{"version":"0.3.0","sha256":"%s","size":3,"file_name":"f.rpm","signature":"%s"}`, hash, sig))
	r, err := NewDesktopReleases(dir).Latest("linux-x64-rpm")
	if err != nil || r.Signature != sig {
		t.Fatalf("%+v %v", r, err)
	}
}
