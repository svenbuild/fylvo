package workflows

import (
	"strings"

	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
)

// Fylvo: a folder from Linux can hold names that differ only in upper and lower case. Fylvo
// takes the first of them and skips the others (and what is in a skipped folder); the task
// lists them, so they can be renamed on disk and imported again.
const (
	SummaryKeyCaseTwins     = "case_twins"
	SummaryKeyCaseTwinCount = "case_twin_count"
	// maxListedCaseTwins is how many skipped paths the task keeps; the count has all of them.
	maxListedCaseTwins = 100
)

// skipCaseTwin notes path as skipped if err refused its name for another spelling in the
// folder, and reports whether it did.
func (s *ImportTaskState) skipCaseTwin(path string, err error) bool {
	if !inventory.IsNameCaseTaken(err) {
		return false
	}
	s.CaseTwinCount++
	if len(s.CaseTwins) < maxListedCaseTwins {
		s.CaseTwins = append(s.CaseTwins, path)
	}
	return true
}

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
