package workflows

import (
	"strings"

	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
)

// Fylvo: preview images of imported files are stored next to the originals
// (setting thumb_entity_suffix "{blob_path}/{blob_name}._thumb"). A second or resumed import of
// the same folder must not take them for files of the user.
var importSkippedSuffixes = []string{"._thumb", "._thumb_sidecar"}

func withoutThumbnails(objects []fs.PhysicalObject) []fs.PhysicalObject {
	kept := objects[:0]
	for _, o := range objects {
		if !o.IsDir && hasThumbnailSuffix(o.Name) {
			continue
		}
		kept = append(kept, o)
	}
	return kept
}

func hasThumbnailSuffix(name string) bool {
	for _, suffix := range importSkippedSuffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}
