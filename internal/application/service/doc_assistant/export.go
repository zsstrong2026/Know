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

// RenderMarkdown 将文档渲染为 Markdown 文本（标题为文档名称，不含参考资料）
func (s *DocAssistantService) RenderMarkdown(ctx context.Context, doc *types.DocDocument) (string, error) {
	chapters, err := decodeDocChapters(doc)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# %s\n\n", s.documentTitle(ctx, doc)))
	for _, ch := range chapters {
		if ch.Title != "" {
			sb.WriteString(fmt.Sprintf("\n## %d. %s\n\n", ch.No, ch.Title))
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
	}
	return sb.String(), nil
}

// documentTitle 文档名称：取生成任务主题；任务缺失时回退为「文档 v版本号」
func (s *DocAssistantService) documentTitle(ctx context.Context, doc *types.DocDocument) string {
	if doc != nil && doc.TaskID != "" {
		if task, err := s.taskRepo.FindByID(ctx, doc.TenantID, doc.OwnerID, doc.TaskID); err == nil && task != nil {
			if name := strings.TrimSpace(task.Theme); name != "" {
				return name
			}
		}
	}
	return fmt.Sprintf("文档 v%d", doc.Version)
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
	if err := writeZipEntry("word/styles.xml", stylesXML); err != nil {
		return nil, err
	}
	if err := writeZipEntry("word/numbering.xml", numberingXML); err != nil {
		return nil, err
	}
	if err := writeZipEntry("word/_rels/document.xml.rels", documentRelsXML); err != nil {
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
<Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>
<Override PartName="/word/numbering.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.numbering+xml"/>
</Types>`

const relsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`

const documentRelsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>
<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/numbering" Target="numbering.xml"/>
</Relationships>`

const documentXMLHead = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>`

const documentXMLTail = `</w:body>
</w:document>`

// stylesXML 样式表：正文 + 文档标题 + 各级章节标题（大纲层级）+ 引用
const stylesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:docDefaults>
<w:rPrDefault><w:rPr><w:rFonts w:ascii="Calibri" w:hAnsi="Calibri" w:eastAsia="宋体" w:cs="Times New Roman"/><w:sz w:val="22"/><w:szCs w:val="22"/><w:lang w:val="en-US" w:eastAsia="zh-CN"/></w:rPr></w:rPrDefault>
<w:pPrDefault><w:pPr><w:spacing w:after="120" w:line="360" w:lineRule="auto"/></w:pPr></w:pPrDefault>
</w:docDefaults>
<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/><w:qFormat/></w:style>
<w:style w:type="paragraph" w:styleId="Title"><w:name w:val="Title"/><w:basedOn w:val="Normal"/><w:qFormat/><w:pPr><w:jc w:val="center"/><w:spacing w:before="240" w:after="480"/></w:pPr><w:rPr><w:rFonts w:ascii="Calibri" w:hAnsi="Calibri" w:eastAsia="微软雅黑"/><w:b/><w:sz w:val="44"/><w:szCs w:val="44"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:keepLines/><w:pageBreakBefore/><w:spacing w:before="0" w:after="360"/><w:outlineLvl w:val="0"/></w:pPr><w:rPr><w:rFonts w:ascii="Calibri" w:hAnsi="Calibri" w:eastAsia="微软雅黑"/><w:b/><w:color w:val="1F4E79"/><w:sz w:val="32"/><w:szCs w:val="32"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Heading2"><w:name w:val="heading 2"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:keepLines/><w:spacing w:before="240" w:after="120"/><w:outlineLvl w:val="1"/></w:pPr><w:rPr><w:rFonts w:ascii="Calibri" w:hAnsi="Calibri" w:eastAsia="微软雅黑"/><w:b/><w:color w:val="2E74B5"/><w:sz w:val="28"/><w:szCs w:val="28"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Heading3"><w:name w:val="heading 3"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:keepLines/><w:spacing w:before="200" w:after="100"/><w:outlineLvl w:val="2"/></w:pPr><w:rPr><w:rFonts w:ascii="Calibri" w:hAnsi="Calibri" w:eastAsia="微软雅黑"/><w:b/><w:color w:val="1F4E79"/><w:sz w:val="24"/><w:szCs w:val="24"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Heading4"><w:name w:val="heading 4"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:keepLines/><w:spacing w:before="160" w:after="80"/><w:outlineLvl w:val="3"/></w:pPr><w:rPr><w:rFonts w:ascii="Calibri" w:hAnsi="Calibri" w:eastAsia="微软雅黑"/><w:b/><w:color w:val="2E74B5"/><w:sz w:val="22"/><w:szCs w:val="22"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Quote"><w:name w:val="Quote"/><w:basedOn w:val="Normal"/><w:qFormat/><w:pPr><w:ind w:left="480" w:right="480"/></w:pPr><w:rPr><w:i/><w:iCs/><w:color w:val="595959"/></w:rPr></w:style>
</w:styles>`

// numberingXML 项目符号列表定义（numId=1）
const numberingXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:numbering xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:abstractNum w:abstractNumId="0"><w:multiLevelType w:val="hybridMultilevel"/>
<w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="bullet"/><w:lvlText w:val="•"/><w:lvlJc w:val="left"/><w:pPr><w:ind w:left="720" w:hanging="360"/></w:pPr></w:lvl>
<w:lvl w:ilvl="1"><w:start w:val="1"/><w:numFmt w:val="bullet"/><w:lvlText w:val="◦"/><w:lvlJc w:val="left"/><w:pPr><w:ind w:left="1080" w:hanging="360"/></w:pPr></w:lvl>
<w:lvl w:ilvl="2"><w:start w:val="1"/><w:numFmt w:val="bullet"/><w:lvlText w:val="▪"/><w:lvlJc w:val="left"/><w:pPr><w:ind w:left="1440" w:hanging="360"/></w:pPr></w:lvl>
</w:abstractNum>
<w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num>
</w:numbering>`

var mdInlineRe = regexp.MustCompile(`(\*\*|__|\*|_|~~|` + "`" + `)`)

// buildDocxDocument 将 md 文本转为 docx 段落 XML（行级简化解析）
// 结构映射：# 文档标题（Title 居中）、## 章标题（Heading1，每章另起一页）、
// ###/#### 小节（Heading2/3，进入导航大纲）
func buildDocxDocument(md string) string {
	var sb strings.Builder
	sb.WriteString(documentXMLHead)
	for _, line := range strings.Split(md, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// 跳过 Markdown 水平分割线
		if len(trimmed) >= 3 && strings.Trim(trimmed, "-") == "" {
			continue
		}
		style, text := classifyLine(trimmed)
		var escBuf bytes.Buffer
		_ = xml.EscapeText(&escBuf, []byte(text))
		// 行内加粗标记清理：**xxx** 在 docx 中直接输出纯文本（v1 简化）
		clean := mdInlineRe.ReplaceAllString(escBuf.String(), "")
		switch style {
		case "title":
			sb.WriteString(`<w:p><w:pPr><w:pStyle w:val="Title"/></w:pPr><w:r><w:t xml:space="preserve">` + clean + `</w:t></w:r></w:p>`)
		case "h1":
			sb.WriteString(`<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t xml:space="preserve">` + clean + `</w:t></w:r></w:p>`)
		case "h2":
			sb.WriteString(`<w:p><w:pPr><w:pStyle w:val="Heading2"/></w:pPr><w:r><w:t xml:space="preserve">` + clean + `</w:t></w:r></w:p>`)
		case "h3":
			sb.WriteString(`<w:p><w:pPr><w:pStyle w:val="Heading3"/></w:pPr><w:r><w:t xml:space="preserve">` + clean + `</w:t></w:r></w:p>`)
		case "h4":
			sb.WriteString(`<w:p><w:pPr><w:pStyle w:val="Heading4"/></w:pPr><w:r><w:t xml:space="preserve">` + clean + `</w:t></w:r></w:p>`)
		case "list":
			sb.WriteString(`<w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr><w:r><w:t xml:space="preserve">` + clean + `</w:t></w:r></w:p>`)
		case "quote":
			sb.WriteString(`<w:p><w:pPr><w:pStyle w:val="Quote"/></w:pPr><w:r><w:t xml:space="preserve">` + clean + `</w:t></w:r></w:p>`)
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
		return "h4", strings.TrimPrefix(line, "###### ")
	case strings.HasPrefix(line, "##### "):
		return "h4", strings.TrimPrefix(line, "##### ")
	case strings.HasPrefix(line, "#### "):
		return "h3", strings.TrimPrefix(line, "#### ")
	case strings.HasPrefix(line, "### "):
		return "h2", strings.TrimPrefix(line, "### ")
	case strings.HasPrefix(line, "## "):
		return "h1", strings.TrimPrefix(line, "## ")
	case strings.HasPrefix(line, "# "):
		return "title", strings.TrimPrefix(line, "# ")
	case strings.HasPrefix(line, "- "), strings.HasPrefix(line, "* "), strings.HasPrefix(line, "+ "):
		return "list", strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(line, "- "), "* "), "+ ")
	case strings.HasPrefix(line, ">"):
		return "quote", strings.TrimSpace(strings.TrimPrefix(line, ">"))
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
