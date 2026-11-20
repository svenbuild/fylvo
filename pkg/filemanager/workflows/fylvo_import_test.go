package workflows

import (
	"testing"

	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/stretchr/testify/require"
)

func TestImportSkipsOwnThumbnails(t *testing.T) {
	objects := []fs.PhysicalObject{
		{Name: "photo.jpg"},
		{Name: "photo.jpg._thumb"},
		{Name: "photo.jpg._thumb_sidecar"},
		{Name: "folder._thumb", IsDir: true},
		{Name: "_thumb.txt"},
	}

	var names []string
	for _, o := range withoutThumbnails(objects) {
		names = append(names, o.Name)
	}
	require.Equal(t, []string{"photo.jpg", "folder._thumb", "_thumb.txt"}, names)
}
