package workflows

import (
	"errors"
	"fmt"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/inventory"

	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/stretchr/testify/require"
)

func TestImportSkipsOwnThumbnails(t *testing.T) {
	objects := []fs.PhysicalObject{
		{RelativePath: "a/photo.jpg"},
		{RelativePath: "a/photo.jpg._thumb"},
		{RelativePath: "a/photo.jpg._thumb_sidecar"},
		{RelativePath: "a/lonely.txt._thumb"}, // no original next to it: a real file
		{RelativePath: "b/photo.jpg._thumb"},  // original is in another folder
		{RelativePath: "folder._thumb", IsDir: true},
	}

	var paths []string
	for _, o := range withoutThumbnails(objects) {
		paths = append(paths, o.RelativePath)
	}
	require.Equal(t, []string{"a/photo.jpg", "a/lonely.txt._thumb", "b/photo.jpg._thumb", "folder._thumb"}, paths)
}

func TestImportListsCaseTwins(t *testing.T) {
	state := &ImportTaskState{}
	require.False(t, state.skipCaseTwin("a/x.jpg", errors.New("disk full")))
	require.True(t, state.skipCaseTwin("a/A.jpg", fmt.Errorf("failed: %w", inventory.ErrNameCaseTaken)))
	for i := 0; i < maxListedCaseTwins+5; i++ {
		state.skipCaseTwin(fmt.Sprintf("b/%d", i), inventory.ErrNameCaseTaken)
	}
	require.Equal(t, maxListedCaseTwins+6, state.CaseTwinCount)
	require.Len(t, state.CaseTwins, maxListedCaseTwins)
	require.Equal(t, "a/A.jpg", state.CaseTwins[0])
}
