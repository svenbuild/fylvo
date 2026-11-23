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
// the list of kinds and reachable from the root, with the parts each kind needs and the
// references between them. Whether Word, Excel and PowerPoint open them without asking
// to repair them only those programs can say: with FYLVO_BLANK_OUT set to a folder the
// documents are written there for that.
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

		// What a part names by an id is in its own list of references, as the kind
		// of part it is meant to be.
		refers := func(part, id, kind, target string) {
			t.Helper()
			list := filepath.ToSlash(filepath.Join(filepath.Dir(part), "_rels", filepath.Base(part)+".rels"))
			require.Contains(t, parts[part], `r:id="`+id+`"`, name+": "+part)
			require.Contains(t, parts[list], `<Relationship Id="`+id+`" Type="`+docRel+"/"+kind+`" Target="`+target+`"/>`, name+": "+list)
		}
		switch main {
		case "word/document.xml":
			require.Contains(t, parts[main], "<w:body><w:p></w:p><w:sectPr>")
			require.True(t, strings.HasSuffix(parts[main], "</w:sectPr></w:body></w:document>"), "the page setup is the last thing in the body")
		case "xl/workbook.xml":
			require.Contains(t, parts[main], `<sheet name="Sheet1" sheetId="1" r:id="rId1"/>`)
			refers(main, "rId1", "worksheet", "worksheets/sheet1.xml")
			require.Contains(t, parts["xl/worksheets/sheet1.xml"], "<sheetData/>")
		case "ppt/presentation.xml":
			refers(main, "rId1", "slideMaster", "slideMasters/slideMaster1.xml")
			refers(main, "rId2", "slide", "slides/slide1.xml")
			require.Contains(t, parts["ppt/_rels/presentation.xml.rels"], `Type="`+docRel+`/theme" Target="theme/theme1.xml"`)
			refers("ppt/slideMasters/slideMaster1.xml", "rId1", "slideLayout", "../slideLayouts/slideLayout1.xml")
			require.Contains(t, parts["ppt/slideMasters/_rels/slideMaster1.xml.rels"], `Type="`+docRel+`/theme" Target="../theme/theme1.xml"`)
			require.Contains(t, parts["ppt/slideLayouts/_rels/slideLayout1.xml.rels"], `Type="`+docRel+`/slideMaster" Target="../slideMasters/slideMaster1.xml"`)
			require.Contains(t, parts["ppt/slides/_rels/slide1.xml.rels"], `Type="`+docRel+`/slideLayout" Target="../slideLayouts/slideLayout1.xml"`)
			// The order PowerPoint wants in a master, and the lists of a theme, each
			// with its three steps.
			master := parts["ppt/slideMasters/slideMaster1.xml"]
			order := []string{"<p:cSld>", "<p:clrMap ", "<p:sldLayoutIdLst>", "<p:txStyles>"}
			for i := 1; i < len(order); i++ {
				require.Less(t, strings.Index(master, order[i-1]), strings.Index(master, order[i]), order[i])
				require.GreaterOrEqual(t, strings.Index(master, order[i-1]), 0, order[i-1])
			}
			theme := parts["ppt/theme/theme1.xml"]
			for list, entry := range map[string]string{
				"a:fillStyleLst": "<a:solidFill>", "a:lnStyleLst": "<a:ln ", "a:effectStyleLst": "<a:effectStyle>", "a:bgFillStyleLst": "<a:solidFill>",
			} {
				start, end := strings.Index(theme, "<"+list+">"), strings.Index(theme, "</"+list+">")
				require.True(t, start >= 0 && end > start, list)
				count := strings.Count(theme[start:end], entry)
				if list == "a:lnStyleLst" {
					// Each line has a fill of its own.
					count = strings.Count(theme[start:end], "<a:ln ")
				}
				require.Equal(t, 3, count, list)
			}
			for _, colour := range []string{"dk1", "lt1", "dk2", "lt2", "accent1", "accent2", "accent3", "accent4", "accent5", "accent6", "hlink", "folHlink"} {
				require.Contains(t, theme, "<a:"+colour+">", colour)
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
