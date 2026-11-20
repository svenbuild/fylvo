package workflows

import (
	"strings"

	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
)

// Fylvo: preview images of imported files are stored next to the originals
// (setting thumb_entity_suffix "{blob_path}/{blob_name}._thumb"). A second or resumed import of
// the same folder must not take them for files of the user. Only a file whose original is right
// next to it counts as a preview image; any other name is imported as it is.
var importSkippedSuffixes = []string{"._thumb_sidecar", "._thumb"}

func withoutThumbnails(objects []fs.PhysicalObject) []fs.PhysicalObject {
	files := make(map[string]bool, len(objects))
	for _, o := range objects {
		if !o.IsDir {
			files[o.RelativePath] = true
		}
	}

	kept := objects[:0]
	for _, o := range objects {
		if !o.IsDir && files[thumbnailOriginal(o.RelativePath)] {
			continue
		}
		kept = append(kept, o)
	}
	return kept
}

// thumbnailOriginal returns the path of the file a preview image belongs to, "" for other names.
func thumbnailOriginal(path string) string {
	for _, suffix := range importSkippedSuffixes {
		if strings.HasSuffix(path, suffix) {
			return strings.TrimSuffix(path, suffix)
		}
	}
	return ""
}
