package docassistant

import (
	"encoding/xml"
	"strings"
	"testing"
)

func mustWellFormed(t *testing.T, name, content string) {
	t.Helper()
	if err := xml.Unmarshal([]byte(content), new(interface{})); err != nil {
		t.Fatalf("%s 不是合法 XML: %v", name, err)
	}
}

const sampleMD = `# 示例文档

## 1. 第一章

正文第一段，首行缩进两字符。

### 1.1 小节

- 一级项目
  - 二级项目
    - 三级项目

1. 第一步
2. 第二步

另一段文字。

1. 重新编号一
2. 重新编号二

| 名称 | 数量 | 备注 |
| --- | --- | --- |
| 苹果 | 3 | **加粗** |
| 香蕉 | 5 | 无 |

` + "```go" + `
func main() {
	fmt.Println("hi")
}
` + "```" + `

> 本章说明注释

## 2. 第二章

#### 1.1.1.1 四级标题
`

func TestBuildDocxDocumentWellFormed(t *testing.T) {
	docXML, orderedLists := buildDocxDocument(sampleMD)
	mustWellFormed(t, "document.xml", docXML)
	mustWellFormed(t, "styles.xml", stylesXML)
	mustWellFormed(t, "numbering.xml", buildNumberingXML(orderedLists))
	mustWellFormed(t, "header1.xml", buildHeaderXML("示例文档 V1"))
	mustWellFormed(t, "footer1.xml", footerXML)
	mustWellFormed(t, "[Content_Types].xml", contentTypesXML)
	mustWellFormed(t, "document.xml.rels", documentRelsXML)

	if orderedLists != 2 {
		t.Fatalf("有序列表段数 = %d, 期望 2", orderedLists)
	}
	checks := []struct{ desc, substr string }{
		{"A4 页面尺寸", `<w:pgSz w:w="11906" w:h="16838"/>`},
		{"页边距 2.54/3.18cm", `w:top="1440" w:right="1803" w:bottom="1440" w:left="1803"`},
		{"页眉页脚距离", `w:header="851" w:footer="992"`},
		{"封面节首页不同", `<w:titlePg/>`},
		{"目录节罗马页码", `<w:pgNumType w:fmt="lowerRoman" w:start="1"/>`},
		{"正文节页码从1", `<w:pgNumType w:fmt="decimal" w:start="1"/>`},
		{"TOC 域", `TOC \o "1-3" \h \z \u`},
		{"一级标题剥离手写序号", `>第一章</w:t>`},
		{"表格表头底纹", `w:fill="D9E2F3"`},
		{"表格字号10.5pt", `<w:sz w:val="21"/>`},
		{"代码块底纹", `w:fill="F5F5F5"`},
		{"二级列表符号缩进", `<w:ilvl w:val="1"/><w:numId w:val="1"/>`},
		{"有序列表重新编号 numId=11", `<w:numId w:val="11"/>`},
	}
	for _, c := range checks {
		if !strings.Contains(docXML, c.substr) {
			t.Errorf("document.xml 缺少「%s」: %s", c.desc, c.substr)
		}
	}
	if strings.Contains(docXML, `>1. 第一章</w:t>`) {
		t.Errorf("一级标题手写序号未被剥离")
	}
}

func TestNormalizeChapterContent(t *testing.T) {
	cases := []struct {
		desc, content, title, want string
	}{
		{
			desc:    "剥离首行 ## 重复标题",
			content: "## 概述\n\n正文内容。",
			title:   "概述",
			want:    "正文内容。",
		},
		{
			desc:    "剥离首行 # 重复标题（含手写序号）",
			content: "# 1. 概述\n\n正文内容。",
			title:   "概述",
			want:    "正文内容。",
		},
		{
			desc:    "剥离首行中文序号重复标题",
			content: "## 一、概述\n正文内容。",
			title:   "概述",
			want:    "正文内容。",
		},
		{
			desc:    "首行标题与章节名不同则保留并下压",
			content: "## 背景介绍\n\n正文。",
			title:   "概述",
			want:    "### 背景介绍\n\n正文。",
		},
		{
			desc:    "正文 ## 小节下压为 ###",
			content: "正文。\n\n## 小节一\n\n内容。\n\n## 小节二\n\n内容。",
			title:   "概述",
			want:    "正文。\n\n### 小节一\n\n内容。\n\n### 小节二\n\n内容。",
		},
		{
			desc:    "正文 # 与 #### 混合时整体平移（最浅到 ###）",
			content: "# 大节\n\n#### 细节\n\n正文。",
			title:   "概述",
			want:    "### 大节\n\n###### 细节\n\n正文。",
		},
		{
			desc:    "已是 ### 起的正文保持不变",
			content: "正文。\n\n### 1.1 小节\n\n内容。",
			title:   "概述",
			want:    "正文。\n\n### 1.1 小节\n\n内容。",
		},
		{
			desc:    "代码块内的 # 注释不受影响",
			content: "## 概述\n\n```go\n# 不是标题\nfunc main() {}\n```\n\n正文。",
			title:   "概述",
			want:    "```go\n# 不是标题\nfunc main() {}\n```\n\n正文。",
		},
		{
			desc:    "无标题正文原样返回",
			content: "纯正文，没有标题。",
			title:   "概述",
			want:    "纯正文，没有标题。",
		},
	}
	for _, c := range cases {
		if got := normalizeChapterContent(c.content, c.title); got != c.want {
			t.Errorf("%s:\n得到:\n%s\n期望:\n%s", c.desc, got, c.want)
		}
	}
}

func TestNormalizeTitleText(t *testing.T) {
	cases := []struct{ in, want string }{
		{"概述", "概述"},
		{"1. 概述", "概述"},
		{"1、概述", "概述"},
		{"一、概述", "概述"},
		{"**概述**", "概述"},
		{"概 述", "概述"},
	}
	for _, c := range cases {
		if got := normalizeTitleText(c.in); got != c.want {
			t.Errorf("normalizeTitleText(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}
