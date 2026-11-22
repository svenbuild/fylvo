package connected

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ErrBadPath is returned for a path that is not a plain list of names.
var ErrBadPath = errors.New("invalid path")

// CheckPath takes the decoded path of an address and returns it relative to the
// root of the connection, without slashes at the ends. It refuses everything that
// is not a plain list of names. Nothing is cleaned up silently: "a/../b" is an
// error, not "b".
func CheckPath(raw string) (string, error) {
	p := strings.Trim(raw, "/")
	if p == "" {
		return "", nil
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." {
			return "", ErrBadPath
		}
		for _, c := range part {
			if c < 0x20 || c == 0x7f || c == '\\' {
				return "", ErrBadPath
			}
		}
	}
	return p, nil
}

// ParentOf returns the folder of a checked path and its last name.
func ParentOf(path string) (parent, name string) {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[:i], path[i+1:]
	}
	return "", path
}

// IsBelow tells whether path is dir itself or lies below it.
func IsBelow(path, dir string) bool {
	return dir == "" || path == dir || strings.HasPrefix(path, dir+"/")
}

func shortHash(prefix string, fields ...string) string {
	h := sha256.New()
	for _, f := range fields {
		h.Write([]byte(strconv.Itoa(len(f))))
		h.Write([]byte{':'})
		h.Write([]byte(f))
	}
	return prefix + hex.EncodeToString(h.Sum(nil))[:24]
}

// FileID is the id clients see for an entry. It follows the provider's item id,
// so it survives a rename or a move; backends without ids fall back to the path.
// It is not a hashid and no route accepts it.
func FileID(connection, providerID, path string) string {
	if providerID == "" {
		return shortHash("cs", connection, "path", path)
	}
	return shortHash("cs", connection, "id", providerID)
}

// VersionID is what clients see as the version of a file. It changes with the
// content: built from the provider's hashes and the size, and from size and
// modification time when the provider has no hash for the file.
func VersionID(connection, providerID, path string, size int64, modTime time.Time, hashes map[string]string) string {
	item := providerID
	if item == "" {
		item = "path:" + path
	}
	fields := []string{connection, item, strconv.FormatInt(size, 10)}

	kinds := make([]string, 0, len(hashes))
	for kind, value := range hashes {
		if value != "" {
			kinds = append(kinds, kind)
		}
	}
	if len(kinds) == 0 {
		fields = append(fields, "time", strconv.FormatInt(modTime.UnixMilli(), 10))
	} else {
		sort.Strings(kinds)
		for _, kind := range kinds {
			fields = append(fields, kind, hashes[kind])
		}
	}
	return shortHash("v", fields...)
}
