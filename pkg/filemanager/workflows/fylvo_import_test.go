package workflows

import (
	"testing"

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
