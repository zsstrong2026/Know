package types

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ---- 常量：澄清状态（只允许 open → answered 单向）----
const (
	SopClarificationStatusOpen     = "open"
	SopClarificationStatusAnswered = "answered"
)

// ---- 常量：澄清来源 ----
const (
	SopClarificationSourceAI    = "ai"
	SopClarificationSourceHuman = "human"
)

// ---- 常量：活动分类（business 业务动态 / audit 审计留痕）----
const (
	SopActivityKindBusiness = "business"
	SopActivityKindAudit    = "audit"
)

// SopItemClarification 事项澄清问答：AI 或人工提出问题，回答后置 answered，
// 除首次回答外不可再改（只增不改，spec §3.9）。
type SopItemClarification struct {
	ID       string `gorm:"type:varchar(36);primaryKey" json:"id"`
	TenantID uint64 `gorm:"index" json:"-"`
	ItemID   string `gorm:"type:varchar(36);index" json:"item_id"`
	Question string `gorm:"type:text" json:"question"`
	Answer   string `gorm:"type:text" json:"answer"`
	// Status ∈ open/answered；写入后唯一的变化是 open → answered。
	Status string `gorm:"type:varchar(16);default:open" json:"status"`
	// Source ∈ ai/human，记录提问方。
	Source     string         `gorm:"type:varchar(16);default:ai" json:"source"`
	CreatedAt  time.Time      `json:"created_at"`
	AnsweredAt *time.Time     `json:"answered_at"`
	DeletedAt  gorm.DeletedAt `gorm:"index" json:"-"`
}

func (SopItemClarification) TableName() string { return "sop_item_clarifications" }

// BeforeCreate 补主键、创建时间与默认状态/来源，避免空值落成 NULL。
func (c *SopItemClarification) BeforeCreate(_ *gorm.DB) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now()
	}
	if c.Status == "" {
		c.Status = SopClarificationStatusOpen
	}
	if c.Source == "" {
		c.Source = SopClarificationSourceAI
	}
	return nil
}

// CanAnswerSopClarification 澄清是否可答：仅 open 可写答案，answered 视为终态。
func CanAnswerSopClarification(status string) bool {
	return status == SopClarificationStatusOpen
}

// SopItemActivity 事项活动日志：只增不改不删，故无 deleted_at 与 UpdatedAt。
type SopItemActivity struct {
	ID       string `gorm:"type:varchar(36);primaryKey" json:"id"`
	TenantID uint64 `gorm:"index" json:"-"`
	ItemID   string `gorm:"type:varchar(36);index" json:"item_id"`
	// Kind ∈ business/audit。
	Kind      string    `gorm:"type:varchar(16)" json:"kind"`
	Actor     string    `gorm:"type:varchar(200)" json:"actor"`
	Action    string    `gorm:"type:varchar(100)" json:"action"`
	Detail    string    `gorm:"type:text" json:"detail"`
	CreatedAt time.Time `json:"created_at"`
}

func (SopItemActivity) TableName() string { return "sop_item_activities" }

func (a *SopItemActivity) BeforeCreate(_ *gorm.DB) error {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}
	return nil
}
