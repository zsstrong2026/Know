package types

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// 工作台文档模板（§9.16）。
//
// 模板是**个人资源**：按 tenant_id + owner_id 隔离，Admin 不可读；
// 模板不是知识，不写工作知识库、不参与归档/检索/AI 引用。
// 服务端只复制与存储 Y.Doc 字节，不解析、不合并、不应用模板。

const (
	// WorkbenchTemplateNameMaxRunes 名称上限（按 rune 计，trim 后不能为空）
	WorkbenchTemplateNameMaxRunes = 60
	// WorkbenchTemplateDescMaxRunes 描述上限
	WorkbenchTemplateDescMaxRunes = 200
	// WorkbenchTemplateMaxPerOwner 每用户模板数量上限（UI 预检，服务端兜底）
	WorkbenchTemplateMaxPerOwner = 50
	// WorkbenchTemplateMaxYDocBytes 模板快照上限 1MB（比条目 8MB 更严）
	WorkbenchTemplateMaxYDocBytes = 1 << 20
)

// WorkbenchTemplate 用户模板；YDoc 只存字节，不出现在列表响应里。
type WorkbenchTemplate struct {
	ID                  string    `json:"-" gorm:"type:varchar(36);primaryKey"`
	TenantID            uint64    `json:"-" gorm:"not null"`
	OwnerID             string    `json:"-" gorm:"type:varchar(36);not null"`
	Name                string    `json:"name" gorm:"type:varchar(60);not null"`
	Description         string    `json:"description" gorm:"type:varchar(200);not null;default:''"`
	YDoc                []byte    `json:"-" gorm:"column:ydoc;type:bytea;not null"`
	SizeBytes           int       `json:"size_bytes" gorm:"not null"`
	EditorSchemaVersion int       `json:"editor_schema_version" gorm:"not null;default:1"`
	// 只做溯源，不建外键、不级联：源条目删除不影响模板。
	SourceItemID *string   `json:"source_item_id,omitempty" gorm:"type:varchar(36)"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// WorkbenchTemplateSummary 列表项：不含 ydoc，最多 50 条，不分页。
type WorkbenchTemplateSummary struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	Description         string    `json:"description"`
	SizeBytes           int       `json:"size_bytes"`
	EditorSchemaVersion int       `json:"editor_schema_version"`
	SourceItemID        *string   `json:"source_item_id,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// ToSummary 投影为列表项；绝不携带 YDoc。
func (t *WorkbenchTemplate) ToSummary() *WorkbenchTemplateSummary {
	if t == nil {
		return nil
	}
	return &WorkbenchTemplateSummary{
		ID:                  t.ID,
		Name:                t.Name,
		Description:         t.Description,
		SizeBytes:           t.SizeBytes,
		EditorSchemaVersion: t.EditorSchemaVersion,
		SourceItemID:        t.SourceItemID,
		CreatedAt:           t.CreatedAt,
		UpdatedAt:           t.UpdatedAt,
	}
}

// WorkbenchTemplateContent 套用模板时返回的快照；YDoc 为 base64 字符串。
type WorkbenchTemplateContent struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	YDoc                string `json:"ydoc"`
	EditorSchemaVersion int    `json:"editor_schema_version"`
	SizeBytes           int    `json:"size_bytes"`
}

// CreateWorkbenchTemplateRequest 从**已保存的文档**复制模板，不接受客户端上传快照。
type CreateWorkbenchTemplateRequest struct {
	SourceItemID string `json:"source_item_id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
}

// Validate 校验名称与描述；source_item_id 的归属与状态由服务层确认。
func (r *CreateWorkbenchTemplateRequest) Validate() error {
	if r == nil {
		return ErrWorkbenchInvalidTemplateName
	}
	r.SourceItemID = strings.TrimSpace(r.SourceItemID)
	r.Name = strings.TrimSpace(r.Name)
	r.Description = strings.TrimSpace(r.Description)
	if r.SourceItemID == "" {
		return fmt.Errorf("%w: empty source", ErrWorkbenchInvalidTemplateSource)
	}
	if err := validateTemplateName(r.Name); err != nil {
		return err
	}
	return validateTemplateDescription(r.Description)
}

// UpdateWorkbenchTemplateRequest 只允许改名称与描述。
type UpdateWorkbenchTemplateRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

// Validate 至少给一个字段，且给定字段必须合法。
func (r *UpdateWorkbenchTemplateRequest) Validate() error {
	if r == nil || (r.Name == nil && r.Description == nil) {
		return ErrWorkbenchInvalidTemplateName
	}
	if r.Name != nil {
		trimmed := strings.TrimSpace(*r.Name)
		if err := validateTemplateName(trimmed); err != nil {
			return err
		}
		r.Name = &trimmed
	}
	if r.Description != nil {
		trimmed := strings.TrimSpace(*r.Description)
		if err := validateTemplateDescription(trimmed); err != nil {
			return err
		}
		r.Description = &trimmed
	}
	return nil
}

func validateTemplateName(name string) error {
	n := utf8.RuneCountInString(name)
	if n == 0 || n > WorkbenchTemplateNameMaxRunes {
		return fmt.Errorf("%w: %d runes", ErrWorkbenchInvalidTemplateName, n)
	}
	return nil
}

func validateTemplateDescription(desc string) error {
	if utf8.RuneCountInString(desc) > WorkbenchTemplateDescMaxRunes {
		return fmt.Errorf("%w: %d runes", ErrWorkbenchInvalidTemplateDescription, len([]rune(desc)))
	}
	return nil
}
