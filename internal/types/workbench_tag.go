package types

import (
	"errors"
	"strings"
	"time"
)

// 工作台标签（P4-2）。
//
// 归属口径：**个人标签**（tenant_id + owner_id），与条目/文件夹/模板一致。
// 同一 workspace 的其他成员（含 Admin）看不到、也用不了；离开 workspace 时随个人数据一起清理。
//
// 设计取舍：
//   - 颜色只允许白名单值，避免把任意字符串塞进前端样式；
//   - 同一用户的标签名唯一（软删后可重建同名），重名直接 409 语义的 400；
//   - 一个条目的标签数上限 20，够用且能挡住"批量打标"把卡片撑爆。
const (
	WorkbenchTagNameMaxRunes  = 40
	WorkbenchTagsPerItemLimit = 20
)

// WorkbenchTagColorDefault 默认色；前端按名字映射到具体色值。
const WorkbenchTagColorDefault = "gray"

// WorkbenchTagColors 允许的颜色白名单。
var WorkbenchTagColors = []string{"gray", "blue", "green", "yellow", "red", "purple"}

var (
	ErrWorkbenchTagNameEmpty = errors.New("workbench tag: name is required")
	ErrWorkbenchTagNameDup   = errors.New("workbench tag: name already exists")
	ErrWorkbenchTagColor     = errors.New("workbench tag: color is not allowed")
)

// WorkbenchTag 一个个人标签。
type WorkbenchTag struct {
	ID       string `json:"id"`
	TenantID uint64 `json:"-"`
	OwnerID  string `json:"-"`
	Name     string `json:"name"`
	Color    string `json:"color"`
	// ItemCount 是打上该标签的条目数；列表接口顺带给出，省一次聚合查询。
	ItemCount int       `json:"item_count"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// WorkbenchTagRequest 新建 / 改名标签。
type WorkbenchTagRequest struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

// WorkbenchItemTagsRequest 覆盖式设置某个条目的标签集合。
type WorkbenchItemTagsRequest struct {
	TagIDs []string `json:"tag_ids"`
}

// Normalize 去空白并按白名单兜底颜色。
func (r *WorkbenchTagRequest) Normalize() {
	if r == nil {
		return
	}
	r.Name = strings.TrimSpace(r.Name)
	r.Color = strings.TrimSpace(strings.ToLower(r.Color))
	if r.Color == "" {
		r.Color = WorkbenchTagColorDefault
	}
}

// Validate 校验名称长度与颜色白名单。
func (r WorkbenchTagRequest) Validate() error {
	if strings.TrimSpace(r.Name) == "" {
		return ErrWorkbenchTagNameEmpty
	}
	if len([]rune(r.Name)) > WorkbenchTagNameMaxRunes {
		return &WorkbenchAILengthError{Field: "name", Limit: WorkbenchTagNameMaxRunes, Actual: len([]rune(r.Name))}
	}
	if !workbenchTagColorAllowed(r.Color) {
		return ErrWorkbenchTagColor
	}
	return nil
}

// IsWorkbenchTagColor 判断颜色是否在白名单内；服务层改颜色时单独用它校验。
func IsWorkbenchTagColor(color string) bool {
	return workbenchTagColorAllowed(color)
}

func workbenchTagColorAllowed(color string) bool {
	for _, allowed := range WorkbenchTagColors {
		if color == allowed {
			return true
		}
	}
	return false
}

// NormalizeItemTags 去空、去重并保序；超过上限截断。
func NormalizeItemTags(ids []string) []string {
	out := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		out = append(out, trimmed)
		if len(out) >= WorkbenchTagsPerItemLimit {
			break
		}
	}
	return out
}
