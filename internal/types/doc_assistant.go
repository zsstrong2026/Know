package types

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// 文档助手模块常量
const (
	// 任务状态
	DocTaskStatusPending  = "pending"
	DocTaskStatusRunning  = "running"
	DocTaskStatusSuccess  = "success"
	DocTaskStatusPartial  = "partial_success"
	DocTaskStatusFailed   = "failed"
	DocTaskStatusCanceled = "cancelled"

	// 章节状态
	DocChapterStatusPending    = "pending"
	DocChapterStatusRetrieving = "retrieving"
	DocChapterStatusGenerating = "generating"
	DocChapterStatusDone       = "done"
	DocChapterStatusFailed     = "failed"
	DocChapterStatusSkipped    = "skipped"

	// 文档状态
	DocDocumentStatusGenerated = "generated"
	DocDocumentStatusEdited    = "edited"

	// 模板校验上限
	DocTemplateMaxChapters    = 30
	DocTemplateMaxWordsUpper  = 5000
	DocTemplateMaxWordsLower  = 200
	DocDefaultTopK            = 8
	DocMaxTopK                = 20

	// 全局文档列表分页上限（P2-5）
	DocDocumentsDefaultLimit = 30
	DocDocumentsMaxLimit     = 100
)

// NewDocAssistantID 生成文档助手实体 ID
func NewDocAssistantID() string {
	return uuid.New().String()
}

// DocTemplateGlobalConfig 模板全局配置
type DocTemplateGlobalConfig struct {
	DefaultTone    string `json:"default_tone,omitempty"`
	TargetAudience string `json:"target_audience,omitempty"`
	DefaultExtra   string `json:"default_extra_requirement,omitempty"`
	TopK           int    `json:"top_k,omitempty"`
}

// DocTemplateChapter 章节定义（章节骨架的一个节点）
type DocTemplateChapter struct {
	Title         string `json:"title"`
	Instruction   string `json:"instruction,omitempty"`
	MaxWords      int    `json:"max_words,omitempty"`
	MustReference bool   `json:"must_reference"`
	Skippable     bool   `json:"skippable,omitempty"`
}

