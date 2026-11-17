package types

import (
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

// 洞察模块常量。状态机：pending → stage1 → waiting_confirm → stage2 → done，
// 任一阶段都可以进入 failed / cancelled。
const (
	InsightStatusPending        = "pending"
	InsightStatusStage1         = "stage1"
	InsightStatusWaitingConfirm = "waiting_confirm"
	InsightStatusStage2         = "stage2"
	InsightStatusDone           = "done"
	InsightStatusFailed         = "failed"
	InsightStatusCancelled      = "cancelled"

	InsightStageCheckup   = "checkup"
	InsightStageEvidence  = "evidence"
	InsightStageSynthesis = "synthesis"
	InsightStageDone      = "done"

	InsightSelfStatusIdle       = "idle"
	InsightSelfStatusGenerating = "generating"
	InsightSelfStatusReady      = "ready"
	InsightSelfStatusFailed     = "failed"
)

// 洞察执行口径。数值与接口文档一致，避免服务层各处写魔法数字。
const (
	InsightGateRequired    = 6   // 自我洞悉门槛：6 个内置主题各有一份报告
	InsightSampleDocLimit  = 20  // 小样模式最多分析 20 篇
	InsightMaxDocLimit     = 300 // 单次全量分析的文档上限
	InsightMaxEvidence     = 120 // 生成报告时最多使用的证据条数
	InsightDocTextLimit    = 6000
	InsightQuoteRuneLimit  = 120
	InsightDimensionMaxLen = 64
)

// InsightDimension 是主题的分析维度定义。
type InsightDimension struct {
	Name     string `json:"name"`
	Criteria string `json:"criteria"`
	Scale    string `json:"scale"`
}

// InsightTheme 是洞察主题实体。内置主题由首次列表请求幂等种入。
type InsightTheme struct {
	ID           string         `json:"id"           gorm:"type:varchar(36);primaryKey"`
	TenantID     uint64         `json:"-"            gorm:"column:tenant_id"`
	OwnerID      string         `json:"-"            gorm:"column:owner_id"`
	Name         string         `json:"name"         gorm:"type:varchar(80)"`
	Description  string         `json:"description"  gorm:"type:text"`
	Builtin      bool           `json:"builtin"      gorm:"column:builtin"`
	Sensitive    bool           `json:"sensitive"`
	Glyph        string         `json:"glyph"        gorm:"type:varchar(32)"`
	Instructions string         `json:"instructions" gorm:"type:text"`
	Dimensions   JSON           `json:"dimensions"   gorm:"column:dimensions;type:json"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `json:"-"            gorm:"index"`
}

// TableName 固定表名，避免 GORM 复数化规则漂移。
func (InsightTheme) TableName() string { return "insight_themes" }

// DimensionList 解析维度数组；脏数据返回空数组而不是报错，避免整页不可用。
func (t *InsightTheme) DimensionList() []InsightDimension {
	if t == nil || len(t.Dimensions) == 0 {
		return []InsightDimension{}
	}
	var out []InsightDimension
	if err := json.Unmarshal(t.Dimensions, &out); err != nil {
		return []InsightDimension{}
	}
	return out
}

// SetDimensions 把维度数组写回 JSON 列。
func (t *InsightTheme) SetDimensions(dims []InsightDimension) error {
	raw, err := json.Marshal(dims)
	if err != nil {
		return err
	}
	t.Dimensions = JSON(raw)
	return nil
}

// InsightRun 是一次主题分析运行。
type InsightRun struct {
	ID            string     `json:"id"              gorm:"type:varchar(36);primaryKey"`
	TenantID      uint64     `json:"-"               gorm:"column:tenant_id"`
	OwnerID       string     `json:"-"               gorm:"column:owner_id"`
	ThemeID       string     `json:"theme_id"        gorm:"column:theme_id"`
	Status        string     `json:"status"          gorm:"type:varchar(24)"`
	Stage         string     `json:"stage"           gorm:"type:varchar(24)"`
	Progress      int        `json:"progress"`
	DocsTotal     int        `json:"docs_total"      gorm:"column:docs_total"`
	DocsProcessed int        `json:"docs_processed"  gorm:"column:docs_processed"`
	EvidenceCount int        `json:"evidence_count"  gorm:"column:evidence_count"`
	OptionsJSON   JSON       `json:"options_json"    gorm:"column:options_json;type:json"`
	Error         string     `json:"error"           gorm:"type:text"`
	Message       string     `json:"message"         gorm:"type:text"`
	StartedAt     *time.Time `json:"started_at"      gorm:"column:started_at"`
	FinishedAt    *time.Time `json:"finished_at"     gorm:"column:finished_at"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// TableName 固定表名。
func (InsightRun) TableName() string { return "insight_runs" }

// InsightEvidence 是从文档中抽取出的单条证据。
type InsightEvidence struct {
	ID             string    `json:"id"              gorm:"type:varchar(36);primaryKey"`
	TenantID       uint64    `json:"-"               gorm:"column:tenant_id"`
	OwnerID        string    `json:"-"               gorm:"column:owner_id"`
	RunID          string    `json:"run_id"          gorm:"column:run_id"`
	ThemeID        string    `json:"theme_id"        gorm:"column:theme_id"`
	KnowledgeID    string    `json:"knowledge_id"    gorm:"column:knowledge_id"`
	KnowledgeTitle string    `json:"knowledge_title" gorm:"column:knowledge_title"`
	ChunkIndex     int       `json:"chunk_index"     gorm:"column:chunk_index"`
	Dimension      string    `json:"dimension"       gorm:"type:varchar(64)"`
	Quote          string    `json:"quote"           gorm:"type:text"`
	EvidenceType   string    `json:"evidence_type"   gorm:"column:evidence_type;type:varchar(16)"`
	Confidence     string    `json:"confidence"      gorm:"type:varchar(8)"`
	Excluded       bool      `json:"excluded"`
	CreatedAt      time.Time `json:"created_at"`
}

// TableName 固定表名。
func (InsightEvidence) TableName() string { return "insight_evidence" }

// InsightReport 是一份主题洞察报告。
type InsightReport struct {
	ID            string    `json:"id"             gorm:"type:varchar(36);primaryKey"`
	TenantID      uint64    `json:"-"              gorm:"column:tenant_id"`
	OwnerID       string    `json:"-"              gorm:"column:owner_id"`
	ThemeID       string    `json:"theme_id"       gorm:"column:theme_id"`
	RunID         string    `json:"run_id"         gorm:"column:run_id"`
	Title         string    `json:"title"          gorm:"type:varchar(255)"`
	ModelID       string    `json:"model_id"       gorm:"column:model_id;type:varchar(64)"`
	DocsCovered   int       `json:"docs_covered"   gorm:"column:docs_covered"`
	EvidenceCount int       `json:"evidence_count" gorm:"column:evidence_count"`
	ContentJSON   JSON      `json:"-"              gorm:"column:content_json;type:json"`
	CreatedAt     time.Time `json:"created_at"`
}

// TableName 固定表名。
func (InsightReport) TableName() string { return "insight_reports" }

// InsightSelf 是用户的自我洞悉快照，同一用户唯一。
type InsightSelf struct {
	ID          string    `json:"id"           gorm:"type:varchar(36);primaryKey"`
	TenantID    uint64    `json:"-"            gorm:"column:tenant_id"`
	OwnerID     string    `json:"-"            gorm:"column:owner_id"`
	Status      string    `json:"status"       gorm:"type:varchar(24)"`
	ContentJSON JSON      `json:"-"            gorm:"column:content_json;type:json"`
	Error       string    `json:"error"        gorm:"type:text"`
	ModelID     string    `json:"model_id"     gorm:"column:model_id;type:varchar(64)"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// TableName 固定表名。
func (InsightSelf) TableName() string { return "insight_self" }

// InsightThemeView 是主题列表/详情的对外结构，附带派生的报告计数与运行状态。
type InsightThemeView struct {
	InsightTheme
	ReportCount int64      `json:"report_count"`
	LastRunAt   *time.Time `json:"last_run_at"`
	Running     bool       `json:"running"`
	// UpdatedDocs 是工作库中在上次报告之后有更新的文档数；无报告时为 0。
	// 只做提醒用途，不代表这些文档一定没被分析（范围筛选可能已排除它们）。
	UpdatedDocs int `json:"updated_docs"`
}

// InsightThemeStats 是主题页顶部的汇总。
type InsightThemeStats struct {
	ReportCount int64 `json:"report_count"`
	DocCount    int   `json:"doc_count"`
}

// InsightThemeListResponse 是 GET /themes 的响应体。
type InsightThemeListResponse struct {
	Themes []*InsightThemeView `json:"themes"`
	Stats  InsightThemeStats   `json:"stats"`
}

// CreateInsightThemeRequest 同时用于新建与整表更新主题。
type CreateInsightThemeRequest struct {
	Name         string             `json:"name"`
	Description  string             `json:"description"`
	Sensitive    bool               `json:"sensitive"`
	Dimensions   []InsightDimension `json:"dimensions"`
	Instructions string             `json:"instructions"`
}

// InsightCheckup 是素材体检结果。优先归类：空 → 附件 → 重复 → 过短 → 可分析。
type InsightCheckup struct {
	Archived   int `json:"archived"`
	Analyzable int `json:"analyzable"`
	TooShort   int `json:"too_short"`
	Empty      int `json:"empty"`
	Attachment int `json:"attachment"`
	Duplicate  int `json:"duplicate"`
	SampleSize int `json:"sample_size"`
	// Folders 是全部归档文档出现过的文件夹路径，供范围选择器使用。
	Folders []string `json:"folders"`
}

// InsightScopeFilter 是一次分析的范围筛选；零值表示全部归档。
type InsightScopeFilter struct {
	FolderPath string `json:"folder_path"`
	RecentDays int    `json:"recent_days"`
}

// StartInsightRunRequest 是启动阶段一的请求。
type StartInsightRunRequest struct {
	Sample     bool   `json:"sample"`
	MaxDocs    int    `json:"max_docs"`
	FolderPath string `json:"folder_path"`
	RecentDays int    `json:"recent_days"`
}

// ScopeFilter 把请求里的范围字段收成一个筛选条件。
func (r StartInsightRunRequest) ScopeFilter() InsightScopeFilter {
	return InsightScopeFilter{FolderPath: r.FolderPath, RecentDays: r.RecentDays}
}

// InsightEvidenceList 是证据列表响应。
type InsightEvidenceList struct {
	Items    []*InsightEvidence `json:"items"`
	Total    int                `json:"total"`
	Excluded int                `json:"excluded"`
}

// ExcludeInsightEvidenceRequest 是批量排除/恢复证据的请求。
type ExcludeInsightEvidenceRequest struct {
	IDs      []string `json:"ids"`
	Excluded bool     `json:"excluded"`
}

// InsightReportSummary 是报告列表项，不含正文。
type InsightReportSummary struct {
	ID            string    `json:"id"`
	ThemeID       string    `json:"theme_id"`
	RunID         string    `json:"run_id"`
	Title         string    `json:"title"`
	CreatedAt     time.Time `json:"created_at"`
	DocsCovered   int       `json:"docs_covered"`
	EvidenceCount int       `json:"evidence_count"`
	ModelID       string    `json:"model_id"`
}

// InsightReportDetail 是报告详情，content 为模型产出的结构化正文。
type InsightReportDetail struct {
	InsightReportSummary
	ThemeName string `json:"theme_name"`
	Content   JSON   `json:"content"`
}

// InsightReportMessage 是报告内追问的一条会话消息。
//
// 会话上下文严格限定为「这份报告 + 它的证据集」，不重新检索全库；
// 归属与报告一致（tenant + owner），管理员不可读。
type InsightReportMessage struct {
	ID        string    `json:"id"         gorm:"type:varchar(36);primaryKey"`
	TenantID  uint64    `json:"-"          gorm:"column:tenant_id"`
	OwnerID   string    `json:"-"          gorm:"column:owner_id"`
	ReportID  string    `json:"report_id"  gorm:"column:report_id;type:varchar(36)"`
	Role      string    `json:"role"       gorm:"type:varchar(16)"`
	Content   string    `json:"content"    gorm:"type:text"`
	RefsJSON  JSON      `json:"refs"       gorm:"column:refs_json;type:json"`
	CreatedAt time.Time `json:"created_at"`
}

// TableName 固定表名。
func (InsightReportMessage) TableName() string { return "insight_report_messages" }

// 报告追问的消息角色。
const (
	InsightMessageRoleUser      = "user"
	InsightMessageRoleAssistant = "assistant"

	InsightAskQuestionMaxRunes = 2000
	InsightAskHistoryLimit     = 12
	InsightAskEvidenceLimit    = 60
	// InsightSelfMessageListLimit 是自我洞悉会话一次最多返回的消息数。
	InsightSelfMessageListLimit = 50
)

// AskInsightReportRequest 是报告追问的请求体。
type AskInsightReportRequest struct {
	Question string `json:"question"`
}

// AskInsightReportResponse 是报告追问的响应体。
type AskInsightReportResponse struct {
	Answer string `json:"answer"`
}

// InsightReportMessageList 是会话消息列表响应。
type InsightReportMessageList struct {
	Items []*InsightReportMessage `json:"items"`
}

// InsightSelfMessage 是自我洞悉说明书内追问的一条会话消息。
//
// 自我洞悉是“一人一份、覆盖更新”的快照，所以归属直接用 tenant + owner，
// 不挂 self_id；重新生成时旧会话由服务层一并清空，避免追问残留旧结论。
type InsightSelfMessage struct {
	ID        string    `json:"id"         gorm:"type:varchar(36);primaryKey"`
	TenantID  uint64    `json:"-"          gorm:"column:tenant_id"`
	OwnerID   string    `json:"-"          gorm:"column:owner_id"`
	Role      string    `json:"role"       gorm:"type:varchar(16)"`
	Content   string    `json:"content"    gorm:"type:text"`
	CreatedAt time.Time `json:"created_at"`
}

// TableName 固定表名。
func (InsightSelfMessage) TableName() string { return "insight_self_messages" }

// AskInsightSelfRequest 是自我洞悉追问的请求体。
type AskInsightSelfRequest struct {
	Question string `json:"question"`
}

// InsightSelfMessageList 是自我洞悉会话消息列表响应。
type InsightSelfMessageList struct {
	Items []*InsightSelfMessage `json:"items"`
}

// InsightSelfGate 是自我洞悉的生成门槛。
type InsightSelfGate struct {
	Required  int      `json:"required"`
	Completed int      `json:"completed"`
	Missing   []string `json:"missing"`
}

// InsightSelfView 是 GET /self 的响应体。
type InsightSelfView struct {
	Gate      InsightSelfGate `json:"gate"`
	Status    string          `json:"status"`
	UpdatedAt *time.Time      `json:"updated_at"`
	Error     string          `json:"error"`
	ModelID   string          `json:"model_id"`
	Content   JSON            `json:"content"`
}

// InsightPrivacy 是洞察导出层的脱敏设置。
//
// 只影响复制 / 导出 / 存为文档；页面展示与报告追问仍使用原文，
// 否则用户会认不出里面的自己和同事。
type InsightPrivacy struct {
	Persons            []string `json:"persons"`
	Organizations      []string `json:"organizations"`
	Projects           []string `json:"projects"`
	HideEvidenceQuotes bool     `json:"hide_evidence_quotes"`
}

const (
	// InsightPrivacyMaxTerms 是每一类敏感词的最大条数。
	InsightPrivacyMaxTerms = 200
	// InsightPrivacyMaxTermRunes 是单个敏感词的最大长度。
	InsightPrivacyMaxTermRunes = 64
)
