package statics

import (
	"compress/gzip"
	"io"
	"io/fs"
	"strings"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/stretchr/testify/require"
)

// Every packed file of the web app comes out of Gzip as exactly what ReadFile gives.
func TestGzipGivesTheFileContent(t *testing.T) {
	embedded := NewStaticFS(logging.NewConsoleLogger(logging.LevelError)).(FS)
	checked := 0
	err := fs.WalkDir(embedded, ".", func(name string, d fs.DirEntry, err error) error {
		require.NoError(t, err)
		if d.IsDir() || !strings.HasSuffix(name, ".js") {
			return nil
		}
		content, _, ok := embedded.Gzip(name)
		if !ok {
			return nil
		}
		zr, err := gzip.NewReader(content)
		require.NoError(t, err, name)
		unpacked, err := io.ReadAll(zr)
		require.NoError(t, err, name)
		want, err := embedded.ReadFile(name)
		require.NoError(t, err, name)
		require.Equal(t, want, unpacked, name)
		checked++
		return nil
	})
	require.NoError(t, err)
	require.NotZero(t, checked)

	_, _, ok := embedded.Gzip("assets/build/missing.js")
	require.False(t, ok)
}