// DocTemplate 文档模板（个人私有：按 tenant_id + owner_id 隔离）
type DocTemplate struct {
	ID           string                  `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID     uint64                  `json:"tenant_id" gorm:"index"`
	OwnerID      string                  `json:"owner_id" gorm:"index"`
	Name         string                  `json:"name" gorm:"type:varchar(200);not null"`
	Description  string                  `json:"description" gorm:"type:text"`
	GlobalConfig JSON                    `json:"global_config" gorm:"type:jsonb"`
	Chapters     JSON                    `json:"chapters" gorm:"type:jsonb"`
	CreatedAt    time.Time               `json:"created_at"`
	UpdatedAt    time.Time               `json:"updated_at"`
	DeletedAt    gorm.DeletedAt          `json:"-" gorm:"index"`
}

// DecodeGlobalConfig 解码模板全局配置
func (t *DocTemplate) DecodeGlobalConfig() (DocTemplateGlobalConfig, error) {
	var cfg DocTemplateGlobalConfig
	if len(t.GlobalConfig) == 0 {
		return cfg, nil
	}
	if err := json.Unmarshal(t.GlobalConfig, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// DocChapterProgress 单章进度（progress JSON 的数组元素）
type DocChapterProgress struct {
	No     int    `json:"no"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// DocTaskProgress 任务进度（progress JSON）
type DocTaskProgress struct {
	Chapters []DocChapterProgress `json:"chapters"`
}

// DocGenerationTask 文档生成任务
type DocGenerationTask struct {
	ID               string     `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID         uint64     `json:"tenant_id" gorm:"index"`
	OwnerID          string     `json:"owner_id" gorm:"index"`
	TemplateID       string     `json:"template_id" gorm:"index"`
	TemplateSnapshot JSON       `json:"template_snapshot" gorm:"type:jsonb"`
	KbIDs            JSON       `json:"kb_ids" gorm:"type:jsonb"`
	KbFilters        JSON       `json:"kb_filters" gorm:"type:jsonb"`
	ModelID          string     `json:"model_id" gorm:"type:varchar(100)"`
	Theme            string     `json:"theme" gorm:"type:text;not null"`
	ExtraRequirement string     `json:"extra_requirement" gorm:"type:text"`
	Status           string     `json:"status" gorm:"type:varchar(32);default:'pending'"`
	Progress         JSON       `json:"progress" gorm:"type:jsonb"`
	Error            string     `json:"error" gorm:"type:text"`
	CreatedAt        time.Time  `json:"created_at"`
	FinishedAt       *time.Time `json:"finished_at"`
}

// DocReference 章节参考资料（按来源知识归并）
type DocReference struct {
	KnowledgeID string   `json:"knowledge_id"`
	Title       string   `json:"title"`
	Filename    string   `json:"filename,omitempty"`
	ChunkIDs    []string `json:"chunk_ids,omitempty"`
}

// DocChapter 文档中的章节内容（documents.chapters JSON 数组元素）
type DocChapter struct {
	No         int            `json:"no"`
	Title      string         `json:"title"`
	Content    string         `json:"content"`
	References []DocReference `json:"references,omitempty"`
	Status     string         `json:"status"`
	Message    string         `json:"message,omitempty"`
}

// DocDocument 生成的文档（version 版本化）
type DocDocument struct {
	ID        string     `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID  uint64     `json:"tenant_id" gorm:"index"`
	OwnerID   string     `json:"owner_id" gorm:"index"`
	TaskID    string     `json:"task_id" gorm:"index"`
	Version   int        `json:"version"`
	Status    string     `json:"status" gorm:"type:varchar(32);default:'generated'"`
	Chapters  JSON       `json:"chapters" gorm:"type:jsonb"`
	MdPath    string     `json:"md_path" gorm:"type:text"`
	CreatedAt time.Time  `json:"created_at"`
}

// DocGenerationPayload 生成任务异步 payload（worker 无请求上下文，需携带归属信息）
type DocGenerationPayload struct {
	TaskID   string `json:"task_id"`
	TenantID uint64 `json:"tenant_id"`
	OwnerID  string `json:"owner_id"`
}

// DocDocumentSummary 全局文档列表的一行（P2-5）。
//
// 标题取自 doc_generation_tasks.theme，不返回 chapters 正文；
// archive_status / archived_version 按 source_type='doc' 批量填充，
// 不读 doc_generation_tasks.archive_status（那是旧自动归档任务的字段）。
//
// doc_documents 没有 updated_at：编辑章节会新增一行版本，因此
// 「最新版本的 created_at」就是这份文档的更新时间。
type DocDocumentSummary struct {
	ID              string    `json:"id"`
	TaskID          string    `json:"task_id"`
	Title           string    `json:"title"`
	Version         int       `json:"version"`
	Status          string    `json:"status"`
	ArchiveStatus   string    `json:"archive_status,omitempty" gorm:"-"`
	ArchivedVersion int       `json:"archived_version,omitempty" gorm:"-"`
	CreatedAt       time.Time `json:"created_at"`
}

// DocDocumentFilter 全局文档列表的查询条件（owner 由服务端从 ctx 注入）。
type DocDocumentFilter struct {
	Query      string
	LatestOnly bool
	Cursor     string
	Limit      int
}

// DocDocumentPage cursor 分页响应（cursor 为 created_at|id 的 base64）。
type DocDocumentPage struct {
	Documents  []*DocDocumentSummary `json:"documents"`
	NextCursor string                `json:"next_cursor"`
}

// DocTemplateChapterEnvelope 章节骨架解码辅助（供 JSON 反序列化）
func (t *DocTemplate) DecodeChapters() ([]DocTemplateChapter, error) {
	var chapters []DocTemplateChapter
	if len(t.Chapters) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(t.Chapters, &chapters); err != nil {
		return nil, err
	}
	return chapters, nil
}
