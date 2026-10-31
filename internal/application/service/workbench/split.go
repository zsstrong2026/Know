package workbench

import (
	"fmt"
	"strings"
	"unicode/utf8"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

// archivePart 是一个待归档的分片：Markdown 原文 + 用于知识标题的名字。
type archivePart struct {
	No       int
	Title    string
	Markdown string
}

// partMarkdownMaxRunes 是单个 part 的正文上限。
// 底层 manualContentMaxLength=200000，180k 给清洗与标题留出余量。
const partMarkdownMaxRunes = 180_000

// titleMaxRunes 是知识标题上限；多 part 时还要容纳 "（n/m）" 后缀。
const titleMaxRunes = 200

// maxArchiveParts 与 types.WorkbenchMaxArchiveParts 保持一致，
// 单独抽出常量是为了让拆分函数不依赖 types 包的业务常量命名。
const maxArchiveParts = types.WorkbenchMaxArchiveParts

// splitArchiveMarkdown 按 H2 把 Markdown 拆成若干 part。
//
// 规则（§6.5）：
//  1. 统一换行、TrimSpace；空内容报 400。
//  2. 只有 fenced code block 之外的 `## ` 才开新 part；H1/H3 不拆。
//  3. 没有 H2 时整篇一个 part。
//  4. 单 part 超限时按 空行 → 单行 → rune 边界 降级拆分；
//     单个 fenced block 本身超限则报 413，不产生半截代码块。
//  5. 最多 20 个 part，超过报 413。
//  6. 单 part 用原题；多 part 用 `原题（1/3）`，原题超长先截断。
func splitArchiveMarkdown(title, markdown string) ([]archivePart, error) {
	normalized := normalizeMarkdown(markdown)
	if strings.TrimSpace(normalized) == "" {
		return nil, apperrors.NewBadRequestError("archive markdown is empty")
	}

	rawSections, err := splitTopLevelH2(normalized)
	if err != nil {
		return nil, err
	}

	// 单节超长时继续降级拆分，直到每节都不超限。
	expanded := make([]string, 0, len(rawSections))
	for _, section := range rawSections {
		pieces, err := splitOversizedSection(section)
		if err != nil {
			return nil, err
		}
		expanded = append(expanded, pieces...)
	}
	if len(expanded) > maxArchiveParts {
		return nil, apperrors.NewWorkbenchTooLarge(int64(maxArchiveParts), int64(len(expanded)))
	}

	baseTitle := truncateTitle(strings.TrimSpace(title))
	total := len(expanded)
	parts := make([]archivePart, 0, total)
	for i, body := range expanded {
		parts = append(parts, archivePart{
			No:       i + 1,
			Title:    partTitle(baseTitle, i+1, total),
			Markdown: body,
		})
	}
	return parts, nil
}

// normalizeMarkdown 统一 CRLF/CR 为 LF，并去掉首尾空白。
func normalizeMarkdown(markdown string) string {
	replaced := strings.ReplaceAll(markdown, "\r\n", "\n")
	replaced = strings.ReplaceAll(replaced, "\r", "\n")
	return strings.TrimSpace(replaced)
}

// splitTopLevelH2 按 fenced block 之外的 `## ` 行切分。
//
// 返回的每一节都保留自己的 `## ` 标题行 —— 归档到知识库里时，
// 该标题就是浏览器里能看到的第一个标题，去掉会让全文缺一级结构。
func splitTopLevelH2(markdown string) ([]string, error) {
	lines := strings.Split(markdown, "\n")
	sections := make([]string, 0, 4)
	current := make([]string, 0, len(lines))
	var fence string // 当前未闭合的 fence 标记（``` 或 ~~~），空表示不在代码块内

	for _, line := range lines {
		trimmed := strings.TrimLeft(line, " ")
		indent := len(line) - len(trimmed)

		if fence == "" && indent <= 3 {
			if marker := fenceMarker(trimmed); marker != "" {
				fence = marker
			} else if isH2(trimmed) {
				if len(current) > 0 {
					sections = append(sections, strings.TrimSpace(strings.Join(current, "\n")))
				}
				current = make([]string, 0, 64)
			}
		} else if fence != "" && indent <= 3 && strings.HasPrefix(trimmed, fence) {
			fence = ""
		}
		current = append(current, line)
	}
	if fence != "" {
		// 未闭合的代码块：按原文照收，不报错（用户文档可能确实没写完，
		// 但归档不该因此失败；后续切分仍会遵守同一 fence 状态）。
		fence = ""
	}
	if len(current) > 0 {
		if body := strings.TrimSpace(strings.Join(current, "\n")); body != "" {
			sections = append(sections, body)
		}
	}
	if len(sections) == 0 {
		return []string{strings.TrimSpace(markdown)}, nil
	}
	// 第一个 H2 之前的“前言”不单独成 part：否则一段简短的导语会变成一个
	// 只有两行、标题却带 (1/N) 的独立知识，既难检索也难读。并入第一节。
	if len(sections) > 1 && !strings.HasPrefix(sections[0], "## ") {
		merged := sections[0] + "\n\n" + sections[1]
		sections = append([]string{merged}, sections[2:]...)
	}
	return sections, nil
}

// fenceMarker 识别行首的代码块标记；返回 ``` 或 ~~~，否则空串。
func fenceMarker(line string) string {
	for _, marker := range []string{"```", "~~~"} {
		if strings.HasPrefix(line, marker) {
			return marker
		}
	}
	return ""
}

// isH2 判断是否是二级标题（`## ` 后必须有内容或行尾）。
func isH2(line string) bool {
	if !strings.HasPrefix(line, "## ") {
		return false
	}
	return strings.TrimSpace(strings.TrimPrefix(line, "## ")) != ""
}

// splitOversizedSection 把超过 partMarkdownMaxRunes 的单节继续拆分。
//
// 降级顺序：空行 → 单行 → rune 边界。前两级都不会切断段落/代码块，
// 只有整块都无法满足时才按 rune 硬切，并在切点尽量避开代码块内部。
func splitOversizedSection(section string) ([]string, error) {
	if utf8.RuneCountInString(section) <= partMarkdownMaxRunes {
		return []string{section}, nil
	}
	// 一个 fenced block 自身超限：拒绝，提示用户拆文档，而不是产出半截代码。
	if block, ok := oversizedFenceBlock(section); ok {
		return nil, apperrors.NewWorkbenchTooLarge(
			int64(partMarkdownMaxRunes), int64(utf8.RuneCountInString(block)))
	}

	chunks := splitByBlankLines(section)
	out := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		if utf8.RuneCountInString(chunk) <= partMarkdownMaxRunes {
			out = append(out, chunk)
			continue
		}
		// 单个空行块仍超限：按行拆。
		for _, piece := range splitByLines(chunk, partMarkdownMaxRunes) {
			out = append(out, piece)
		}
	}
	return out, nil
}

