package util

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExists(t *testing.T) {
	asserts := assert.New(t)
	asserts.True(Exists("io_test.go"))
	asserts.False(Exists("io_test.js"))
}

func TestCreatNestedFile(t *testing.T) {
	asserts := assert.New(t)
	dir := filepath.Join(t.TempDir(), "test")

	// 父目录不存在
	{
		file, err := CreatNestedFile(filepath.Join(dir, "nest.txt"))
		asserts.NoError(err)
		asserts.NoError(file.Close())
		asserts.FileExists(filepath.Join(dir, "nest.txt"))
	}

	// 父目录存在
	{
		file, err := CreatNestedFile(filepath.Join(dir, "direct.txt"))
		asserts.NoError(err)
		asserts.NoError(file.Close())
		asserts.FileExists(filepath.Join(dir, "direct.txt"))
	}
}
