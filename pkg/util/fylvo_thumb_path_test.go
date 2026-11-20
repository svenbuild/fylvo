package util

import (
	"path"
	"strings"
	"testing"
)

func TestUniqueThumbPath(t *testing.T) {
	p := "uploads/1/1_abc_image.jpeg._thumb"
	a, okA := UniqueThumbPath(p)
	b, okB := UniqueThumbPath(p)
	if !okA || !okB || a == b {
		t.Fatalf("two calls gave the same path %q", a)
	}
	for _, got := range []string{a, b} {
		if path.Dir(got) != "uploads/1" {
			t.Errorf("folder changed: %q", got)
		}
		if !strings.HasSuffix(got, "_1_abc_image.jpeg._thumb") {
			t.Errorf("name does not end like the original: %q", got)
		}
	}
}

func TestUniqueThumbPathTooLong(t *testing.T) {
	if _, ok := UniqueThumbPath("uploads/" + strings.Repeat("a", 247)); ok {
		t.Fatal("a name that would pass 255 bytes was accepted")
	}
	if _, ok := UniqueThumbPath("uploads/" + strings.Repeat("a", 246)); !ok {
		t.Fatal("a name of exactly 255 bytes with the prefix was refused")
	}
}
