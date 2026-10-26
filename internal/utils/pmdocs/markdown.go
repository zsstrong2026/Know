// Package pmdocs 把资料正文（Markdown）导出为 docx/pptx。
// 仅依赖标准库手写 OOXML，避免为两种格式引入新的第三方依赖。
package pmdocs

import "strings"

// 块类型：标题层级 1~3，列表项用 levelList，正文为 levelBody
const (
	levelBody = 0
	levelList = 4
)

// Block 正文最小结构单元：Level 决定导出时的字号与缩进
type Block struct {
	Level int
	Text  string
}

// Parse 轻量 Markdown 解析：只识别标题、列表与普通段落，足够覆盖资料生成结果。
// 表格、代码块等复杂语法按普通文本输出，保证导出不丢内容。
func Parse(markdown string) []Block {
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	blocks := make([]Block, 0, len(lines))
	inFence := false
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "```") {
			inFence = !inFence // 代码围栏标记本身不输出
			continue
		}
		if line == "" {
			continue
		}
		if inFence {
			blocks = append(blocks, Block{Level: levelBody, Text: line})
			continue
		}
		if level, text, ok := parseHeading(line); ok {
			blocks = append(blocks, Block{Level: level, Text: text})
			continue
		}
		if text, ok := parseListItem(line); ok {
			blocks = append(blocks, Block{Level: levelList, Text: text})
			continue
		}
		blocks = append(blocks, Block{Level: levelBody, Text: stripInlineMarks(line)})
	}
	return blocks
}

// parseHeading 识别 # ~ ###（更深的标题统一降级为三级，避免导出层级过深）
func parseHeading(line string) (int, string, bool) {
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	if n == 0 || n >= len(line) || line[n] != ' ' {
		return 0, "", false
	}
	level := n
	if level > 3 {
		level = 3
	}
	return level, stripInlineMarks(strings.TrimSpace(line[n+1:])), true
}

// parseListItem 识别 - / * / + 列表项
func parseListItem(line string) (string, bool) {
	if len(line) < 2 {
		return "", false
	}
	switch line[0] {
	case '-', '*', '+':
		if line[1] == ' ' {
			return stripInlineMarks(strings.TrimSpace(line[2:])), true
		}
	}
	return "", false
}

// stripInlineMarks 去掉行内强调标记，导出为纯文本
func stripInlineMarks(s string) string {
	return strings.NewReplacer("**", "", "__", "", "`", "").Replace(s)
}
