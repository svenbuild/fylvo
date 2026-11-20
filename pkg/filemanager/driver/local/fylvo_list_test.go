package local

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/stretchr/testify/require"
)

// Fylvo: listing a folder for the import.

func TestListOfMissingFolderFails(t *testing.T) {
	d := &Driver{l: logging.NewConsoleLogger(logging.LevelError)}
	_, err := d.List(context.Background(), filepath.Join(t.TempDir(), "missing"), func(int) {}, true)
	require.Error(t, err)
}

func TestListGivesDatesOnDisk(t *testing.T) {
	root := t.TempDir()
	date := time.Date(2012, 3, 4, 5, 6, 7, 0, time.UTC)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "Ordner"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "Ordner", "a.txt"), []byte("abc"), 0o644))
	require.NoError(t, os.Chtimes(filepath.Join(root, "Ordner", "a.txt"), date, date))
	require.NoError(t, os.Chtimes(filepath.Join(root, "Ordner"), date.Add(time.Hour), date.Add(time.Hour)))

	d := &Driver{l: logging.NewConsoleLogger(logging.LevelError)}
	objects, err := d.List(context.Background(), root, func(int) {}, true)
	require.NoError(t, err)
	require.Len(t, objects, 2)
	require.Equal(t, "Ordner", objects[0].RelativePath)
	require.True(t, objects[0].IsDir)
	require.True(t, objects[0].LastModify.Equal(date.Add(time.Hour)))
	require.Equal(t, "Ordner/a.txt", objects[1].RelativePath)
	require.Equal(t, int64(3), objects[1].Size)
	require.True(t, objects[1].LastModify.Equal(date))
}

func TestListOfAFileFails(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a.txt")
	require.NoError(t, os.WriteFile(file, []byte("abc"), 0o644))

	d := &Driver{l: logging.NewConsoleLogger(logging.LevelError)}
	_, err := d.List(context.Background(), file, func(int) {}, true)
	require.Error(t, err)
}
