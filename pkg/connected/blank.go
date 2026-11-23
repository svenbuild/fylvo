package connected

import (
	"archive/zip"
	"bytes"
	"html"
	"strings"
)

// Empty Office documents.
//
// A new file in a connected storage is created at the provider, and there a document of
// zero bytes is one that Word, Excel and PowerPoint refuse to open. So a new ".docx",
// ".xlsx" or ".pptx" is created as a real, empty document: the smallest package of parts
// each of the three programs opens. They are put together here; no file of them is
// shipped.

const (
	xmlHead       = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"
	relsType      = "application/vnd.openxmlformats-package.relationships+xml"
	relsNS        = "http://schemas.openxmlformats.org/package/2006/relationships"
	typesNS       = "http://schemas.openxmlformats.org/package/2006/content-types"
	docRel        = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	docType       = "application/vnd.openxmlformats-officedocument."
	wordNS        = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	sheetNS       = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
	slideNS       = "http://schemas.openxmlformats.org/presentationml/2006/main"
	drawNS        = "http://schemas.openxmlformats.org/drawingml/2006/main"
	emptyTree     = `<p:cSld><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr/></p:spTree></p:cSld>`
	slideSpaces   = ` xmlns:a="` + drawNS + `" xmlns:r="` + docRel + `" xmlns:p="` + slideNS + `"`
	inheritColors = `<p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr>`
)

// part is one file of a package: its name and its content.
type part struct{ name, content string }

// pack puts the parts of a document together. The first part of every package is the
// list of the kinds of its parts, made here from overrides (part name, content type).
func pack(overrides [][2]string, parts []part) []byte {
	var types strings.Builder
	types.WriteString(xmlHead + `<Types xmlns="` + typesNS + `">` +
		`<Default Extension="rels" ContentType="` + relsType + `"/>` +
		`<Default Extension="xml" ContentType="application/xml"/>`)
	for _, o := range overrides {
		types.WriteString(`<Override PartName="` + o[0] + `" ContentType="` + docType + o[1] + `"/>`)
	}
	types.WriteString(`</Types>`)

	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for _, p := range append([]part{{"[Content_Types].xml", types.String()}}, parts...) {
		// Writing into memory does not fail.
		w, _ := z.Create(p.name)
		_, _ = w.Write([]byte(p.content))
	}
	_ = z.Close()
	return buf.Bytes()
}

// rels is the list of what a part refers to: (id, kind, target).
func rels(list ...[3]string) string {
	var b strings.Builder
	b.WriteString(xmlHead + `<Relationships xmlns="` + relsNS + `">`)
	for _, r := range list {
		b.WriteString(`<Relationship Id="` + r[0] + `" Type="` + docRel + "/" + r[1] + `" Target="` + r[2] + `"/>`)
	}
	b.WriteString(`</Relationships>`)
	return b.String()
}

// wordDocument is a Word document with one paragraph of the given text.
func wordDocument(text string) []byte {
	run := ""
	if text != "" {
		run = `<w:r><w:t xml:space="preserve">` + html.EscapeString(text) + `</w:t></w:r>`
	}
	return pack(
		[][2]string{{"/word/document.xml", "wordprocessingml.document.main+xml"}},
		[]part{
			{"_rels/.rels", rels([3]string{"rId1", "officeDocument", "word/document.xml"})},
			{"word/document.xml", xmlHead + `<w:document xmlns:w="` + wordNS + `"><w:body><w:p>` + run + `</w:p>` +
				`<w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1417" w:right="1417" w:bottom="1134" w:left="1417" w:header="708" w:footer="708" w:gutter="0"/></w:sectPr>` +
				`</w:body></w:document>`},
		})
}

// workbook is an Excel workbook with one empty sheet.
func workbook() []byte {
	return pack(
		[][2]string{
			{"/xl/workbook.xml", "spreadsheetml.sheet.main+xml"},
			{"/xl/worksheets/sheet1.xml", "spreadsheetml.worksheet+xml"},
		},
		[]part{
			{"_rels/.rels", rels([3]string{"rId1", "officeDocument", "xl/workbook.xml"})},
			{"xl/workbook.xml", xmlHead + `<workbook xmlns="` + sheetNS + `" xmlns:r="` + docRel + `">` +
				`<sheets><sheet name="Sheet1" sheetId="1" r:id="rId1"/></sheets></workbook>`},
			{"xl/_rels/workbook.xml.rels", rels([3]string{"rId1", "worksheet", "worksheets/sheet1.xml"})},
			{"xl/worksheets/sheet1.xml", xmlHead + `<worksheet xmlns="` + sheetNS + `"><sheetData/></worksheet>`},
		})
}

