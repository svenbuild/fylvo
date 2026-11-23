package connected

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The empty Office documents are packages every part of which is well-formed, named in
// the list of kinds, and reachable from the root: what a program checks before it opens
// one. With FYLVO_BLANK_OUT set to a folder they are also written there, to be opened
// with the programs themselves.
func TestBlankDocuments(t *testing.T) {
	for name, main := range map[string]string{
		"new.docx": "word/document.xml", "New.XLSX": "xl/workbook.xml", "talk.v2.pptx": "ppt/presentation.xml",
	} {
		content, ok := BlankDocument(name)
		require.True(t, ok, name)
		z, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
		require.NoError(t, err, name)

		parts := map[string]string{}
		for _, f := range z.File {
			r, err := f.Open()
			require.NoError(t, err)
			raw, err := io.ReadAll(r)
			require.NoError(t, err)
			r.Close()
			parts[f.Name] = string(raw)
			// Well-formed, down to the last tag.
			d := xml.NewDecoder(bytes.NewReader(raw))
			for {
				if _, err := d.Token(); err == io.EOF {
					break
				} else {
					require.NoError(t, err, name+": "+f.Name)
				}
			}
		}
		require.Equal(t, "[Content_Types].xml", z.File[0].Name, "the list of kinds comes first")
		require.Contains(t, parts, main, name)
		require.Contains(t, parts["_rels/.rels"], `Target="`+main+`"`, name)

		// Every part but the lists themselves has its kind named, and every kind
		// that is named has its part.
		for part := range parts {
			if part == "[Content_Types].xml" || strings.HasSuffix(part, ".rels") {
				continue
			}
			require.Contains(t, parts["[Content_Types].xml"], `PartName="/`+part+`"`, name)
		}
		for _, o := range strings.Split(parts["[Content_Types].xml"], `PartName="/`)[1:] {
			require.Contains(t, parts, o[:strings.Index(o, `"`)], name)
		}
		// Every reference leads to a part of the package.
		for file, content := range parts {
			if !strings.HasSuffix(file, ".rels") {
				continue
			}
			base := filepath.ToSlash(filepath.Dir(filepath.Dir(file)))
			for _, r := range strings.Split(content, `Target="`)[1:] {
				target := filepath.ToSlash(filepath.Join(base, r[:strings.Index(r, `"`)]))
				require.Contains(t, parts, target, name+": "+file)
			}
		}

		if dir := os.Getenv("FYLVO_BLANK_OUT"); dir != "" {
			require.NoError(t, os.WriteFile(filepath.Join(dir, strings.ToLower(name)), content, 0o600))
		}
	}

	// The same bytes every time, and nothing for any other kind of file.
	first, _ := BlankDocument("a.docx")
	second, _ := BlankDocument("b.docx")
	require.Equal(t, first, second)
	for _, other := range []string{"notes.txt", "docx", "letter.doc", "sheet.xlsm", "", "a.docx.bak"} {
		_, ok := BlankDocument(other)
		require.False(t, ok, other)
	}
	// A document with a line of text keeps that line, also with characters of markup.
	z, err := zip.NewReader(bytes.NewReader(wordDocument(`A <b> & "c"`)), int64(len(wordDocument(`A <b> & "c"`))))
	require.NoError(t, err)
	for _, f := range z.File {
		if f.Name == "word/document.xml" {
			r, _ := f.Open()
			raw, _ := io.ReadAll(r)
			r.Close()
			require.Contains(t, string(raw), "A &lt;b&gt; &amp; &#34;c&#34;")
		}
	}
}
