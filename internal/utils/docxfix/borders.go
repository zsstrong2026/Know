// Package docxfix 修复 Word 产物中被误写为 DrawingML 的表格边框。
//
// 缺陷来源：生成脚本把 PPT 的边框写法（a:lnL / a:lnR / a:lnT / a:lnB）写进了
// Word 的 <w:tcPr>。Word 只认 <w:tcBorders><w:top/>...，遇到 DrawingML 节点
// 会直接报「文件已损坏」。这里在产物入库前统一清洗，避免每次下载后还要手工修。
//
// 清洗走字节级正则而非 XML 反序列化：document.xml 的命名空间前缀与
// mc:Ignorable 映射一旦被重排，Word 同样会拒绝打开。
package docxfix

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	// 需要清洗的部件：正文、页眉页脚、脚注尾注
	partRe = regexp.MustCompile(`^word/(document|header\d*|footer\d*|footnotes|endnotes)\.xml$`)
	// 单元格属性块
	tcPrRe = regexp.MustCompile(`(?s)<w:tcPr\b[^>]*>(.*?)</w:tcPr>`)
	// DrawingML 单边线条：自闭合 或 成对闭合（RE2 不支持反向引用，故分两条）
	lnSelfRe = regexp.MustCompile(`(?s)<a:ln([LRBT])\b[^>]*/>`)
	lnPairRe = regexp.MustCompile(`(?s)<a:ln([LRBT])\b[^>]*>(.*?)</a:ln[LRBT]>`)

	lnWidthRe   = regexp.MustCompile(`\bw="(\d+)"`)
	lnColorRe   = regexp.MustCompile(`<a:srgbClr\b[^>]*\bval="([0-9A-Fa-f]{6})"`)
	tcBordersRe = regexp.MustCompile(`<w:tcBorders\b`)
	// CT_TcPr 中必须排在 tcBorders 之后的元素，用于决定插入位置
	afterRe = regexp.MustCompile(`<w:(?:shd|noWrap|tcMar|textDirection|tcFitText|vAlign|hideMark)\b`)
)

type zipPart struct {
	name     string
	method   uint16
	modified time.Time
	data     []byte
}

// SanitizeArtifact 在产物入库前清洗 .docx 的非法表格边框，返回新内容与修复的单元格数。
//
// 非 docx、无法解析的包、以及没有缺陷的文件都原样返回（不重新打包），
// 这样绝大多数正常产物不付出任何额外代价。
func SanitizeArtifact(fileName string, data []byte) ([]byte, int) {
	if len(data) == 0 || !strings.EqualFold(filepath.Ext(fileName), ".docx") {
		return data, 0
	}
	return sanitizePackage(data)
}

func sanitizePackage(data []byte) ([]byte, int) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return data, 0
	}

	parts := make([]zipPart, 0, len(zr.File))
	changed := false
	total := 0
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return data, 0
		}
		body, readErr := io.ReadAll(rc)
		rc.Close()
		if readErr != nil {
			return data, 0
		}

		// OPC 规范只允许正斜杠，反斜杠条目会被 WPS / Mac Word 拒绝
		name := strings.ReplaceAll(f.Name, `\`, "/")
		if name != f.Name {
			changed = true
		}
		if partRe.MatchString(name) {
			if fixed, n := sanitizeXML(body); n > 0 {
				body, total, changed = fixed, total+n, true
			}
		}
		parts = append(parts, zipPart{name: name, method: f.Method, modified: f.Modified, data: body})
	}
	if !changed {
		return data, 0
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, p := range parts {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: p.name, Method: p.method, Modified: p.modified})
		if err != nil {
			_ = zw.Close()
			return data, 0
		}
		if _, err := w.Write(p.data); err != nil {
			_ = zw.Close()
			return data, 0
		}
	}
	if err := zw.Close(); err != nil {
		return data, 0
	}
	return buf.Bytes(), total
}

// sanitizeXML 清洗单个部件，返回新字节与修复的单元格数。
func sanitizeXML(data []byte) ([]byte, int) {
	if !bytes.Contains(data, []byte("<a:ln")) {
		return data, 0
	}
	total := 0
	out := tcPrRe.ReplaceAllFunc(data, func(match []byte) []byte {
		open := bytes.Index(match, []byte(">"))
		close := bytes.LastIndex(match, []byte("</w:tcPr>"))
		if open < 0 || close <= open {
			return match
		}
		body := match[open+1 : close]
		if !lnSelfRe.Match(body) && !lnPairRe.Match(body) {
			return match
		}

		sz, color := lnStyle(body)
		cleaned := lnSelfRe.ReplaceAll(body, nil)
		cleaned = lnPairRe.ReplaceAll(cleaned, nil)
		// 已有合法边框时只删非法节点，不重复插入
		if !tcBordersRe.Match(cleaned) {
			borders := tcBorders(sz, color)
			pos := len(cleaned)
			if loc := afterRe.FindIndex(cleaned); loc != nil {
				pos = loc[0]
			}
			merged := make([]byte, 0, len(cleaned)+len(borders))
			merged = append(merged, cleaned[:pos]...)
			merged = append(merged, borders...)
			merged = append(merged, cleaned[pos:]...)
			cleaned = merged
		}
		total++
		return bytes.Replace(match, body, cleaned, 1)
	})
	return out, total
}

// lnStyle 从 DrawingML 线条还原线宽与颜色。
//
// DrawingML 的 w 单位是 EMU（12700 EMU = 1pt），w:sz 单位是 1/8 磅，
// 故 sz = round(EMU/12700*8)，钳制在 2~96。
func lnStyle(body []byte) (int, string) {
	sz, color := 6, "DDE4EE"
	sample := lnSelfRe.Find(body)
	if sample == nil {
		sample = lnPairRe.Find(body)
	}
	if sample == nil {
		return sz, color
	}
	if m := lnWidthRe.FindSubmatch(sample); len(m) > 1 {
		if emu, err := strconv.Atoi(string(m[1])); err == nil {
			sz = int(math.Round(float64(emu) / 12700 * 8))
			sz = max(2, min(96, sz))
		}
	}
	if m := lnColorRe.FindSubmatch(sample); len(m) > 1 {
		color = strings.ToUpper(string(m[1]))
	}
	return sz, color
}

func tcBorders(sz int, color string) []byte {
	var b strings.Builder
	b.WriteString("<w:tcBorders>")
	for _, side := range []string{"top", "left", "bottom", "right"} {
		fmt.Fprintf(&b, `<w:%s w:val="single" w:sz="%d" w:space="0" w:color="%s"/>`, side, sz, color)
	}
	b.WriteString("</w:tcBorders>")
	return []byte(b.String())
}