// oversizedFenceBlock 找出第一个自身超过上限的 fenced block。
func oversizedFenceBlock(section string) (string, bool) {
	var fence string
	var block []string
	for _, line := range strings.Split(section, "\n") {
		trimmed := strings.TrimLeft(line, " ")
		if fence == "" {
			if marker := fenceMarker(trimmed); marker != "" {
				fence = marker
				block = []string{line}
			}
			continue
		}
		block = append(block, line)
		if strings.HasPrefix(trimmed, fence) {
			joined := strings.Join(block, "\n")
			if utf8.RuneCountInString(joined) > partMarkdownMaxRunes {
				return joined, true
			}
			fence = ""
			block = nil
		}
	}
	return "", false
}

// splitByBlankLines 按空行聚合，直到接近上限。
func splitByBlankLines(section string) []string {
	blocks := strings.Split(section, "\n\n")
	out := make([]string, 0, len(blocks))
	var buffer strings.Builder
	flush := func() {
		if text := strings.TrimSpace(buffer.String()); text != "" {
			out = append(out, text)
		}
		buffer.Reset()
	}
	for _, block := range blocks {
		candidate := block
		if buffer.Len() > 0 {
			candidate = buffer.String() + "\n\n" + block
		}
		if utf8.RuneCountInString(candidate) > partMarkdownMaxRunes {
			flush()
		}
		if buffer.Len() > 0 {
			buffer.WriteString("\n\n")
		}
		buffer.WriteString(block)
	}
	flush()
	if len(out) == 0 {
		return []string{section}
	}
	return out
}

// splitByLines 把单块按行聚合成不超过 limit 的片段。
func splitByLines(chunk string, limit int) []string {
	out := make([]string, 0, 4)
	var buffer strings.Builder
	flush := func() {
		if text := strings.TrimSpace(buffer.String()); text != "" {
			out = append(out, text)
		}
		buffer.Reset()
	}
	for _, line := range strings.Split(chunk, "\n") {
		// 单行本身就超限：只能按 rune 硬切（极少见，如无换行的长表格行）。
		if utf8.RuneCountInString(line) > limit {
			flush()
			for _, piece := range hardSplitRunes(line, limit) {
				out = append(out, piece)
			}
			continue
		}
		candidate := line
		if buffer.Len() > 0 {
			candidate = buffer.String() + "\n" + line
		}
		if utf8.RuneCountInString(candidate) > limit {
			flush()
		}
		if buffer.Len() > 0 {
			buffer.WriteString("\n")
		}
		buffer.WriteString(line)
	}
	flush()
	if len(out) == 0 {
		return []string{chunk}
	}
	return out
}

// hardSplitRunes 按 rune 边界切分（不会切断多字节字符）。
func hardSplitRunes(s string, limit int) []string {
	runes := []rune(s)
	out := make([]string, 0, len(runes)/limit+1)
	for start := 0; start < len(runes); start += limit {
		end := start + limit
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, string(runes[start:end]))
	}
	return out
}

// truncateTitle 把标题裁到 titleMaxRunes 以内，超出加省略号。
func truncateTitle(title string) string {
	if title == "" {
		return "未命名归档"
	}
	if utf8.RuneCountInString(title) <= titleMaxRunes {
		return title
	}
	runes := []rune(title)
	return string(runes[:titleMaxRunes-1]) + "…"
}

// partTitle 生成 part 标题：单 part 用原题，多 part 加 `（n/m）` 后缀。
// 后缀必须计入 200 rune 上限，所以要先把基础标题压到"上限减后缀长度"。
func partTitle(base string, no, total int) string {
	if total <= 1 {
		return base
	}
	suffix := fmt.Sprintf("（%d/%d）", no, total)
	budget := titleMaxRunes - utf8.RuneCountInString(suffix)
	if budget <= 0 {
		return suffix
	}
	runes := []rune(base)
	if len(runes) > budget {
		base = string(runes[:budget-1]) + "…"
	}
	return base + suffix
}
