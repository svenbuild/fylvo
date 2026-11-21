package workflows

import (
	"path"
	"regexp"
	"strings"

	"github.com/samber/lo"

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
		if !o.IsDir && lo.SomeBy(thumbnailOriginals(o.RelativePath), func(p string) bool { return files[p] }) {
			continue
		}
		kept = append(kept, o)
	}
	return kept
}

// thumbnailOriginals returns the paths of the file a preview image may belong to, none for other
// names. Fylvo: newer previews carry a random prefix ("Ab3dEf9h_photo.jpg._thumb", see
// util.UniqueThumbPath), older ones do not ("photo.jpg._thumb").
func thumbnailOriginals(p string) []string {
	for _, suffix := range importSkippedSuffixes {
		if strings.HasSuffix(p, suffix) {
			original := strings.TrimSuffix(p, suffix)
			candidates := []string{original}
			dir, name := path.Split(original)
			// Only previews get the prefix, side files ("._thumb_sidecar") never.
			if suffix == "._thumb" && len(name) > 9 && thumbPrefix.MatchString(name) {
				candidates = append(candidates, dir+name[9:])
			}
			return candidates
		}
	}
	return nil
}

// thumbPrefix is the random prefix util.UniqueThumbPath puts in front of a preview's name.
var thumbPrefix = regexp.MustCompile(`^[0-9A-Za-z]{8}_`)
