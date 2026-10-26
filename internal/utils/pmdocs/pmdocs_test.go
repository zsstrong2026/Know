package pmdocs

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

// readZip 解包导出文件，返回条目名 → 内容，便于断言 OOXML 结构
func readZip(t *testing.T, data []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("result is not a valid zip: %v", err)
	}
	files := make(map[string]string, len(zr.File))
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open zip entry %s: %v", f.Name, err)
		}
		raw, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read zip entry %s: %v", f.Name, err)
		}
		files[f.Name] = string(raw)
	}
	return files
}

// assertWellFormedXML 逐个 token 解析，确保部件是可被 Office 打开的合法 XML
func assertWellFormedXML(t *testing.T, name, content string) {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(content))
	for {
		if _, err := dec.Token(); err == io.EOF {
			return
		} else if err != nil {
			t.Fatalf("%s is not well-formed xml: %v", name, err)
		}
	}
}

func TestParseMarkdownBlocks(t *testing.T) {
	md := "# 操作手册\n\n## 登录\n\n- 输入**账号**\n\n```\ncode line\n```\n\n普通段落"
	blocks := Parse(md)

	want := []Block{
		{Level: 1, Text: "操作手册"},
		{Level: 2, Text: "登录"},
		{Level: levelList, Text: "输入账号"},
		{Level: levelBody, Text: "code line"},
		{Level: levelBody, Text: "普通段落"},
	}
	if len(blocks) != len(want) {
		t.Fatalf("want %d blocks, got %d: %+v", len(want), len(blocks), blocks)
	}
	for i, w := range want {
		if blocks[i] != w {
			t.Fatalf("block %d want %+v, got %+v", i, w, blocks[i])
		}
	}
}

func TestRenderDocx(t *testing.T) {
	md := "# 操作手册\n\n## 登录\n\n1. 输入账号 & 密码\n\n- 点击 <登录>"
	data, err := RenderDocx("操作手册", md)
	if err != nil {
		t.Fatalf("render docx: %v", err)
	}
	files := readZip(t, data)

	for _, name := range []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml"} {
		if _, ok := files[name]; !ok {
			t.Fatalf("docx missing part %s", name)
		}
		assertWellFormedXML(t, name, files[name])
	}

	doc := files["word/document.xml"]
	if strings.Count(doc, "<w:p>") < 4 {
		t.Fatalf("want title + heading + list + body paragraphs, got %d", strings.Count(doc, "<w:p>"))
	}
	if !strings.Contains(doc, "输入账号 &amp; 密码") {
		t.Fatalf("special chars should be escaped and body kept: %s", doc)
	}
	if !strings.Contains(doc, "点击 &lt;登录&gt;") {
		t.Fatalf("angle brackets should be escaped")
	}
}

func TestRenderDocxEmptyTitleFallsBack(t *testing.T) {
	data, err := RenderDocx("  ", "正文")
	if err != nil {
		t.Fatalf("render docx: %v", err)
	}
	if !strings.Contains(readZip(t, data)["word/document.xml"], "项目资料") {
		t.Fatalf("empty title should fall back to 项目资料")
	}
}

func TestRenderPptx(t *testing.T) {
	md := "# 产品介绍\n\n## 亮点\n\n- 快速部署\n\n## 架构\n\n微服务架构"
	data, err := RenderPptx("产品介绍", md)
	if err != nil {
		t.Fatalf("render pptx: %v", err)
	}
	files := readZip(t, data)

	required := []string{
		"[Content_Types].xml", "_rels/.rels", "ppt/presentation.xml",
		"ppt/slideMasters/slideMaster1.xml", "ppt/slideLayouts/slideLayout1.xml",
		"ppt/theme/theme1.xml", "ppt/slides/slide1.xml",
	}
	for _, name := range required {
		if _, ok := files[name]; !ok {
			t.Fatalf("pptx missing part %s", name)
		}
		assertWellFormedXML(t, name, files[name])
	}

	// 封面（首个一级标题与资料名重复，保留资料名）+ 两个二级标题各起一页 → 共 3 页
	if _, ok := files["ppt/slides/slide3.xml"]; !ok {
		t.Fatalf("want 3 slides")
	}
	if _, ok := files["ppt/slides/slide4.xml"]; ok {
		t.Fatalf("want at most 3 slides")
	}
	if !strings.Contains(files["ppt/slides/slide1.xml"], "产品介绍") {
		t.Fatalf("cover should keep material title")
	}
	if !strings.Contains(files["ppt/slides/slide3.xml"], "微服务架构") {
		t.Fatalf("slide body should keep content")
	}
	if !strings.Contains(files["ppt/presentation.xml"], `r:id="rId3"`) {
		t.Fatalf("presentation should reference slide1 via rId3")
	}
}

func TestSplitSlidesOverflow(t *testing.T) {
	// 2 个标题 + 10 个正文块：第二页超限后应续页
	var md strings.Builder
	md.WriteString("# 封面\n\n## 明细\n")
	for i := 0; i < 10; i++ {
		md.WriteString("\n正文块\n")
	}
	slides := splitSlides("资料", Parse(md.String()))

	if len(slides) != 3 {
		t.Fatalf("want 3 slides (title + 8 + 2), got %d", len(slides))
	}
	if !strings.HasSuffix(slides[2].Title, "（续）") {
		t.Fatalf("continuation slide should be marked, got %q", slides[2].Title)
	}
	if len(slides[2].Blocks) != 2 {
		t.Fatalf("want 2 remaining blocks, got %d", len(slides[2].Blocks))
	}
}
