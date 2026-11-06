package docxfix

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// buildDocx 打包一个最小 docx，仅用于清洗逻辑测试。
func buildDocx(t *testing.T, documentXML string) []byte {
	t.Helper()
	parts := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="xml" ContentType="application/xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"/>`,
		"word/document.xml":   documentXML,
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range parts {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func readDocument(t *testing.T, data []byte) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	for _, f := range zr.File {
		if f.Name != "word/document.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open document.xml: %v", err)
		}
		defer rc.Close()
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(rc); err != nil {
			t.Fatalf("read document.xml: %v", err)
		}
		return buf.String()
	}
	t.Fatal("word/document.xml not found")
	return ""
}

const brokenCell = `<w:tcPr><w:tcW w:w="5000" w:type="dxa"/>` +
	`<a:lnL w="9525" cap="flat" cmpd="sng" algn="ctr"><a:solidFill><a:srgbClr val="DDE4EE"/></a:solidFill><a:prstDash val="solid"/></a:lnL>` +
	`<a:lnR w="9525" cap="flat" cmpd="sng" algn="ctr"><a:solidFill><a:srgbClr val="DDE4EE"/></a:solidFill><a:prstDash val="solid"/></a:lnR>` +
	`<a:lnT w="9525" cap="flat" cmpd="sng" algn="ctr"><a:solidFill><a:srgbClr val="DDE4EE"/></a:solidFill><a:prstDash val="solid"/></a:lnT>` +
	`<a:lnB w="9525" cap="flat" cmpd="sng" algn="ctr"><a:solidFill><a:srgbClr val="DDE4EE"/></a:solidFill><a:prstDash val="solid"/></a:lnB>` +
	`<w:vAlign w:val="center"/></w:tcPr>`

func TestSanitizeArtifactReplacesDrawingMLBorders(t *testing.T) {
	doc := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><w:body><w:tbl><w:tr><w:tc>` +
		brokenCell + `<w:p/></w:tc></w:tr></w:tbl></w:body></w:document>`

	fixed, n := SanitizeArtifact("报告.docx", buildDocx(t, doc))
	if n != 1 {
		t.Fatalf("修复数量 = %d，期望 1", n)
	}
	got := readDocument(t, fixed)
	if strings.Contains(got, "a:ln") {
		t.Fatalf("仍残留 DrawingML 边框: %s", got)
	}
	if !strings.Contains(got, "<w:tcBorders>") {
		t.Fatalf("未补上 w:tcBorders: %s", got)
	}
	if !strings.Contains(got, `w:sz="6"`) || !strings.Contains(got, `w:color="DDE4EE"`) {
		t.Fatalf("边框样式未按原样还原: %s", got)
	}
	// tcBorders 必须排在 vAlign 之前，否则 Word 判定节点顺序非法
	if strings.Index(got, "<w:tcBorders>") > strings.Index(got, "<w:vAlign") {
		t.Fatalf("tcBorders 插入位置错误: %s", got)
	}
}

func TestSanitizeArtifactKeepsExistingTcBorders(t *testing.T) {
	doc := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><w:body><w:tbl><w:tr><w:tc>` +
		`<w:tcPr><w:tcBorders><w:top w:val="single" w:sz="4" w:color="FF0000"/></w:tcBorders>` +
		`<a:lnL w="9525"><a:srgbClr val="DDE4EE"/></a:lnL></w:tcPr><w:p/></w:tc></w:tr></w:tbl></w:body></w:document>`

	fixed, n := SanitizeArtifact("报告.docx", buildDocx(t, doc))
	if n != 1 {
		t.Fatalf("修复数量 = %d，期望 1", n)
	}
	got := readDocument(t, fixed)
	if strings.Contains(got, "a:ln") {
		t.Fatalf("仍残留 DrawingML 边框: %s", got)
	}
	if strings.Count(got, "<w:tcBorders>") != 1 {
		t.Fatalf("不应重复插入 tcBorders: %s", got)
	}
	if !strings.Contains(got, `w:color="FF0000"`) {
		t.Fatalf("既有合法边框被覆盖: %s", got)
	}
}

func TestSanitizeArtifactLeavesCleanDocxUntouched(t *testing.T) {
	doc := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:tbl><w:tr><w:tc><w:tcPr><w:tcBorders><w:top w:val="single" w:sz="6" w:color="DDE4EE"/></w:tcBorders></w:tcPr><w:p/></w:tc></w:tr></w:tbl></w:body></w:document>`
	original := buildDocx(t, doc)
	fixed, n := SanitizeArtifact("报告.docx", original)
	if n != 0 {
		t.Fatalf("干净文件不应产生修复，得到 %d", n)
	}
	if !bytes.Equal(fixed, original) {
		t.Fatal("干净文件不应被重新打包")
	}
}

func TestSanitizeArtifactIgnoresOtherFormats(t *testing.T) {
	original := buildDocx(t, `<w:document></w:document>`)
	fixed, n := SanitizeArtifact("报告.pptx", original)
	if n != 0 || !bytes.Equal(fixed, original) {
		t.Fatal("非 docx 产物应原样返回")
	}
}
