package connected

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
)

// ErrBadPath is returned for a path that is not a plain list of names.
var ErrBadPath = errors.New("invalid path")

// CheckPath takes the decoded path of an address and returns it relative to the
// root of the connection, without slashes at the ends. It refuses everything that
// is not a plain list of names. Nothing is cleaned up silently: "a/../b" is an
// error, not "b".
func CheckPath(raw string) (string, error) {
	// One slash at each end is how addresses are written. More of them are empty
	// names, like anywhere else in the path.
	if raw == "" || raw == "/" {
		return "", nil
	}
	p := strings.TrimSuffix(strings.TrimPrefix(raw, "/"), "/")
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

// Fold makes a name comparable where case does not count. Lower case alone is not
// enough: some letters have two small forms of one capital (the Greek sigma), and a
// provider that compares by capitals takes them for one. So the name goes through its
// capitals first.
func Fold(name string) string {
	return strings.ToLower(strings.ToUpper(name))
}

// FileID is the id clients see for a row, and send back to say which row they mean. It
// follows the provider's id of the entry, so it survives a rename or a move. A row that
// was reached through a link gets an id from its path instead: the same item reached two
// ways must show as two rows. It is not a hashid and no route accepts it as an address.
func FileID(connection string, row *Row) string {
	if row.ViaLink || row.ID == "" {
		return shortHash("cs", connection, "path", row.Path)
	}
	return shortHash("cs", connection, "id", row.ID)
}

// VersionID is what clients see as the version of a file. It is built from the item that
// holds the content and the provider's own marker for the state of that content, so it
// changes exactly when the provider says the content did. Empty when the provider gave
// no marker (a folder, a link whose target was not read).
func VersionID(connection string, row *Row) string {
	if row.IsDir || row.Version == "" {
		return ""
	}
	return shortHash("v", connection, row.ContentID(), row.Version)
}
