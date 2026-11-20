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
		{RelativePath: "c/photo.jpg"},
		{RelativePath: "c/Ab3dEf9h_photo.jpg._thumb"}, // Fylvo: newer previews carry a random prefix
		{RelativePath: "c/Zz9yXw8v_other.jpg._thumb"}, // prefix, but no original: a real file
		{RelativePath: "c/Ab3dEf9h_photo.jpg._thumb_sidecar"}, // side files never get the prefix
		{RelativePath: "d/\nx.jpg"},
		{RelativePath: "d/Ab3dEf9h_\nx.jpg._thumb"}, // original name starting with a line break
	}

	var paths []string
	for _, o := range withoutThumbnails(objects) {
		paths = append(paths, o.RelativePath)
	}
	require.Equal(t, []string{"a/photo.jpg", "a/lonely.txt._thumb", "b/photo.jpg._thumb", "folder._thumb", "c/photo.jpg", "c/Zz9yXw8v_other.jpg._thumb", "c/Ab3dEf9h_photo.jpg._thumb_sidecar", "d/\nx.jpg"}, paths)
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
