package util

import "path"

// UniqueThumbPath puts a random token in front of the file name of a thumbnail save path (Fylvo).
// Thumbnail paths used to be derived from the source file only, so two generations for the same
// file at the same time wrote to one path, and the clean-up of the one that failed deleted the
// file of the one that succeeded. The name still ends like before (e.g. "._thumb").
// A name that would grow past 255 bytes (the usual limit of one file name) stays as it was.
func UniqueThumbPath(p string) string {
	dir, name := path.Split(p)
	if len(name)+9 > 255 {
		return p
	}
	return dir + RandStringRunes(8) + "_" + name
}
