package main

import (
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// 以下三个 struct 是 PM 源表的只读镜像：GORM 列名/类型标签与
// internal/types/pm.go 的 PMProject / PMRequirement / PMRequirementAttachment 逐列一致，
// 但 JSON 标签按「备份文件要自描述」重写（types 的 TenantID 是 json:"-"、附件正文是 json:"-"），
// 备份才能包含租户与附件字节。本命令只读不写 PM 表，故不定义 BeforeCreate。

type pmProjectSource struct {
	ID          string         `gorm:"type:varchar(36);primaryKey" json:"id"`
	TenantID    uint64         `gorm:"index" json:"tenant_id"`
	Name        string         `gorm:"type:varchar(255)" json:"name"`
	Description string         `gorm:"type:text" json:"description"`
	KBID        string         `gorm:"type:varchar(36)" json:"kb_id"`
	SessionID   string         `gorm:"type:varchar(36)" json:"session_id"`
	Status      string         `gorm:"type:varchar(16);default:active" json:"status"`
	OwnerID     string         `gorm:"type:varchar(36)" json:"owner_id"`
	CreatedBy   string         `gorm:"type:varchar(36)" json:"created_by"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

func (pmProjectSource) TableName() string { return "pm_projects" }

type pmRequirementSource struct {
	ID               string         `gorm:"type:varchar(36);primaryKey" json:"id"`
	TenantID         uint64         `gorm:"index" json:"tenant_id"`
	ProjectID        string         `gorm:"type:varchar(36);index" json:"project_id"`
	Title            string         `gorm:"type:varchar(255)" json:"title"`
	Description      string         `gorm:"type:text" json:"description"`
	Priority         string         `gorm:"type:varchar(8);default:P1" json:"priority"`
	Status           string         `gorm:"type:varchar(32);default:pending_analysis" json:"status"`
	Source           string         `gorm:"type:varchar(32);default:other" json:"source"`
	Proposer         string         `gorm:"type:varchar(128)" json:"proposer"`
	AIAnalysis       types.JSON     `gorm:"type:jsonb" json:"ai_analysis"`
	DesignPlan       string         `gorm:"type:text" json:"design_plan"`
	SessionID        string         `gorm:"type:varchar(36)" json:"session_id"`
	KnowledgeID      string         `gorm:"type:varchar(36)" json:"knowledge_id"`
	ActualLaunchDate *time.Time     `gorm:"type:date" json:"actual_launch_date"`
	EffectNotes      string         `gorm:"type:text" json:"effect_notes"`
	ValueAchieved    string         `gorm:"type:varchar(16)" json:"value_achieved"`
	RetroNotes       string         `gorm:"type:text" json:"retro_notes"`
	RejectReason     string         `gorm:"type:text" json:"reject_reason"`
	ValueAchievedAt  *time.Time     `json:"value_achieved_at"`
	OwnerID          string         `gorm:"type:varchar(36)" json:"owner_id"`
	CreatedBy        string         `gorm:"type:varchar(36)" json:"created_by"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	DeletedAt        gorm.DeletedAt `gorm:"index" json:"-"`
}

func (pmRequirementSource) TableName() string { return "pm_requirements" }

type pmAttachmentSource struct {
	ID            string         `gorm:"type:varchar(36);primaryKey" json:"id"`
	TenantID      uint64         `gorm:"index" json:"tenant_id"`
	RequirementID string         `gorm:"type:varchar(36);index" json:"requirement_id"`
	FileName      string         `gorm:"type:varchar(255)" json:"file_name"`
	ContentType   string         `gorm:"type:varchar(128)" json:"content_type"`
	Size          int64          `json:"size"`
	TextContent   string         `gorm:"type:text" json:"text_content"`
	Data          []byte         `gorm:"type:bytea" json:"data"`
	CreatedBy     string         `gorm:"type:varchar(36)" json:"created_by"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	DeletedAt     gorm.DeletedAt `gorm:"index" json:"-"`
}

func (pmAttachmentSource) TableName() string { return "pm_requirement_attachments" }
