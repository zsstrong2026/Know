package workbench

import (
	"strings"
	"testing"
	"unicode/utf8"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 没有 H2 时整篇一个 part，且标题就是原题。
func TestSplitArchiveMarkdownWithoutH2(t *testing.T) {
	parts, err := splitArchiveMarkdown("单节文档", "# 一级标题\n\n正文内容")
	require.NoError(t, err)
	require.Len(t, parts, 1)
	assert.Equal(t, 1, parts[0].No)
	assert.Equal(t, "单节文档", parts[0].Title)
	assert.Contains(t, parts[0].Markdown, "正文内容")
}

// 多个 H2 按顺序拆成多个 part，标题带 (n/m) 后缀。
// 第一个 H2 之前的前言并入第一节，不单独成 part。
func TestSplitArchiveMarkdownByH2(t *testing.T) {
	markdown := "前言\n\n## 第一节\n\n内容 A\n\n## 第二节\n\n内容 B\n\n## 第三节\n\n内容 C"
	parts, err := splitArchiveMarkdown("周报", markdown)
	require.NoError(t, err)
	require.Len(t, parts, 3)

	assert.Equal(t, "周报（1/3）", parts[0].Title)
	assert.Contains(t, parts[0].Markdown, "前言", "前言并入第一节")
	assert.Contains(t, parts[0].Markdown, "## 第一节")
	assert.Contains(t, parts[0].Markdown, "内容 A")
	assert.Equal(t, "周报（2/3）", parts[1].Title)
	assert.Contains(t, parts[1].Markdown, "## 第二节")
	assert.Equal(t, "周报（3/3）", parts[2].Title)
	assert.Contains(t, parts[2].Markdown, "内容 C")

	// part_no 必须从 1 连续编号：更新归档按 part_no 对齐。
	for i, part := range parts {
		assert.Equal(t, i+1, part.No)
	}
}

// 代码块里的 `## ` 不是标题，不能被拆开。
func TestSplitArchiveMarkdownIgnoresH2InsideFence(t *testing.T) {
	markdown := "## 真实标题\n\n```bash\n## 这是注释不是标题\necho hi\n```\n\n结尾"
	parts, err := splitArchiveMarkdown("代码文档", markdown)
	require.NoError(t, err)
	require.Len(t, parts, 1, "fenced block 内的 ## 不得开新 part")
	assert.Contains(t, parts[0].Markdown, "## 这是注释不是标题")
}

// 波浪号 fence 同样生效。
func TestSplitArchiveMarkdownHandlesTildeFence(t *testing.T) {
	markdown := "## 第一节\n\n~~~\n## 不是标题\n~~~\n\n## 第二节\n\n内容"
	parts, err := splitArchiveMarkdown("文档", markdown)
	require.NoError(t, err)
	require.Len(t, parts, 2)
}

// H1/H3 不参与拆分。
func TestSplitArchiveMarkdownIgnoresH1AndH3(t *testing.T) {
	markdown := "# 一级\n\n### 三级\n\n正文"
	parts, err := splitArchiveMarkdown("文档", markdown)
	require.NoError(t, err)
	require.Len(t, parts, 1)
}

// CRLF 必须被统一成 LF，否则 fenced block 判定与 rune 计数都会失真。
func TestSplitArchiveMarkdownNormalizesCRLF(t *testing.T) {
	markdown := "## 第一节\r\n\r\n内容 A\r\n\r\n## 第二节\r\n\r\n内容 B"
	parts, err := splitArchiveMarkdown("文档", markdown)
	require.NoError(t, err)
	require.Len(t, parts, 2)
	for _, part := range parts {
		assert.NotContains(t, part.Markdown, "\r", "拆分结果不应残留 CR")
	}
}

// 空内容必须 400，而不是产出一个空知识。
func TestSplitArchiveMarkdownRejectsEmpty(t *testing.T) {
	for _, markdown := range []string{"", "   ", "\n\n\n", "\r\n"} {
		_, err := splitArchiveMarkdown("标题", markdown)
		appErr, ok := apperrors.IsAppError(err)
		require.Truef(t, ok, "空内容必须是 AppError，input=%q got=%v", markdown, err)
		assert.Equal(t, apperrors.ErrBadRequest, appErr.Code)
	}
}

// 单个 H2 超限时按空行降级拆分，且每片都不超上限。
func TestSplitArchiveMarkdownSplitsOversizedSection(t *testing.T) {
	// 每段 1000 rune，共 300 段 → 约 30 万 rune，必须被拆开。
	para := strings.Repeat("字", 1000)
	blocks := make([]string, 0, 300)
	for i := 0; i < 300; i++ {
		blocks = append(blocks, para)
	}
	markdown := "## 超长章节\n\n" + strings.Join(blocks, "\n\n")

	parts, err := splitArchiveMarkdown("长文", markdown)
	require.NoError(t, err)
	require.Greater(t, len(parts), 1, "超长必须拆成多片")
	for _, part := range parts {
		assert.LessOrEqual(t, utf8.RuneCountInString(part.Markdown), partMarkdownMaxRunes,
			"每片都不得超过 %d rune", partMarkdownMaxRunes)
	}
	// 内容不能丢：所有分片的正文长度之和应接近原文（去掉分隔空行）。
	total := 0
	for _, part := range parts {
		total += utf8.RuneCountInString(part.Markdown)
	}
	assert.Greater(t, total, 290_000, "拆分不得丢内容")
}

// 单个 fenced block 自身超限时必须 413，而不是切出半截代码块。
func TestSplitArchiveMarkdownRejectsOversizedFence(t *testing.T) {
	code := strings.Repeat("x", partMarkdownMaxRunes+1000)
	markdown := "## 大代码块\n\n```\n" + code + "\n```"

	_, err := splitArchiveMarkdown("文档", markdown)
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok, "必须是 AppError，got %v", err)
	assert.Equal(t, apperrors.ErrWorkbenchTooLarge, appErr.Code)
}