// theme is the look a presentation needs to have one at all: colours, two fonts, and
// three steps each of fills, lines, effects and backgrounds.
func theme() string {
	colour := func(name, hex string) string {
		return `<a:` + name + `><a:srgbClr val="` + hex + `"/></a:` + name + `>`
	}
	font := func(kind string) string {
		return `<a:` + kind + `><a:latin typeface="Calibri"/><a:ea typeface=""/><a:cs typeface=""/></a:` + kind + `>`
	}
	fill := `<a:solidFill><a:schemeClr val="phClr"/></a:solidFill>`
	line := func(width string) string {
		return `<a:ln w="` + width + `"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:prstDash val="solid"/></a:ln>`
	}
	effect := `<a:effectStyle><a:effectLst/></a:effectStyle>`
	return xmlHead + `<a:theme xmlns:a="` + drawNS + `" name="Office"><a:themeElements>` +
		`<a:clrScheme name="Office">` +
		`<a:dk1><a:sysClr val="windowText" lastClr="000000"/></a:dk1><a:lt1><a:sysClr val="window" lastClr="FFFFFF"/></a:lt1>` +
		colour("dk2", "44546A") + colour("lt2", "E7E6E6") +
		colour("accent1", "4472C4") + colour("accent2", "ED7D31") + colour("accent3", "A5A5A5") +
		colour("accent4", "FFC000") + colour("accent5", "5B9BD5") + colour("accent6", "70AD47") +
		colour("hlink", "0563C1") + colour("folHlink", "954F72") +
		`</a:clrScheme>` +
		`<a:fontScheme name="Office">` + font("majorFont") + font("minorFont") + `</a:fontScheme>` +
		`<a:fmtScheme name="Office">` +
		`<a:fillStyleLst>` + fill + fill + fill + `</a:fillStyleLst>` +
		`<a:lnStyleLst>` + line("6350") + line("12700") + line("19050") + `</a:lnStyleLst>` +
		`<a:effectStyleLst>` + effect + effect + effect + `</a:effectStyleLst>` +
		`<a:bgFillStyleLst>` + fill + fill + fill + `</a:bgFillStyleLst>` +
		`</a:fmtScheme></a:themeElements><a:objectDefaults/><a:extraClrSchemeLst/></a:theme>`
}

// presentation is a PowerPoint presentation with one empty slide.
func presentation() []byte {
	return pack(
		[][2]string{
			{"/ppt/presentation.xml", "presentationml.presentation.main+xml"},
			{"/ppt/slideMasters/slideMaster1.xml", "presentationml.slideMaster+xml"},
			{"/ppt/slideLayouts/slideLayout1.xml", "presentationml.slideLayout+xml"},
			{"/ppt/slides/slide1.xml", "presentationml.slide+xml"},
			{"/ppt/theme/theme1.xml", "theme+xml"},
		},
		[]part{
			{"_rels/.rels", rels([3]string{"rId1", "officeDocument", "ppt/presentation.xml"})},
			{"ppt/presentation.xml", xmlHead + `<p:presentation` + slideSpaces + `>` +
				`<p:sldMasterIdLst><p:sldMasterId id="2147483648" r:id="rId1"/></p:sldMasterIdLst>` +
				`<p:sldIdLst><p:sldId id="256" r:id="rId2"/></p:sldIdLst>` +
				`<p:sldSz cx="12192000" cy="6858000"/><p:notesSz cx="6858000" cy="9144000"/>` +
				`</p:presentation>`},
			{"ppt/_rels/presentation.xml.rels", rels(
				[3]string{"rId1", "slideMaster", "slideMasters/slideMaster1.xml"},
				[3]string{"rId2", "slide", "slides/slide1.xml"},
				[3]string{"rId3", "theme", "theme/theme1.xml"})},
			{"ppt/slideMasters/slideMaster1.xml", xmlHead + `<p:sldMaster` + slideSpaces + `>` + emptyTree +
				`<p:clrMap bg1="lt1" tx1="dk1" bg2="lt2" tx2="dk2" accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" hlink="hlink" folHlink="folHlink"/>` +
				`<p:sldLayoutIdLst><p:sldLayoutId id="2147483649" r:id="rId1"/></p:sldLayoutIdLst>` +
				// The schema does without the text styles; PowerPoint writes them into
				// every master, so they are there, empty.
				`<p:txStyles><p:titleStyle/><p:bodyStyle/><p:otherStyle/></p:txStyles>` +
				`</p:sldMaster>`},
			{"ppt/slideMasters/_rels/slideMaster1.xml.rels", rels(
				[3]string{"rId1", "slideLayout", "../slideLayouts/slideLayout1.xml"},
				[3]string{"rId2", "theme", "../theme/theme1.xml"})},
			{"ppt/slideLayouts/slideLayout1.xml", xmlHead + `<p:sldLayout` + slideSpaces + ` type="blank" preserve="1">` +
				strings.Replace(emptyTree, `<p:cSld>`, `<p:cSld name="Blank">`, 1) + inheritColors + `</p:sldLayout>`},
			{"ppt/slideLayouts/_rels/slideLayout1.xml.rels", rels([3]string{"rId1", "slideMaster", "../slideMasters/slideMaster1.xml"})},
			{"ppt/slides/slide1.xml", xmlHead + `<p:sld` + slideSpaces + `>` + emptyTree + inheritColors + `</p:sld>`},
			{"ppt/slides/_rels/slide1.xml.rels", rels([3]string{"rId1", "slideLayout", "../slideLayouts/slideLayout1.xml"})},
			{"ppt/theme/theme1.xml", theme()},
		})
}

// BlankDocument returns an empty Office document for a file of the given name, by its
// extension. ok is false for every other kind of file: those are created empty.
func BlankDocument(name string) (content []byte, ok bool) {
	dot := strings.LastIndex(name, ".")
	if dot < 0 {
		return nil, false
	}
	switch strings.ToLower(name[dot+1:]) {
	case "docx":
		return wordDocument(""), true
	case "xlsx":
		return workbook(), true
	case "pptx":
		return presentation(), true
	}
	return nil, false
}
