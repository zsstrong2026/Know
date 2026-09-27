package docassistant

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// RenderMarkdown 将文档渲染为 Markdown 文本
func (s *DocAssistantService) RenderMarkdown(ctx context.Context, doc *types.DocDocument) (string, error) {
	chapters, err := decodeDocChapters(doc)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# 文档 v%d\n\n", doc.Version))
	for _, ch := range chapters {
		if ch.Title != "" {
			sb.WriteString(fmt.Sprintf("\n## %s\n\n", ch.Title))
		}
		switch ch.Status {
		case types.DocChapterStatusSkipped:
			sb.WriteString("> " + orDefault(ch.Message, "本章未生成") + "\n\n")
			continue
		case types.DocChapterStatusFailed:
			sb.WriteString("> 本章生成失败：" + orDefault(ch.Message, "未知原因") + "\n\n")
			continue
		}
		sb.WriteString(ch.Content + "\n\n")
		if len(ch.References) > 0 {
			sb.WriteString("### 参考资料\n\n")
			for _, ref := range ch.References {
				name := ref.Title
				if name == "" {
					name = ref.Filename
				}
				if name == "" {
					name = ref.KnowledgeID
				}
				sb.WriteString(fmt.Sprintf("- %s\n", name))
			}
			sb.WriteString("\n")
		}
	}
	return sb.String(), nil
}

// RenderWord 将文档渲染为 docx 二进制（纯标准库，零外部依赖）
func (s *DocAssistantService) RenderWord(ctx context.Context, doc *types.DocDocument) ([]byte, error) {
	md, err := s.RenderMarkdown(ctx, doc)
	if err != nil {
		return nil, err
	}
	documentXML := buildDocxDocument(md)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	writeZipEntry := func(name string, content string) error {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = w.Write([]byte(content))
		return err
	}

	if err := writeZipEntry("[Content_Types].xml", contentTypesXML); err != nil {
		return nil, err
	}
	if err := writeZipEntry("_rels/.rels", relsXML); err != nil {
		return nil, err
	}
	if err := writeZipEntry("word/document.xml", documentXML); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ---- docx 生成 ----

const contentTypesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Default Extension="xml" ContentType="application/xml"/>
<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`

const relsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`

const documentXMLHead = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>`

const documentXMLTail = `</w:body>
</w:document>`

var mdInlineRe = regexp.MustCompile(`(\*\*|__|\*|_|~~|` + "`" + `)`)

// buildDocxDocument 将 md 文本转为 docx 段落 XML（行级简化解析）
func buildDocxDocument(md string) string {
	var sb strings.Builder
	sb.WriteString(documentXMLHead)
	for _, line := range strings.Split(md, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		style, text := classifyLine(trimmed)
		var escBuf bytes.Buffer
		_ = xml.EscapeText(&escBuf, []byte(text))
		// 行内加粗标记清理：**xxx** 在 docx 中直接输出纯文本（v1 简化）
		clean := mdInlineRe.ReplaceAllString(escBuf.String(), "")
		switch style {
		case "h1":
			sb.WriteString(`<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t xml:space="preserve">` + clean + `</w:t></w:r></w:p>`)
		case "h2":
			sb.WriteString(`<w:p><w:pPr><w:pStyle w:val="Heading2"/></w:pPr><w:r><w:t xml:space="preserve">` + clean + `</w:t></w:r></w:p>`)
		case "h3":
			sb.WriteString(`<w:p><w:pPr><w:pStyle w:val="Heading3"/></w:pPr><w:r><w:t xml:space="preserve">` + clean + `</w:t></w:r></w:p>`)
		case "h4", "h5":
			sb.WriteString(`<w:p><w:pPr><w:pStyle w:val="Heading4"/></w:pPr><w:r><w:t xml:space="preserve">` + clean + `</w:t></w:r></w:p>`)
		case "list":
			sb.WriteString(`<w:p><w:pPr><w:numPr><w:ilvl w:val="0"/></w:numPr></w:pPr><w:r><w:t xml:space="preserve">` + clean + `</w:t></w:r></w:p>`)
		case "quote":
			sb.WriteString(`<w:p><w:pPr><w:pStyle w:val="IntenseQuote"/></w:pPr><w:r><w:t xml:space="preserve">` + clean + `</w:t></w:r></w:p>`)
		default:
			sb.WriteString(`<w:p><w:r><w:t xml:space="preserve">` + clean + `</w:t></w:r></w:p>`)
		}
	}
	sb.WriteString(documentXMLTail)
	return sb.String()
}

func classifyLine(line string) (style, text string) {
	switch {
	case strings.HasPrefix(line, "###### "):
		return "h5", strings.TrimPrefix(line, "###### ")
	case strings.HasPrefix(line, "##### "):
		return "h4", strings.TrimPrefix(line, "##### ")
	case strings.HasPrefix(line, "#### "):
		return "h3", strings.TrimPrefix(line, "#### ")
	case strings.HasPrefix(line, "### "):
		return "h3", strings.TrimPrefix(line, "### ")
	case strings.HasPrefix(line, "## "):
		return "h2", strings.TrimPrefix(line, "## ")
	case strings.HasPrefix(line, "# "):
		return "h1", strings.TrimPrefix(line, "# ")
	case strings.HasPrefix(line, "- "), strings.HasPrefix(line, "* "), strings.HasPrefix(line, "+ "):
		return "list", strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(line, "- "), "* "), "+ ")
	case strings.HasPrefix(line, ">"):
		return "quote", strings.TrimPrefix(line, "> ")
	default:
		return "para", line
	}
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