// 超过 20 个 part 必须 413。
func TestSplitArchiveMarkdownRejectsTooManyParts(t *testing.T) {
	blocks := make([]string, 0, types.WorkbenchMaxArchiveParts+1)
	for i := 0; i <= types.WorkbenchMaxArchiveParts; i++ {
		blocks = append(blocks, "## 第"+strings.Repeat("x", i+1)+" 节\n\n内容")
	}
	_, err := splitArchiveMarkdown("文档", strings.Join(blocks, "\n\n"))
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	assert.Equal(t, apperrors.ErrWorkbenchTooLarge, appErr.Code)
}

// 标题超长时要截断，并保证加上 (n/m) 后缀后仍不超过 200 rune。
func TestPartTitleStaysWithinLimit(t *testing.T) {
	longTitle := strings.Repeat("题", 300)
	parts, err := splitArchiveMarkdown(longTitle, "## A\n\n1\n\n## B\n\n2")
	require.NoError(t, err)
	require.Len(t, parts, 2)
	for _, part := range parts {
		assert.LessOrEqual(t, utf8.RuneCountInString(part.Title), titleMaxRunes,
			"标题不得超出 %d rune", titleMaxRunes)
		assert.Contains(t, part.Title, "题")
	}
	// 中文标题不能被切成半个字符。
	assert.True(t, utf8.ValidString(parts[0].Title))
}

// 未闭合的 fence 不应让拆分失败（用户可能确实没写完）。
func TestSplitArchiveMarkdownToleratesUnclosedFence(t *testing.T) {
	markdown := "## 第一节\n\n```go\nfunc main() {}\n"
	parts, err := splitArchiveMarkdown("文档", markdown)
	require.NoError(t, err)
	require.Len(t, parts, 1)
	assert.Contains(t, parts[0].Markdown, "func main")
}

// normalizeMarkdown 单独验证：CRLF/CR 都要归一，首尾空白要去掉。
func TestNormalizeMarkdown(t *testing.T) {
	assert.Equal(t, "a\nb\nc", normalizeMarkdown("  a\r\nb\rc  "))
	assert.Equal(t, "", normalizeMarkdown("   "))
}
