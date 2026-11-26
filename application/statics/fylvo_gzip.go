package statics

import (
	"archive/zip"
	"encoding/binary"
	"io"
	"path"
	"strings"
	"time"
)

// Gzip returns the named file as a gzip stream and its time. The stream is the deflated
// data of the zip with a gzip header and trailer, so nothing is packed again. ok is false
// when there is no such file or the zip stores it unpacked.
func (f FS) Gzip(name string) (content io.ReadSeeker, modTime time.Time, ok bool) {
	file := f.lookup(name)
	if file == nil || file.zip == nil || file.zip.Method != zip.Deflate {
		return nil, time.Time{}, false
	}
	offset, err := file.zip.DataOffset()
	if err != nil {
		return nil, time.Time{}, false
	}
	var b strings.Builder
	b.Grow(10 + int(file.zip.CompressedSize64) + 8)
	b.WriteString("\x1f\x8b\x08\x00\x00\x00\x00\x00\x00\xff")
	b.WriteString(zipContent[offset : offset+int64(file.zip.CompressedSize64)])
	b.Write(binary.LittleEndian.AppendUint32(nil, file.zip.CRC32))
	b.Write(binary.LittleEndian.AppendUint32(nil, uint32(file.zip.UncompressedSize64)))
	return strings.NewReader(b.String()), file.modTime, true
}

// OpenGzip is Gzip for a path of the web app, when it is served from the embedded zip.
func (b *GinFS) OpenGzip(name string) (io.ReadSeeker, time.Time, bool) {
	if b.zipped.files == nil {
		return nil, time.Time{}, false
	}
	return b.zipped.Gzip(path.Join("assets/build", name))
}
