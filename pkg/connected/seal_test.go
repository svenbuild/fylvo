package connected

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
)

func testSealer(key byte) *Sealer {
	return NewSealer(func(context.Context) ([]byte, error) {
		return bytes.Repeat([]byte{key}, 32), nil
	})
}

func TestSealRoundTrip(t *testing.T) {
	s := testSealer(1)
	ctx := context.Background()
	bound := Context("abc123", "7", "drive")

	sealed, err := s.Seal(ctx, PurposeToken, bound, []byte("refresh-token"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("refresh-token")) {
		t.Fatal("sealed value carries the plain text")
	}
	plain, err := s.Open(ctx, PurposeToken, bound, sealed)
	if err != nil || string(plain) != "refresh-token" {
		t.Fatalf("open: %q, %v", plain, err)
	}

	again, _ := s.Seal(ctx, PurposeToken, bound, []byte("refresh-token"))
	if bytes.Equal(sealed, again) {
		t.Fatal("sealing twice gave the same bytes")
	}
}

func TestSealBinding(t *testing.T) {
	s := testSealer(1)
	ctx := context.Background()
	bound := Context("abc123", "7", "drive")
	sealed, _ := s.Seal(ctx, PurposeToken, bound, []byte("refresh-token"))

	cases := map[string]func() ([]byte, error){
		"another row":     func() ([]byte, error) { return s.Open(ctx, PurposeToken, Context("abc124", "7", "drive"), sealed) },
		"another user":    func() ([]byte, error) { return s.Open(ctx, PurposeToken, Context("abc123", "8", "drive"), sealed) },
		"another purpose": func() ([]byte, error) { return s.Open(ctx, PurposeGrant, bound, sealed) },
		"another key":     func() ([]byte, error) { return testSealer(2).Open(ctx, PurposeToken, bound, sealed) },
		"changed bytes": func() ([]byte, error) {
			broken := append([]byte{}, sealed...)
			broken[len(broken)-1] ^= 1
			return s.Open(ctx, PurposeToken, bound, broken)
		},
		"unknown version": func() ([]byte, error) {
			broken := append([]byte{}, sealed...)
			broken[0] = 9
			return s.Open(ctx, PurposeToken, bound, broken)
		},
		"too short": func() ([]byte, error) { return s.Open(ctx, PurposeToken, bound, sealed[:5]) },
	}
	for name, open := range cases {
		if plain, err := open(); !errors.Is(err, ErrSealed) {
			t.Errorf("%s: got %q, %v; want ErrSealed", name, plain, err)
		}
	}
}

// Fields with a shifted border must not give the same context.
func TestContextIsUnambiguous(t *testing.T) {
	if bytes.Equal(Context("ab", "c"), Context("a", "bc")) {
		t.Fatal("contexts collide")
	}
	if bytes.Equal(Context("a", ""), Context("", "a")) {
		t.Fatal("contexts collide")
	}
}

func TestCheckPath(t *testing.T) {
	good := map[string]string{
		"":                    "",
		"/":                   "",
		"/a":                  "a",
		"/Docs/b ä #1.txt/":   "Docs/b ä #1.txt",
		"a/..b/c..":           "a/..b/c..",
		"/a:b*c?\"<>|":        "a:b*c?\"<>|",
		"/Dir %20/file+x.txt": "Dir %20/file+x.txt",
	}
	for in, want := range good {
		got, err := CheckPath(in)
		if err != nil || got != want {
			t.Errorf("CheckPath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}

	bad := []string{"/a/../b", "/..", "/a/./b", "/a//b", "a\\b", "/a/b\x00", "/a\nb", "/a/..", "../a", "/a/\x7f"}
	for _, in := range bad {
		if got, err := CheckPath(in); !errors.Is(err, ErrBadPath) {
			t.Errorf("CheckPath(%q) = %q, %v; want ErrBadPath", in, got, err)
		}
	}
}

func TestIsBelow(t *testing.T) {
	cases := []struct {
		path, dir string
		want      bool
	}{
		{"a/b", "a", true}, {"a", "a", true}, {"ab", "a", false}, {"a", "a/b", false}, {"x/y", "", true},
	}
	for _, c := range cases {
		if got := IsBelow(c.path, c.dir); got != c.want {
			t.Errorf("IsBelow(%q, %q) = %v", c.path, c.dir, got)
		}
	}
}

func TestIDs(t *testing.T) {
	file := func(id, path, version string) *Row {
		return &Row{Item: provider.Item{ID: id, Version: version}, Path: path}
	}

	if FileID("c1", file("item1", "a/b", "v1")) != FileID("c1", file("item1", "moved/b", "v2")) {
		t.Error("the id changed with the path or the content although the item is the same")
	}
	if FileID("c1", file("item1", "a", "v1")) == FileID("c2", file("item1", "a", "v1")) {
		t.Error("two connections share an id")
	}
	// A provider id that looks like a path must not collide with a path.
	if FileID("c1", file("a/b", "x", "")) == FileID("c1", &Row{Path: "a/b", ViaLink: true}) {
		t.Error("id and path collide")
	}

	// The same item reached directly and through a link shows as two rows.
	direct := file("item1", "Target/a.txt", "v1")
	through := file("item1", "Shortcut/a.txt", "v1")
	through.ViaLink = true
	if FileID("c1", direct) == FileID("c1", through) {
		t.Error("an item reached two ways has one id")
	}
	// A link has its own id, not the one of what it points to.
	link := &Row{Item: provider.Item{ID: "link1", Link: &provider.Link{TargetID: "item1"}}, Path: "l", ViaLink: true}
	if FileID("c1", link) == FileID("c1", direct) {
		t.Error("a link shares the id of its target")
	}

	base := VersionID("c1", file("item1", "a", "v1"))
	if base != VersionID("c1", file("item1", "renamed", "v1")) {
		t.Error("the version changed without the content changing")
	}
	if base == VersionID("c1", file("item1", "a", "v2")) {
		t.Error("the version did not follow the provider's marker")
	}
	if base == VersionID("c1", file("item2", "a", "v1")) {
		t.Error("another item with the same marker has the same version")
	}
	if VersionID("c1", file("item1", "a", "")) != "" || VersionID("c1", &Row{Item: provider.Item{ID: "d", IsDir: true, Version: "x"}}) != "" {
		t.Error("a version without a marker, or for a folder")
	}
	if len(base) != 25 || base[0] != 'v' || len(FileID("c1", direct)) != 26 {
		t.Errorf("unexpected shape: %q %q", base, FileID("c1", direct))
	}
}

// One slash at each end of a path is how addresses are written; more are empty names.
func TestCheckPathEdges(t *testing.T) {
	for raw, want := range map[string]string{"": "", "/": "", "/Docs": "Docs", "/Docs/": "Docs", "Docs/a b": "Docs/a b"} {
		got, err := CheckPath(raw)
		if err != nil || got != want {
			t.Errorf("%q: %q, %v", raw, got, err)
		}
	}
	for _, raw := range []string{"//", "//Docs", "/Docs//", "/Docs//a", "/Docs/../a"} {
		if _, err := CheckPath(raw); err == nil {
			t.Errorf("%q was accepted", raw)
		}
	}
}
