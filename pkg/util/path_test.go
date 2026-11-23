package util

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestFillSlash(t *testing.T) {
	asserts := assert.New(t)
	asserts.Equal("/", FillSlash("/"))
	asserts.Equal("/", FillSlash(""))
	asserts.Equal("/123/", FillSlash("/123"))
}

func TestRemoveSlash(t *testing.T) {
	asserts := assert.New(t)
	asserts.Equal("/", RemoveSlash("/"))
	asserts.Equal("/123/1236", RemoveSlash("/123/1236"))
	asserts.Equal("/123/1236", RemoveSlash("/123/1236/"))
}
