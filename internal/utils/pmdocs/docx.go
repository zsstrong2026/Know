package pmdocs

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"
)

// docxContentTypes Word 包内容类型：仅声明主文档，无需样式/编号部件
const docxContentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`

const docxRootRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`

// RenderDocx 生成最小可用 Word 文档：资料标题为一级标题，正文按 Markdown 结构排版
func RenderDocx(title, markdown string) ([]byte, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "项目资料"
	}
	var body strings.Builder
	body.WriteString(docxParagraph(title, 1))
	for _, b := range Parse(markdown) {
		// 正文首行常与文件标题重复，跳过避免出现两个同名大标题
		if b.Level == 1 && b.Text == title {
			continue
		}
		body.WriteString(docxParagraph(b.Text, b.Level))
	}

	document := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
		body.String() +
		`<w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440" w:header="720" w:footer="720" w:gutter="0"/></w:sectPr></w:body></w:document>`

	return zipPackage(map[string]string{
		"[Content_Types].xml": docxContentTypes,
		"_rels/.rels":         docxRootRels,
		"word/document.xml":   document,
	})
}

// docxParagraph 生成单段 OOXML，样式直接写在 run 上，不依赖 styles.xml
func docxParagraph(text string, level int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	size := 21 // 10.5pt，与 Word 正文默认字号一致
	bold := false
	indent := 0
	switch level {
	case 1:
		size, bold = 36, true
	case 2:
		size, bold = 32, true
	case 3:
		size, bold = 28, true
	case levelList:
		text = "• " + text
		indent = 360
	}
	var rPr strings.Builder
	rPr.WriteString(`<w:rFonts w:ascii="Arial" w:hAnsi="Arial" w:eastAsia="Microsoft YaHei"/>`)
	if bold {
		rPr.WriteString(`<w:b/>`)
	}
	fmt.Fprintf(&rPr, `<w:sz w:val="%d"/><w:szCs w:val="%d"/>`, size, size)

	pPr := `<w:spacing w:before="0" w:after="120"/>`
	if indent > 0 {
		pPr += fmt.Sprintf(`<w:ind w:left="%d"/>`, indent)
	}
	return `<w:p><w:pPr>` + pPr + `</w:pPr><w:r><w:rPr>` + rPr.String() +
		`</w:rPr><w:t xml:space="preserve">` + escapeXML(text) + `</w:t></w:r></w:p>`
}

// zipPackage 把部件写入 zip；OOXML 要求以未压缩/压缩均可，这里统一 Deflate
func zipPackage(files map[string]string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			return nil, fmt.Errorf("pmdocs: create zip entry %s: %w", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			return nil, fmt.Errorf("pmdocs: write zip entry %s: %w", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("pmdocs: close zip: %w", err)
	}
	return buf.Bytes(), nil
}

// escapeXML 转义文本节点，避免正文中的 & < > 破坏 XML 结构
func escapeXML(s string) string {
	var buf bytes.Buffer
	if err := xml.EscapeText(&buf, []byte(s)); err != nil {
		return s
	}
	return buf.String()
}
