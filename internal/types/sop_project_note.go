package types

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ---- 常量：黑板条目类型（decision=决策、run_summary=运行摘要、risk=风险、
// release=发布、note=普通记录）----
const (
	SopNoteKindDecision   = "decision"
	SopNoteKindRunSummary = "run_summary"
	SopNoteKindRisk       = "risk"
	SopNoteKindRelease    = "release"
	SopNoteKindNote       = "note"
)

// ---- 常量：黑板条目来源（human=人工、engine=引擎自动记录、ai=AI 起草后人工确认）----
const (
	SopNoteSourceHuman  = "human"
	SopNoteSourceEngine = "engine"
	SopNoteSourceAI     = "ai"
)

var sopNoteKinds = []string{
	SopNoteKindDecision, SopNoteKindRunSummary, SopNoteKindRisk, SopNoteKindRelease, SopNoteKindNote,
}

var sopNoteSources = []string{SopNoteSourceHuman, SopNoteSourceEngine, SopNoteSourceAI}

// SopProjectNote 项目黑板条目：项目共享上下文的唯一事实来源。
//
// 简报只是黑板的投影（spec §3.10）：人工补充必须落这里，简报文档由引擎渲染，
// 不允许反向把简报里的改动当成事实。
// Seq 是项目内自增序号（软删行不占位），既是渲染水位（brief_rendered_seq）的比对
// 依据，也是时间线的稳定排序键；编辑会推进 seq，保证重新渲染能看到变化。
type SopProjectNote struct {
	ID        string         `gorm:"type:varchar(36);primaryKey" json:"id"`
	TenantID  uint64         `gorm:"index" json:"-"`
	ProjectID string         `gorm:"type:varchar(36);index" json:"project_id"`
	Kind      string         `gorm:"type:varchar(16);default:note" json:"kind"`
	Title     string         `gorm:"type:varchar(500)" json:"title"`
	Body      string         `gorm:"type:text" json:"body"`
	ItemID    string         `gorm:"type:varchar(36)" json:"item_id"`
	RunID     string         `gorm:"type:varchar(36)" json:"run_id"`
	Source    string         `gorm:"type:varchar(16);default:human" json:"source"`
	Pinned    bool           `gorm:"not null;default:false" json:"pinned"`
	Seq       int64          `gorm:"not null;default:0" json:"seq"`
	CreatedBy string         `gorm:"type:varchar(36)" json:"created_by"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

func (SopProjectNote) TableName() string { return "sop_project_notes" }

// BeforeCreate 补主键、时间戳与常量列的默认值（避免零值写成 NULL/空串后校验口径漂移）。
func (n *SopProjectNote) BeforeCreate(_ *gorm.DB) error {
	if n.ID == "" {
		n.ID = uuid.NewString()
	}
	now := time.Now()
	if n.CreatedAt.IsZero() {
		n.CreatedAt = now
	}
	if n.UpdatedAt.IsZero() {
		n.UpdatedAt = now
	}
	if n.Kind == "" {
		n.Kind = SopNoteKindNote
	}
	if n.Source == "" {
		n.Source = SopNoteSourceHuman
	}
	return nil
}

// ErrInvalidSopNote 黑板条目校验失败的哨兵错误；所有校验失败都以它包装。
var ErrInvalidSopNote = fmt.Errorf("sop note: invalid")

// ValidateSopNote 校验条目类型；空字符串按普通记录（note）处理。
func ValidateSopNote(kind string) error {
	if kind == "" {
		return nil
	}
	if !containsString(sopNoteKinds, kind) {
		return fmt.Errorf("%w: unknown kind %q (want one of %s)",
			ErrInvalidSopNote, kind, strings.Join(sopNoteKinds, "/"))
	}
	return nil
}

// ValidateSopNoteSource 校验条目来源；空字符串按人工（human）处理。
func ValidateSopNoteSource(source string) error {
	if source == "" {
		return nil
	}
	if !containsString(sopNoteSources, source) {
		return fmt.Errorf("%w: unknown source %q (want one of %s)",
			ErrInvalidSopNote, source, strings.Join(sopNoteSources, "/"))
	}
	return nil
}

// SopNoteKindLabel 条目类型的中文名（UI 徽标与运行摘要文案复用）。
func SopNoteKindLabel(kind string) string {
	switch kind {
	case SopNoteKindDecision:
		return "决策"
	case SopNoteKindRunSummary:
		return "运行摘要"
	case SopNoteKindRisk:
		return "风险"
	case SopNoteKindRelease:
		return "发布"
	default:
		return "记录"
	}
}
