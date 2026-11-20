package util

import (
	"path"
	"strings"
	"testing"
)

func TestUniqueThumbPath(t *testing.T) {
	p := "uploads/1/1_abc_image.jpeg._thumb"
	a, b := UniqueThumbPath(p), UniqueThumbPath(p)
	if a == b {
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
