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
