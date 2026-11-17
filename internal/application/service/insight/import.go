// 方法包导入：解析 SKILL.md 的 frontmatter，返回主题草稿（不落库）。
package insight

import (
	"archive/zip"
	"bytes"
	"io"
	"path"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

const insightImportMaxRunes = 8000

// skillFrontmatter 是 SKILL.md 顶部声明的最小结构。
type skillFrontmatter struct {
	Name        string                   `yaml:"name"`
	Description string                   `yaml:"description"`
	Sensitive   bool                     `yaml:"sensitive"`
	Dimensions  []types.InsightDimension `yaml:"dimensions"`
}

// ImportThemePackage 解析 .md 或 .zip 方法包，返回可直接提交创建的主题草稿。
//
// 方法包只作为「分析口径 + 提示词规范」读取，不执行其中任何代码，
// 也不进入沙箱镜像。
func (s *Service) ImportThemePackage(
	filename string, data []byte,
) (*types.CreateInsightThemeRequest, error) {
	content, err := readMethodPackage(filename, data)
	if err != nil {
		return nil, err
	}
	front, body := splitFrontmatter(content)

	var fm skillFrontmatter
	if strings.TrimSpace(front) != "" {
		if err := yaml.Unmarshal([]byte(front), &fm); err != nil {
			return nil, apperrors.NewValidationError("method package frontmatter is invalid").
				WithDetails(err.Error())
		}
	}

	name := strings.TrimSpace(fm.Name)
	if name == "" {
		name = strings.TrimSpace(strings.TrimSuffix(path.Base(filename), path.Ext(filename)))
	}
	if name == "" {
		return nil, apperrors.NewValidationError("method package name is required")
	}

	dimensions := make([]types.InsightDimension, 0, len(fm.Dimensions))
	for _, dim := range fm.Dimensions {
		dim.Name = strings.TrimSpace(dim.Name)
		if dim.Name == "" {
			continue
		}
		dim.Criteria = strings.TrimSpace(dim.Criteria)
		if strings.TrimSpace(dim.Scale) == "" {
			dim.Scale = "0-100"
		}
		dimensions = append(dimensions, dim)
	}
	if len(dimensions) == 0 {
		// 没有声明维度时给一个可编辑的默认维度，避免创建出无法出报告的主题。
		dimensions = []types.InsightDimension{{
			Name:     "核心结论",
			Criteria: "从文档中提炼与主题相关的结论",
			Scale:    "0-100",
		}}
	}

	instructions := strings.TrimSpace(body)
	if utf8.RuneCountInString(instructions) > insightImportMaxRunes {
		instructions = string([]rune(instructions)[:insightImportMaxRunes])
	}

	return &types.CreateInsightThemeRequest{
		Name:         name,
		Description:  strings.TrimSpace(fm.Description),
		Sensitive:    fm.Sensitive,
		Dimensions:   dimensions,
		Instructions: instructions,
	}, nil
}

// readMethodPackage 读取包内 SKILL.md；.zip 取层级最浅的一份，.md 直接使用。
func readMethodPackage(filename string, data []byte) (string, error) {
	if !strings.EqualFold(path.Ext(filename), ".zip") {
		return string(data), nil
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", apperrors.NewValidationError("invalid zip package")
	}
	best := ""
	bestDepth := 1 << 30
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		if !strings.EqualFold(path.Base(entry.Name), "SKILL.md") {
			continue
		}
		depth := strings.Count(strings.Trim(entry.Name, "/"), "/")
		if depth >= bestDepth {
			continue
		}
		opened, err := entry.Open()
		if err != nil {
			continue
		}
		raw, readErr := io.ReadAll(io.LimitReader(opened, 1<<20))
		opened.Close()
		if readErr != nil {
			continue
		}
		best = string(raw)
		bestDepth = depth
	}
	if best == "" {
		return "", apperrors.NewValidationError("method package must contain SKILL.md")
	}
	return best, nil
}

// splitFrontmatter 分离 YAML frontmatter 与正文。
func splitFrontmatter(content string) (front, body string) {
	text := strings.TrimPrefix(content, "\ufeff")
	text = strings.TrimLeft(text, " \r\n\t")
	if !strings.HasPrefix(text, "---") {
		return "", text
	}
	rest := text[3:]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return "", text
	}
	return strings.TrimSpace(rest[:idx]), strings.TrimSpace(rest[idx+4:])
}
