package connected

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
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
	when := time.Unix(1700000000, 0)

	if FileID("c1", "item1", "a/b") != FileID("c1", "item1", "moved/b") {
		t.Error("the id changed with the path although the provider id is the same")
	}
	if FileID("c1", "item1", "a") == FileID("c2", "item1", "a") {
		t.Error("two connections share an id")
	}
	if FileID("c1", "", "a/b") == FileID("c1", "", "a/c") {
		t.Error("two paths share an id")
	}
	// A provider id that looks like a path must not collide with a path.
	if FileID("c1", "a/b", "x") == FileID("c1", "", "a/b") {
		t.Error("id and path collide")
	}

	md5a := map[string]string{"md5": "aaaa"}
	base := VersionID("c1", "item1", "a", 10, when, md5a)
	if base != VersionID("c1", "item1", "renamed", 10, when.Add(time.Hour), md5a) {
		t.Error("the version changed without the content changing")
	}
	if base == VersionID("c1", "item1", "a", 10, when, map[string]string{"md5": "bbbb"}) {
		t.Error("the version did not follow the hash")
	}
	if base == VersionID("c1", "item1", "a", 11, when, md5a) {
		t.Error("the version did not follow the size")
	}
	if base == VersionID("c1", "item2", "a", 10, when, md5a) {
		t.Error("another item with the same content has the same version")
	}

	noHash := VersionID("c1", "item1", "a", 10, when, nil)
	if noHash == VersionID("c1", "item1", "a", 10, when.Add(time.Second), map[string]string{"md5": ""}) {
		t.Error("without a hash the version did not follow the time")
	}
	if len(base) != 25 || base[0] != 'v' || len(FileID("c1", "x", "y")) != 26 {
		t.Errorf("unexpected shape: %q %q", base, FileID("c1", "x", "y"))
	}
}
