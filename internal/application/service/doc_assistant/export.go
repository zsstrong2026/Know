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
		sb.WriteString(normalizeChapterContent(ch.Content, ch.Title) + "\n\n")
	}
	return sb.String(), nil
}

var (
	// mdHeadingRe 匹配 ATX 标题行（# ～ ######，兼容尾部闭合 #）
	mdHeadingRe = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*#*\s*$`)
	// chineseNumPrefixRe 匹配手写中文序号前缀，如「一、」「十二、」
	chineseNumPrefixRe = regexp.MustCompile(`^[一二三四五六七八九十]+、\s*`)
	// looseNumPrefixRe 宽松匹配手写阿拉伯序号前缀（空白可选），仅用于标题重复判定
	looseNumPrefixRe = regexp.MustCompile(`^\d+[\.、]\s*`)
)

// normalizeChapterContent 清洗章节正文，供导出使用：
// 1) 剥离开头与章节标题重复的 Markdown 标题行（模型常在正文首行重复章节名）；
// 2) 正文内标题级别整体下压，使最浅级别从 ### 起，避免正文小节抢占
//    章节标题（## → Heading1）的自动编号与目录层级。
func normalizeChapterContent(content, chapterTitle string) string {
	lines := strings.Split(content, "\n")
	// 1) 首个非空行若是与章节名同义的标题行，则删除
	if chapterTitle != "" {
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			if m := mdHeadingRe.FindStringSubmatch(trimmed); m != nil &&
				normalizeTitleText(m[2]) == normalizeTitleText(chapterTitle) {
				lines = append(lines[:i], lines[i+1:]...)
			}
			break // 只判定第一个非空行
		}
	}
	// 2) 统计正文（代码块外）最浅标题级别，必要时整体下压到 ### 起
	minLevel := 7
	inCode := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inCode = !inCode
			continue
		}
		if inCode {
			continue
		}
		if m := mdHeadingRe.FindStringSubmatch(trimmed); m != nil && len(m[1]) < minLevel {
			minLevel = len(m[1])
		}
	}
	if minLevel < 3 {
		shift := 3 - minLevel
		inCode = false
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "```") {
				inCode = !inCode
				continue
			}
			if inCode {
				continue
			}
			if m := mdHeadingRe.FindStringSubmatch(trimmed); m != nil {
				level := len(m[1]) + shift
				if level > 6 {
					level = 6
				}
				lines[i] = strings.Repeat("#", level) + " " + m[2]
			}
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// normalizeTitleText 规范化标题文本用于重复判定：剥离手写序号（阿拉伯/中文数字）、
// 行内标记与全部空白，使「## 1. 概述」「# 一、概述」均能与章节名「概述」匹配
func normalizeTitleText(s string) string {
	s = strings.TrimSpace(s)
	s = hnManualNum.ReplaceAllString(s, "")
	s = h1ManualNum.ReplaceAllString(s, "")
	s = looseNumPrefixRe.ReplaceAllString(s, "")
	s = chineseNumPrefixRe.ReplaceAllString(s, "")
	s = mdInlineRe.ReplaceAllString(s, "")
	return strings.Join(strings.Fields(s), "")
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
// 排版规范：A4；上下边距 2.54cm、左右 3.18cm；页眉 1.5cm、页脚 1.75cm；
// 正文宋体/Times New Roman 12pt、1.5 倍行距、首行缩进 2 字符、两端对齐；
// 标题微软雅黑 16/14/13/12pt 加粗，一级中文数字「一、」起自动多级编号；
// 封面 + 自动目录 + 正文分节；页眉为文档名+版本，页脚居中页码。
func (s *DocAssistantService) RenderWord(ctx context.Context, doc *types.DocDocument) ([]byte, error) {
	md, err := s.RenderMarkdown(ctx, doc)
	if err != nil {
		return nil, err
	}
	documentXML, orderedLists := buildDocxDocument(md)
	numberingXML := buildNumberingXML(orderedLists)
	headerXML := buildHeaderXML(fmt.Sprintf("%s V%d", s.documentTitle(ctx, doc), doc.Version))

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

	entries := []struct{ name, content string }{
		{"[Content_Types].xml", contentTypesXML},
		{"_rels/.rels", relsXML},
		{"word/document.xml", documentXML},
		{"word/styles.xml", stylesXML},
		{"word/numbering.xml", numberingXML},
		{"word/header1.xml", headerXML},
		{"word/footer1.xml", footerXML},
		{"word/_rels/document.xml.rels", documentRelsXML},
	}
	for _, e := range entries {
		if err := writeZipEntry(e.name, e.content); err != nil {
			return nil, err
		}
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
<Override PartName="/word/header1.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.header+xml"/>
<Override PartName="/word/footer1.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.footer+xml"/>
</Types>`

const relsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`

const documentRelsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>
<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/numbering" Target="numbering.xml"/>
<Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/header" Target="header1.xml"/>
<Relationship Id="rId4" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/footer" Target="footer1.xml"/>
</Relationships>`

const documentXMLHead = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
<w:body>`

const documentXMLTail = `</w:body>
</w:document>`

// 页面设置：A4(11906x16838 twips)；上下 2.54cm=1440、左右 3.18cm=1803；
// 页眉 1.5cm=851、页脚 1.75cm=992。
const pageSectProps = `<w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1440" w:right="1803" w:bottom="1440" w:left="1803" w:header="851" w:footer="992" w:gutter="0"/>`

// 第一节（封面+目录）：首页不同（封面无页眉页脚），目录页罗马数字页码
const section1SectPr = `<w:sectPr><w:footerReference w:type="default" r:id="rId4"/>` + pageSectProps + `<w:pgNumType w:fmt="lowerRoman" w:start="1"/><w:titlePg/></w:sectPr>`

// 第二节（正文）：页眉=文档名+版本，页脚居中阿拉伯页码，从 1 起
const section2SectPr = `<w:sectPr><w:headerReference w:type="default" r:id="rId3"/><w:footerReference w:type="default" r:id="rId4"/>` + pageSectProps + `<w:pgNumType w:fmt="decimal" w:start="1"/></w:sectPr>`

// 封面与目录块：封面标题后分页，「目录」标题 + TOC 域（dirty 打开时自动更新），末尾分节
const tocBlockXML = `<w:p><w:r><w:br w:type="page"/></w:r></w:p>` +
	`<w:p><w:pPr><w:spacing w:before="240" w:after="240"/><w:ind w:firstLineChars="0" w:firstLine="0"/><w:jc w:val="center"/></w:pPr><w:r><w:rPr><w:rFonts w:ascii="Arial" w:hAnsi="Arial" w:eastAsia="微软雅黑"/><w:b/><w:sz w:val="32"/><w:szCs w:val="32"/></w:rPr><w:t>目  录</w:t></w:r></w:p>` +
	`<w:p><w:pPr><w:ind w:firstLineChars="0" w:firstLine="0"/></w:pPr>` +
	`<w:r><w:fldChar w:fldCharType="begin" w:dirty="true"/></w:r>` +
	`<w:r><w:instrText xml:space="preserve"> TOC \o "1-3" \h \z \u </w:instrText></w:r>` +
	`<w:r><w:fldChar w:fldCharType="separate"/></w:r>` +
	`<w:r><w:rPr><w:color w:val="808080"/></w:rPr><w:t>（目录将在打开文档时自动生成；若未显示，请全选后按 F9 更新）</w:t></w:r>` +
	`<w:r><w:fldChar w:fldCharType="end"/></w:r></w:p>` +
	`<w:p><w:pPr>` + section1SectPr + `</w:pPr></w:p>`

// stylesXML 样式表：正文宋体 12pt/1.5 倍行距/首行缩进 2 字符/两端对齐；
// 标题微软雅黑 16/14/13pt、四级宋体 12pt 加粗，段前段后 24/12、18/10、12/8、6/4pt；
// 标题绑定多级自动编号 numId=2；引用按注释样式（楷体 9pt 灰）。
const stylesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:docDefaults>
<w:rPrDefault><w:rPr><w:rFonts w:ascii="Times New Roman" w:hAnsi="Times New Roman" w:eastAsia="宋体" w:cs="Times New Roman"/><w:sz w:val="24"/><w:szCs w:val="24"/><w:lang w:val="en-US" w:eastAsia="zh-CN"/></w:rPr></w:rPrDefault>
<w:pPrDefault><w:pPr><w:spacing w:after="120" w:line="360" w:lineRule="auto"/></w:pPr></w:pPrDefault>
</w:docDefaults>
<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/><w:qFormat/><w:pPr><w:spacing w:after="120" w:line="360" w:lineRule="auto"/><w:ind w:firstLineChars="200" w:firstLine="480"/><w:jc w:val="both"/></w:pPr><w:rPr><w:rFonts w:ascii="Times New Roman" w:hAnsi="Times New Roman" w:eastAsia="宋体" w:cs="Times New Roman"/><w:sz w:val="24"/><w:szCs w:val="24"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Title"><w:name w:val="Title"/><w:basedOn w:val="Normal"/><w:qFormat/><w:pPr><w:spacing w:before="1200" w:after="480"/><w:ind w:firstLineChars="0" w:firstLine="0"/><w:jc w:val="center"/></w:pPr><w:rPr><w:rFonts w:ascii="Arial" w:hAnsi="Arial" w:eastAsia="微软雅黑"/><w:b/><w:sz w:val="44"/><w:szCs w:val="44"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:keepLines/><w:numPr><w:ilvl w:val="0"/><w:numId w:val="2"/></w:numPr><w:spacing w:before="480" w:after="240" w:line="360" w:lineRule="auto"/><w:ind w:firstLineChars="0" w:firstLine="0"/><w:jc w:val="left"/><w:outlineLvl w:val="0"/></w:pPr><w:rPr><w:rFonts w:ascii="Arial" w:hAnsi="Arial" w:eastAsia="微软雅黑"/><w:b/><w:color w:val="000000"/><w:sz w:val="32"/><w:szCs w:val="32"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Heading2"><w:name w:val="heading 2"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:keepLines/><w:numPr><w:ilvl w:val="1"/><w:numId w:val="2"/></w:numPr><w:spacing w:before="360" w:after="200" w:line="360" w:lineRule="auto"/><w:ind w:firstLineChars="0" w:firstLine="0"/><w:jc w:val="left"/><w:outlineLvl w:val="1"/></w:pPr><w:rPr><w:rFonts w:ascii="Arial" w:hAnsi="Arial" w:eastAsia="微软雅黑"/><w:b/><w:color w:val="000000"/><w:sz w:val="28"/><w:szCs w:val="28"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Heading3"><w:name w:val="heading 3"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:keepLines/><w:numPr><w:ilvl w:val="2"/><w:numId w:val="2"/></w:numPr><w:spacing w:before="240" w:after="160" w:line="360" w:lineRule="auto"/><w:ind w:firstLineChars="0" w:firstLine="0"/><w:jc w:val="left"/><w:outlineLvl w:val="2"/></w:pPr><w:rPr><w:rFonts w:ascii="Arial" w:hAnsi="Arial" w:eastAsia="微软雅黑"/><w:b/><w:color w:val="000000"/><w:sz w:val="26"/><w:szCs w:val="26"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Heading4"><w:name w:val="heading 4"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:keepLines/><w:numPr><w:ilvl w:val="3"/><w:numId w:val="2"/></w:numPr><w:spacing w:before="120" w:after="80" w:line="360" w:lineRule="auto"/><w:ind w:firstLineChars="0" w:firstLine="0"/><w:jc w:val="left"/><w:outlineLvl w:val="3"/></w:pPr><w:rPr><w:rFonts w:ascii="Arial" w:hAnsi="Arial" w:eastAsia="宋体"/><w:b/><w:color w:val="000000"/><w:sz w:val="24"/><w:szCs w:val="24"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Quote"><w:name w:val="Quote"/><w:basedOn w:val="Normal"/><w:qFormat/><w:pPr><w:ind w:left="480" w:right="480" w:firstLineChars="0" w:firstLine="0"/></w:pPr><w:rPr><w:rFonts w:ascii="Times New Roman" w:hAnsi="Times New Roman" w:eastAsia="楷体"/><w:color w:val="595959"/><w:sz w:val="18"/><w:szCs w:val="18"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="TOC1"><w:name w:val="toc 1"/><w:basedOn w:val="Normal"/><w:pPr><w:tabs><w:tab w:val="right" w:leader="dot" w:pos="8300"/></w:tabs><w:spacing w:before="120" w:after="60" w:line="360" w:lineRule="auto"/><w:ind w:left="0" w:firstLineChars="0" w:firstLine="0"/><w:jc w:val="left"/></w:pPr><w:rPr><w:rFonts w:ascii="Times New Roman" w:hAnsi="Times New Roman" w:eastAsia="宋体"/><w:b/><w:sz w:val="24"/><w:szCs w:val="24"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="TOC2"><w:name w:val="toc 2"/><w:basedOn w:val="Normal"/><w:pPr><w:tabs><w:tab w:val="right" w:leader="dot" w:pos="8300"/></w:tabs><w:spacing w:before="0" w:after="60" w:line="360" w:lineRule="auto"/><w:ind w:left="210" w:firstLineChars="0" w:firstLine="0"/><w:jc w:val="left"/></w:pPr><w:rPr><w:rFonts w:ascii="Times New Roman" w:hAnsi="Times New Roman" w:eastAsia="宋体"/><w:sz w:val="24"/><w:szCs w:val="24"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="TOC3"><w:name w:val="toc 3"/><w:basedOn w:val="Normal"/><w:pPr><w:tabs><w:tab w:val="right" w:leader="dot" w:pos="8300"/></w:tabs><w:spacing w:before="0" w:after="60" w:line="360" w:lineRule="auto"/><w:ind w:left="420" w:firstLineChars="0" w:firstLine="0"/><w:jc w:val="left"/></w:pPr><w:rPr><w:rFonts w:ascii="Times New Roman" w:hAnsi="Times New Roman" w:eastAsia="宋体"/><w:sz w:val="21"/><w:szCs w:val="21"/></w:rPr></w:style>
</w:styles>`

// 多级编号抽象定义：
// abstractNum 0：项目符号 •/○/—，左缩进 0.74cm(420 twips) 逐级递增；
// abstractNum 1：标题多级编号，一级「一、」（chineseCounting），二三四级 isLgl 转阿拉伯数字 1.1/1.1.1/1.1.1.1；
// abstractNum 2：有序列表 1./2./3.，每段列表用独立 num + startOverride 重新从 1 起。
const numberingHeadXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:numbering xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:abstractNum w:abstractNumId="0"><w:multiLevelType w:val="hybridMultilevel"/>
<w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="bullet"/><w:lvlText w:val="•"/><w:lvlJc w:val="left"/><w:pPr><w:ind w:left="420" w:hanging="210"/></w:pPr></w:lvl>
<w:lvl w:ilvl="1"><w:start w:val="1"/><w:numFmt w:val="bullet"/><w:lvlText w:val="○"/><w:lvlJc w:val="left"/><w:pPr><w:ind w:left="840" w:hanging="210"/></w:pPr></w:lvl>
<w:lvl w:ilvl="2"><w:start w:val="1"/><w:numFmt w:val="bullet"/><w:lvlText w:val="—"/><w:lvlJc w:val="left"/><w:pPr><w:ind w:left="1260" w:hanging="210"/></w:pPr></w:lvl>
</w:abstractNum>
<w:abstractNum w:abstractNumId="1"><w:multiLevelType w:val="multilevel"/>
<w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="chineseCounting"/><w:lvlText w:val="%1、"/><w:lvlJc w:val="left"/><w:pPr><w:ind w:left="0" w:firstLine="0"/></w:pPr></w:lvl>
<w:lvl w:ilvl="1"><w:start w:val="1"/><w:numFmt w:val="decimal"/><w:isLgl/><w:lvlText w:val="%1.%2"/><w:lvlJc w:val="left"/><w:pPr><w:ind w:left="0" w:firstLine="0"/></w:pPr></w:lvl>
<w:lvl w:ilvl="2"><w:start w:val="1"/><w:numFmt w:val="decimal"/><w:isLgl/><w:lvlText w:val="%1.%2.%3"/><w:lvlJc w:val="left"/><w:pPr><w:ind w:left="0" w:firstLine="0"/></w:pPr></w:lvl>
<w:lvl w:ilvl="3"><w:start w:val="1"/><w:numFmt w:val="decimal"/><w:isLgl/><w:lvlText w:val="%1.%2.%3.%4"/><w:lvlJc w:val="left"/><w:pPr><w:ind w:left="0" w:firstLine="0"/></w:pPr></w:lvl>
</w:abstractNum>
<w:abstractNum w:abstractNumId="2"><w:multiLevelType w:val="singleLevel"/>
<w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="decimal"/><w:lvlText w:val="%1."/><w:lvlJc w:val="left"/><w:pPr><w:ind w:left="420" w:hanging="280"/></w:pPr></w:lvl>
</w:abstractNum>
<w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num>
<w:num w:numId="2"><w:abstractNumId w:val="1"/></w:num>`

func buildNumberingXML(orderedLists int) string {
	var sb strings.Builder
	sb.WriteString(numberingHeadXML)
	for i := 0; i < orderedLists; i++ {
		fmt.Fprintf(&sb, `<w:num w:numId="%d"><w:abstractNumId w:val="2"/><w:lvlOverride w:ilvl="0"><w:startOverride w:val="1"/></w:lvlOverride></w:num>`, 10+i)
	}
	sb.WriteString(`</w:numbering>`)
	return sb.String()
}

// buildHeaderXML 页眉：文档名+版本号，宋体 9pt 灰，右对齐，底部 0.5pt 单线
func buildHeaderXML(text string) string {
	var escBuf bytes.Buffer
	_ = xml.EscapeText(&escBuf, []byte(text))
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:hdr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:p><w:pPr><w:pBdr><w:bottom w:val="single" w:sz="4" w:space="1" w:color="808080"/></w:pBdr><w:spacing w:after="0" w:line="240" w:lineRule="auto"/><w:ind w:firstLineChars="0" w:firstLine="0"/><w:jc w:val="right"/></w:pPr><w:r><w:rPr><w:rFonts w:ascii="Times New Roman" w:hAnsi="Times New Roman" w:eastAsia="宋体"/><w:color w:val="808080"/><w:sz w:val="18"/><w:szCs w:val="18"/></w:rPr><w:t xml:space="preserve">` + escBuf.String() + `</w:t></w:r></w:p>
</w:hdr>`
}

// footerXML 页脚：居中「- 页码 -」，宋体 9pt 灰；数字格式由所在节 pgNumType 决定（目录罗马、正文阿拉伯）
const footerXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:ftr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:p><w:pPr><w:spacing w:after="0" w:line="240" w:lineRule="auto"/><w:ind w:firstLineChars="0" w:firstLine="0"/><w:jc w:val="center"/></w:pPr><w:r><w:rPr><w:rFonts w:ascii="Times New Roman" w:hAnsi="Times New Roman" w:eastAsia="宋体"/><w:color w:val="808080"/><w:sz w:val="18"/><w:szCs w:val="18"/></w:rPr><w:t xml:space="preserve">- </w:t></w:r><w:r><w:rPr><w:rFonts w:ascii="Times New Roman" w:hAnsi="Times New Roman" w:eastAsia="宋体"/><w:color w:val="808080"/><w:sz w:val="18"/><w:szCs w:val="18"/></w:rPr><w:fldChar w:fldCharType="begin"/></w:r><w:r><w:rPr><w:rFonts w:ascii="Times New Roman" w:hAnsi="Times New Roman" w:eastAsia="宋体"/><w:color w:val="808080"/><w:sz w:val="18"/><w:szCs w:val="18"/></w:rPr><w:instrText xml:space="preserve"> PAGE </w:instrText></w:r><w:r><w:rPr><w:rFonts w:ascii="Times New Roman" w:hAnsi="Times New Roman" w:eastAsia="宋体"/><w:color w:val="808080"/><w:sz w:val="18"/><w:szCs w:val="18"/></w:rPr><w:fldChar w:fldCharType="end"/></w:r><w:r><w:rPr><w:rFonts w:ascii="Times New Roman" w:hAnsi="Times New Roman" w:eastAsia="宋体"/><w:color w:val="808080"/><w:sz w:val="18"/><w:szCs w:val="18"/></w:rPr><w:t xml:space="preserve"> -</w:t></w:r></w:p>
</w:ftr>`

var (
	mdInlineRe   = regexp.MustCompile(`(\*\*|__|\*|_|~~|` + "`" + `)`)
	h1ManualNum  = regexp.MustCompile(`^\d+[\.、]\s+`)
	hnManualNum  = regexp.MustCompile(`^\d+(\.\d+)+[\.、]?\s+`)
	orderedLstRe = regexp.MustCompile(`^\d+\.\s+`)
	tableSepRe   = regexp.MustCompile(`^\|?[\s:|-]+\|?$`)
)

// buildDocxDocument 将 md 文本转为 docx 段落 XML。
// 结构映射：# 封面标题（Title 居中，随后自动目录 + 分节）；##/###/####… 映射
// Heading1-4，自动多级编号（剥离手写序号）；支持表格、代码块、多级项目符号、
// 有序列表（每段独立从 1 起）。返回文档 XML 与有序列表段数（用于生成编号定义）。
func buildDocxDocument(md string) (string, int) {
	var sb strings.Builder
	sb.WriteString(documentXMLHead)
	lines := strings.Split(md, "\n")
	inCode := false
	orderedLists := 0
	curOListNumID := 0
	titleDone := false
	for i := 0; i < len(lines); i++ {
		raw := strings.TrimRight(lines[i], "\r")
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "```") {
			inCode = !inCode
			continue
		}
		if inCode {
			sb.WriteString(codeParagraph(raw))
			continue
		}
		if trimmed == "" {
			continue
		}
		// 跳过 Markdown 水平分割线
		if len(trimmed) >= 3 && strings.Trim(trimmed, "-") == "" {
			continue
		}
		// 表格块：连续以 | 起始的行
		if strings.HasPrefix(trimmed, "|") {
			j := i
			var block []string
			for j < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[j]), "|") {
				block = append(block, strings.TrimSpace(lines[j]))
				j++
			}
			i = j - 1
			sb.WriteString(buildTable(block))
			curOListNumID = 0
			continue
		}
		style, text := classifyLine(trimmed)
		if style == "para" && orderedLstRe.MatchString(trimmed) {
			style = "olist"
			text = orderedLstRe.ReplaceAllString(trimmed, "")
		}
		if style != "olist" {
			curOListNumID = 0
		}
		var escBuf bytes.Buffer
		_ = xml.EscapeText(&escBuf, []byte(text))
		// 行内加粗/斜体/删除线/行内代码标记清理：docx 中直接输出纯文本（v1 简化）
		clean := mdInlineRe.ReplaceAllString(escBuf.String(), "")
		switch style {
		case "title":
			sb.WriteString(styledParagraph("Title", clean))
			if !titleDone {
				sb.WriteString(tocBlockXML)
				titleDone = true
			}
		case "h1":
			sb.WriteString(styledParagraph("Heading1", h1ManualNum.ReplaceAllString(clean, "")))
		case "h2":
			sb.WriteString(styledParagraph("Heading2", hnManualNum.ReplaceAllString(clean, "")))
		case "h3":
			sb.WriteString(styledParagraph("Heading3", hnManualNum.ReplaceAllString(clean, "")))
		case "h4":
			sb.WriteString(styledParagraph("Heading4", hnManualNum.ReplaceAllString(clean, "")))
		case "list":
			indent := len(raw) - len(strings.TrimLeft(raw, " "))
			ilvl := indent / 2
			if ilvl > 2 {
				ilvl = 2
			}
			sb.WriteString(fmt.Sprintf(`<w:p><w:pPr><w:numPr><w:ilvl w:val="%d"/><w:numId w:val="1"/></w:numPr><w:spacing w:after="0"/><w:ind w:firstLineChars="0" w:firstLine="0"/></w:pPr><w:r><w:t xml:space="preserve">%s</w:t></w:r></w:p>`, ilvl, clean))
		case "olist":
			if curOListNumID == 0 {
				orderedLists++
				curOListNumID = 10 + orderedLists - 1
			}
			sb.WriteString(fmt.Sprintf(`<w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="%d"/></w:numPr><w:spacing w:after="0"/><w:ind w:firstLineChars="0" w:firstLine="0"/></w:pPr><w:r><w:t xml:space="preserve">%s</w:t></w:r></w:p>`, curOListNumID, clean))
		case "quote":
			sb.WriteString(styledParagraph("Quote", clean))
		default:
			sb.WriteString(`<w:p><w:r><w:t xml:space="preserve">` + clean + `</w:t></w:r></w:p>`)
		}
	}
	sb.WriteString(section2SectPr)
	sb.WriteString(documentXMLTail)
	return sb.String(), orderedLists
}

func styledParagraph(style, escText string) string {
	return `<w:p><w:pPr><w:pStyle w:val="` + style + `"/></w:pPr><w:r><w:t xml:space="preserve">` + escText + `</w:t></w:r></w:p>`
}

// codeParagraph 代码块行：Consolas 10pt、#F5F5F5 底纹、单倍行距、左对齐无首行缩进
func codeParagraph(raw string) string {
	var escBuf bytes.Buffer
	_ = xml.EscapeText(&escBuf, []byte(raw))
	return `<w:p><w:pPr><w:shd w:val="clear" w:color="auto" w:fill="F5F5F5"/><w:spacing w:before="0" w:after="0" w:line="240" w:lineRule="auto"/><w:ind w:firstLineChars="0" w:firstLine="0"/><w:jc w:val="left"/></w:pPr><w:r><w:rPr><w:rFonts w:ascii="Consolas" w:hAnsi="Consolas" w:eastAsia="宋体"/><w:sz w:val="20"/><w:szCs w:val="20"/></w:rPr><w:t xml:space="preserve">` + escBuf.String() + `</w:t></w:r></w:p>`
}

// buildTable 渲染 Markdown 表格：100% 页宽、全框线、表头 #D9E2F3 底纹加粗居中、
// 正文左对齐、宋体 10.5pt、单元格边距上下 2pt 左右 5pt；非合法表格回退为普通段落
func buildTable(block []string) string {
	var sb strings.Builder
	if len(block) < 2 || !tableSepRe.MatchString(block[1]) {
		for _, l := range block {
			sb.WriteString(plainParagraph(l))
		}
		return sb.String()
	}
	rows := make([][]string, 0, len(block)-1)
	rows = append(rows, splitTableRow(block[0]))
	for _, l := range block[2:] {
		rows = append(rows, splitTableRow(l))
	}
	ncols := 0
	for _, r := range rows {
		if len(r) > ncols {
			ncols = len(r)
		}
	}
	if ncols == 0 {
		return ""
	}
	cellW := 5000 / ncols
	sb.WriteString(`<w:tbl><w:tblPr><w:tblW w:w="5000" w:type="pct"/><w:jc w:val="center"/><w:tblBorders>` +
		`<w:top w:val="single" w:sz="4" w:space="0" w:color="000000"/>` +
		`<w:left w:val="single" w:sz="4" w:space="0" w:color="000000"/>` +
		`<w:bottom w:val="single" w:sz="4" w:space="0" w:color="000000"/>` +
		`<w:right w:val="single" w:sz="4" w:space="0" w:color="000000"/>` +
		`<w:insideH w:val="single" w:sz="4" w:space="0" w:color="000000"/>` +
		`<w:insideV w:val="single" w:sz="4" w:space="0" w:color="000000"/>` +
		`</w:tblBorders><w:tblCellMar>` +
		`<w:top w:w="40" w:type="dxa"/><w:left w:w="100" w:type="dxa"/><w:bottom w:w="40" w:type="dxa"/><w:right w:w="100" w:type="dxa"/>` +
		`</w:tblCellMar></w:tblPr>`)
	for ri, row := range rows {
		if ri == 0 {
			sb.WriteString(`<w:tr><w:trPr><w:tblHeader/></w:trPr>`)
		} else {
			sb.WriteString(`<w:tr>`)
		}
		for ci := 0; ci < ncols; ci++ {
			cell := ""
			if ci < len(row) {
				cell = row[ci]
			}
			var escBuf bytes.Buffer
			_ = xml.EscapeText(&escBuf, []byte(cell))
			clean := mdInlineRe.ReplaceAllString(escBuf.String(), "")
			if ri == 0 {
				sb.WriteString(fmt.Sprintf(`<w:tc><w:tcPr><w:tcW w:w="%d" w:type="pct"/><w:shd w:val="clear" w:color="auto" w:fill="D9E2F3"/></w:tcPr><w:p><w:pPr><w:spacing w:after="0" w:line="240" w:lineRule="auto"/><w:ind w:firstLineChars="0" w:firstLine="0"/><w:jc w:val="center"/></w:pPr><w:r><w:rPr><w:b/><w:sz w:val="21"/><w:szCs w:val="21"/></w:rPr><w:t xml:space="preserve">%s</w:t></w:r></w:p></w:tc>`, cellW, clean))
			} else {
				sb.WriteString(fmt.Sprintf(`<w:tc><w:tcPr><w:tcW w:w="%d" w:type="pct"/></w:tcPr><w:p><w:pPr><w:spacing w:after="0" w:line="240" w:lineRule="auto"/><w:ind w:firstLineChars="0" w:firstLine="0"/><w:jc w:val="left"/></w:pPr><w:r><w:rPr><w:sz w:val="21"/><w:szCs w:val="21"/></w:rPr><w:t xml:space="preserve">%s</w:t></w:r></w:p></w:tc>`, cellW, clean))
			}
		}
		sb.WriteString(`</w:tr>`)
	}
	sb.WriteString(`</w:tbl>`)
	// Word 不允许表格后紧跟分节或另一个表格，补一个空段
	sb.WriteString(`<w:p><w:pPr><w:spacing w:after="0" w:line="240" w:lineRule="auto"/><w:ind w:firstLineChars="0" w:firstLine="0"/></w:pPr></w:p>`)
	return sb.String()
}

func splitTableRow(line string) []string {
	line = strings.TrimPrefix(strings.TrimSuffix(line, "|"), "|")
	parts := strings.Split(line, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func plainParagraph(text string) string {
	var escBuf bytes.Buffer
	_ = xml.EscapeText(&escBuf, []byte(text))
	clean := mdInlineRe.ReplaceAllString(escBuf.String(), "")
	return `<w:p><w:r><w:t xml:space="preserve">` + clean + `</w:t></w:r></w:p>`
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
