package types

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ---- 常量：需求状态（数据库存英文，UI 中文映射在前端）----
const (
	RequirementStatusPendingAnalysis = "pending_analysis"
	RequirementStatusConfirmed       = "confirmed"
	RequirementStatusScheduled       = "scheduled"
	RequirementStatusDeveloping      = "developing"
	RequirementStatusLaunched        = "launched"
	RequirementStatusRejected        = "rejected"
	RequirementStatusClosed          = "closed"
)

// ---- 常量：版本状态 ----
const (
	VersionStatusPlanning   = "planning"
	VersionStatusReviewing  = "reviewing"
	VersionStatusFinalized  = "finalized"
	VersionStatusDeveloping = "developing"
	VersionStatusLaunched   = "launched"
	VersionStatusShelved    = "shelved"
)

// ---- 常量：需求优先级 ----
const (
	RequirementPriorityP0 = "P0"
	RequirementPriorityP1 = "P1"
	RequirementPriorityP2 = "P2"
	RequirementPriorityP3 = "P3"
)

// ---- 常量：需求来源 ----
const (
	RequirementSourceCustomer   = "customer"
	RequirementSourceInternal   = "internal"
	RequirementSourceData       = "data"
	RequirementSourceCompetitor = "competitor"
	RequirementSourceStrategy   = "strategy"
	RequirementSourceOther      = "other"
)

// ---- 常量：需求价值达成（空串表示未回填）----
const (
	RequirementValueAchieved    = "achieved"
	RequirementValuePartial     = "partial"
	RequirementValueNotAchieved = "not_achieved"
)

// ---- 常量：需求附件 ----
const (
	// PMRequirementAttachmentMaxSize 单个附件大小上限（10MB）
	PMRequirementAttachmentMaxSize = 10 << 20
	// PMRequirementAttachmentTextMaxLen 注入 AI 上下文时单个附件文本的最大字符数
	PMRequirementAttachmentTextMaxLen = 4000
)

// LaunchVerificationNoteMaxLen 上线验证中单个说明字段的长度上限
const LaunchVerificationNoteMaxLen = 2000

// LaunchVerificationInput 上线验证回填入参：效果数据 / 价值达成 / 复盘说明
type LaunchVerificationInput struct {
	ValueAchieved string `json:"value_achieved" binding:"required"`
	EffectNotes   string `json:"effect_notes"`
	RetroNotes    string `json:"retro_notes"`
}

// Normalize 去除首尾空白，避免纯空格被当作有效内容
func (in *LaunchVerificationInput) Normalize() {
	in.ValueAchieved = strings.TrimSpace(in.ValueAchieved)
	in.EffectNotes = strings.TrimSpace(in.EffectNotes)
	in.RetroNotes = strings.TrimSpace(in.RetroNotes)
}

// ---- 常量：版本评审结论（空串表示未评审）----
const (
	VersionConclusionPass        = "pass"
	VersionConclusionConditional = "conditional"
	VersionConclusionReject      = "reject"
)

// ---- 常量：功能清单 ----
const (
	FeatureSourceRoadmap = "roadmap"
	FeatureSourcePRD     = "prd"
	FeatureSourceManual  = "manual"

	FeatureStatusPlanned  = "planned"
	FeatureStatusLaunched = "launched"

	FeatureChangeAdd    = "add"
	FeatureChangeChange = "change"
	FeatureChangeRemove = "remove"
)

// ---- 常量：项目资料 ----
const (
	MaterialTypeManual   = "manual"
	MaterialTypeBrochure = "brochure"
	MaterialTypePPT      = "ppt"

	MaterialStatusDraft     = "draft"
	MaterialStatusPublished = "published"
)

// ---- 常量：项目状态 ----
const (
	PMProjectStatusActive   = "active"
	PMProjectStatusArchived = "archived"
)

// ---- 常量：规划批次 ----
const (
	PlanBatchTypeInitial   = "initial"
	PlanBatchTypeIteration = "iteration"
	PlanBatchTypeReplan    = "replan" // 重新规划：以草稿为准覆盖当前内容

	PlanBatchStatusDraft    = "draft"
	PlanBatchStatusActive   = "active"
	PlanBatchStatusArchived = "archived"
)

// ---- 常量：规划类型（合并语义用的同义别名）----
const (
	PlanTypeInitial   = PlanBatchTypeInitial
	PlanTypeIteration = PlanBatchTypeIteration
	PlanTypeReplan    = PlanBatchTypeReplan
)

// ---- 常量：异步任务 ----
const (
	PMTaskStatusPending   = "pending"
	PMTaskStatusRunning   = "running"
	PMTaskStatusSucceeded = "succeeded"
	PMTaskStatusFailed    = "failed"

	// asynq 任务类型，与 task.go 的 <domain>:<action> 命名保持一致
	TypePMPRDGenerate       = "pm:prd-generate"
	TypePMPrototypeGenerate = "pm:prototype-generate"
	TypePMMaterialGenerate  = "pm:material-generate"
	TypePMRoadmapGenerate   = "pm:roadmap-generate"
)

// ---- PM 知识库产物目录 ----
// 知识库不支持多级目录时，目录语义降级为知识点标题前缀（见 PMKBDirService.Resolve）。
type PMDir string

const (
	PMDirRequirement PMDir = "requirement"  // 需求分析产物
	PMDirPlanHistory PMDir = "plan_history" // 功能规划历史
	PMDirPRD         PMDir = "prd"          // 版本 PRD
	PMDirPrototype   PMDir = "prototype"    // 版本原型
	PMDirMaterial    PMDir = "material"     // 项目资料
)

// PMDirs 项目创建时需要初始化的逻辑目录全集
var PMDirs = []PMDir{PMDirRequirement, PMDirPlanHistory, PMDirPRD, PMDirPrototype, PMDirMaterial}

// ---- 枚举白名单 ----

var validRequirementPriorities = map[string]bool{
	RequirementPriorityP0: true,
	RequirementPriorityP1: true,
	RequirementPriorityP2: true,
	RequirementPriorityP3: true,
}

var validRequirementSources = map[string]bool{
	RequirementSourceCustomer:   true,
	RequirementSourceInternal:   true,
	RequirementSourceData:       true,
	RequirementSourceCompetitor: true,
	RequirementSourceStrategy:   true,
	RequirementSourceOther:      true,
}

var validVersionConclusions = map[string]bool{
	VersionConclusionPass:        true,
	VersionConclusionConditional: true,
	VersionConclusionReject:      true,
}

// IsValidRequirementPriority 校验需求优先级（handler/service 共用，拦截脏数据入库）
func IsValidRequirementPriority(v string) bool { return validRequirementPriorities[v] }

// IsValidRequirementSource 校验需求来源
func IsValidRequirementSource(v string) bool { return validRequirementSources[v] }

// IsValidVersionConclusion 校验版本评审结论
func IsValidVersionConclusion(v string) bool { return validVersionConclusions[v] }

// PMAnalysisSessionMarker 标记 PM 工作站的需求分析会话：它会话描述带此前缀，
// 用于在首页对话列表中隐藏（仿 skill_maintenance / meeting_artifact 的隐藏约定）。
const PMAnalysisSessionMarker = "pm_analysis:"

// IsPMAnalysisSessionDescription 判断会话是否属于 PM 工作站分析会话
func IsPMAnalysisSessionDescription(description string) bool {
	return strings.HasPrefix(description, PMAnalysisSessionMarker)
}

// PMRoadmapSessionMarker 标记 PM 工作站的功能规划引导会话：同样在首页对话列表中隐藏
const PMRoadmapSessionMarker = "pm_roadmap:"

// IsPMRoadmapSessionDescription 判断会话是否属于 PM 工作站规划引导会话
func IsPMRoadmapSessionDescription(description string) bool {
	return strings.HasPrefix(description, PMRoadmapSessionMarker)
}

// ---- 模型：一张表一个 struct ----

// PMProject 项目
type PMProject struct {
	ID          string         `gorm:"type:varchar(36);primaryKey" json:"id"`
	TenantID    uint64         `gorm:"index" json:"-"`
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

func (PMProject) TableName() string { return "pm_projects" }

func (p *PMProject) BeforeCreate(_ *gorm.DB) error {
	if p.ID == "" {
		p.ID = uuid.New().String()
	}
	return nil
}

// jsonOrEmpty 空 JSON 落库前补默认值：驱动会把空 JSON 写成 NULL，
// 而 PM 表的相关列均为 NOT NULL，需在插入前归一化。
func jsonOrEmpty(j *JSON, def string) {
	if len(*j) == 0 {
		*j = JSON(def)
	}
}

// PMRequirement 需求
type PMRequirement struct {
	ID               string         `gorm:"type:varchar(36);primaryKey" json:"id"`
	TenantID         uint64         `gorm:"index" json:"-"`
	ProjectID        string         `gorm:"type:varchar(36);index" json:"project_id"`
	Title            string         `gorm:"type:varchar(255)" json:"title"`
	Description      string         `gorm:"type:text" json:"description"`
	Priority         string         `gorm:"type:varchar(8);default:P1" json:"priority"`
	Status           string         `gorm:"type:varchar(32);default:pending_analysis" json:"status"`
	Source           string         `gorm:"type:varchar(32);default:other" json:"source"`
	Proposer         string         `gorm:"type:varchar(128)" json:"proposer"`
	AIAnalysis       JSON           `gorm:"type:jsonb" json:"ai_analysis"`
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

func (PMRequirement) TableName() string { return "pm_requirements" }

func (r *PMRequirement) BeforeCreate(_ *gorm.DB) error {
	if r.ID == "" {
		r.ID = uuid.New().String()
	}
	jsonOrEmpty(&r.AIAnalysis, "{}")
	return nil
}

// PMRequirementAttachment 需求附件：文件字节直存数据库，列表响应不返回正文与文本
type PMRequirementAttachment struct {
	ID            string `gorm:"type:varchar(36);primaryKey" json:"id"`
	TenantID      uint64 `gorm:"index" json:"-"`
	RequirementID string `gorm:"type:varchar(36);index" json:"requirement_id"`
	FileName      string `gorm:"type:varchar(255)" json:"file_name"`
	ContentType   string `gorm:"type:varchar(128)" json:"content_type"`
	Size          int64  `json:"size"`
	// TextContent 纯文本类附件抽取的正文，仅用于拼接 AI 分析上下文
	TextContent string         `gorm:"type:text" json:"-"`
	Data        []byte         `gorm:"type:bytea" json:"-"`
	CreatedBy   string         `gorm:"type:varchar(36)" json:"created_by"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

func (PMRequirementAttachment) TableName() string { return "pm_requirement_attachments" }

func (a *PMRequirementAttachment) BeforeCreate(_ *gorm.DB) error {
	if a.ID == "" {
		a.ID = uuid.New().String()
	}
	return nil
}

// PMVersion 版本
type PMVersion struct {
	ID               string     `gorm:"type:varchar(36);primaryKey" json:"id"`
	TenantID         uint64     `gorm:"index" json:"-"`
	ProjectID        string     `gorm:"type:varchar(36);index" json:"project_id"`
	VersionNo        string     `gorm:"type:varchar(32)" json:"version_no"`
	Name             string     `gorm:"type:varchar(255)" json:"name"`
	Status           string     `gorm:"type:varchar(16);default:planning" json:"status"`
	ReviewRound      int        `gorm:"default:0" json:"review_round"`
	ReviewOpinions   JSON       `gorm:"type:jsonb" json:"review_opinions"`
	ReviewConclusion string     `gorm:"type:varchar(16)" json:"review_conclusion"`
	Reviewers        string     `gorm:"type:varchar(512)" json:"reviewers"`
	ReleaseNote      string     `gorm:"type:text" json:"release_note"`
	PlanDate         *time.Time `gorm:"type:date" json:"plan_date"`
	PlanLaunchDate   *time.Time `gorm:"type:date" json:"plan_launch_date"`
	FinalizeDate     *time.Time `json:"finalize_date"`
	LaunchDate       *time.Time `json:"launch_date"`
	// ===== 版本产物（PRD / 原型 / 功能清单）=====
	PRDKnowledgeID         string     `gorm:"type:varchar(36)" json:"prd_knowledge_id"`
	PrototypeKnowledgeID   string     `gorm:"type:varchar(36)" json:"prototype_knowledge_id"`
	ArtifactRound          int        `gorm:"default:0" json:"artifact_round"`
	ArtifactFeatureCount   int        `gorm:"default:0" json:"artifact_feature_count"`
	ArtifactRequirementIDs JSON       `gorm:"type:jsonb" json:"-"`
	UpdateOutline          string     `gorm:"type:text" json:"update_outline"`
	GeneratedAt            *time.Time `json:"generated_at"`
	// 非持久化字段：列表需求数、需求集合是否在生成后发生变动
	RequirementCount    int            `gorm:"-" json:"requirement_count"`
	RequirementsChanged bool           `gorm:"-" json:"requirements_changed"`
	OwnerID             string         `gorm:"type:varchar(36)" json:"owner_id"`
	CreatedAt           time.Time      `json:"created_at"`
	UpdatedAt           time.Time      `json:"updated_at"`
	DeletedAt           gorm.DeletedAt `gorm:"index" json:"-"`
}

func (PMVersion) TableName() string { return "pm_versions" }

func (v *PMVersion) BeforeCreate(_ *gorm.DB) error {
	if v.ID == "" {
		v.ID = uuid.New().String()
	}
	jsonOrEmpty(&v.ReviewOpinions, "[]")
	jsonOrEmpty(&v.ArtifactRequirementIDs, "[]")
	return nil
}

// PMVersionReviewRecord 单轮评审记录，按轮次累积写入 pm_versions.review_opinions
type PMVersionReviewRecord struct {
	Round      int       `json:"round"`
	Conclusion string    `json:"conclusion"`
	Opinions   JSON      `json:"opinions"`
	Reviewers  string    `json:"reviewers,omitempty"`
	ReviewedAt time.Time `json:"reviewed_at"`
}

// 评审意见处理状态：待处理 / 已解决
const (
	ReviewOpinionPending  = "pending"
	ReviewOpinionResolved = "resolved"
)

// PMReviewOpinionItem 单条评审意见，支持记录处理说明与状态
type PMReviewOpinionItem struct {
	Text   string `json:"text"`
	Status string `json:"status"`
	Note   string `json:"note"`
}

// PMReviewOpinionsBody 一轮评审的意见载体（存入 PMVersionReviewRecord.Opinions）
type PMReviewOpinionsBody struct {
	Items      []PMReviewOpinionItem `json:"items"`
	ChangeNote string                `json:"change_note"`
}

// PMVersionRequirement 版本与需求的多对多关联（硬删，无软删列）
type PMVersionRequirement struct {
	ID            string    `gorm:"type:varchar(36);primaryKey" json:"id"`
	TenantID      uint64    `gorm:"index" json:"-"`
	VersionID     string    `gorm:"type:varchar(36);index" json:"version_id"`
	RequirementID string    `gorm:"type:varchar(36);index" json:"requirement_id"`
	CreatedAt     time.Time `json:"created_at"`
}

func (PMVersionRequirement) TableName() string { return "pm_version_requirements" }

func (vr *PMVersionRequirement) BeforeCreate(_ *gorm.DB) error {
	if vr.ID == "" {
		vr.ID = uuid.New().String()
	}
	return nil
}

// PMFeature 功能清单三级节点（模块 / 功能 / 操作），parent_id 为空表示模块级。
// Subsystem 仅随规划草稿 JSON 流转（对应 feature-list 技能的「子系统」列），不落 pm_features 表。
type PMFeature struct {
	ID            string         `gorm:"type:varchar(36);primaryKey" json:"id"`
	TenantID      uint64         `gorm:"index" json:"-"`
	ProjectID     string         `gorm:"type:varchar(36);index" json:"project_id"`
	RequirementID string         `gorm:"type:varchar(36);index" json:"requirement_id"`
	ParentID      string         `gorm:"type:varchar(36);index" json:"parent_id"`
	Subsystem     string         `gorm:"-" json:"subsystem,omitempty"`
	ModuleName    string         `gorm:"type:varchar(255)" json:"module_name"`
	FeatureName   string         `gorm:"type:varchar(255)" json:"feature_name"`
	OperationName string         `gorm:"type:varchar(255)" json:"operation_name"`
	Source        string         `gorm:"type:varchar(16);default:manual" json:"source"`
	Status        string         `gorm:"type:varchar(16);default:planned" json:"status"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	DeletedAt     gorm.DeletedAt `gorm:"index" json:"-"`
}

func (PMFeature) TableName() string { return "pm_features" }

func (f *PMFeature) BeforeCreate(_ *gorm.DB) error {
	if f.ID == "" {
		f.ID = uuid.New().String()
	}
	return nil
}

// PMFeatureIteration 功能点迭代历史（变更流水，无软删）
type PMFeatureIteration struct {
	ID         string    `gorm:"type:varchar(36);primaryKey" json:"id"`
	TenantID   uint64    `gorm:"index" json:"-"`
	FeatureID  string    `gorm:"type:varchar(36);index" json:"feature_id"`
	VersionID  string    `gorm:"type:varchar(36);index" json:"version_id"`
	ChangeType string    `gorm:"type:varchar(16)" json:"change_type"`
	ChangeNote string    `gorm:"type:text" json:"change_note"`
	ChangedAt  time.Time `json:"changed_at"`
}

func (PMFeatureIteration) TableName() string { return "pm_feature_iterations" }

func (fi *PMFeatureIteration) BeforeCreate(_ *gorm.DB) error {
	if fi.ID == "" {
		fi.ID = uuid.New().String()
	}
	return nil
}

// CreatePMFeatureInput 人工新增功能点入参：version_id 非空时同时写一条 add 迭代
type CreatePMFeatureInput struct {
	Feature   *PMFeature `json:"feature"`
	VersionID string     `json:"version_id"`
}

// PMFeatureNode 功能清单三级树节点，携带最新迭代版本号与状态
type PMFeatureNode struct {
	*PMFeature
	Children            []*PMFeatureNode `json:"children,omitempty"`
	LatestVersionNo     string           `json:"latest_version_no,omitempty"`
	LatestVersionStatus string           `json:"latest_version_status,omitempty"`
}

// PMMaterial 项目资料
type PMMaterial struct {
	ID           string `gorm:"type:varchar(36);primaryKey" json:"id"`
	TenantID     uint64 `gorm:"index" json:"-"`
	ProjectID    string `gorm:"type:varchar(36);index" json:"project_id"`
	MaterialType string `gorm:"type:varchar(16)" json:"material_type"`
	VersionID    string `gorm:"type:varchar(36)" json:"version_id"`
	KnowledgeID  string `gorm:"type:varchar(36)" json:"knowledge_id"`
	Title        string `gorm:"type:varchar(255)" json:"title"`
	// Content 生成任务回写的 Markdown 正文，发布落库与导出 docx/pptx 的唯一内容源
	Content   string         `gorm:"type:text" json:"content"`
	Status    string         `gorm:"type:varchar(16);default:draft" json:"status"`
	Archived  bool           `gorm:"default:false" json:"archived"`
	CreatedBy string         `gorm:"type:varchar(36)" json:"created_by"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	// 以下为列表/详情派生字段，不落库：关联版本号、项目最新版本号与更新提示
	VersionNo       string `gorm:"-" json:"version_no,omitempty"`
	LatestVersionNo string `gorm:"-" json:"latest_version_no,omitempty"`
	NeedsUpdate     bool   `gorm:"-" json:"needs_update"`
}

func (PMMaterial) TableName() string { return "pm_materials" }

// PMMaterialFile 资料导出文件载体（不落库）：下载端点直接写响应
type PMMaterialFile struct {
	FileName    string `json:"file_name"`
	ContentType string `json:"content_type"`
	Data        []byte `json:"-"`
}

func (m *PMMaterial) BeforeCreate(_ *gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.New().String()
	}
	return nil
}

// PMPlanBatch 规划批次（含 5 步引导草稿与步骤进度）
type PMPlanBatch struct {
	ID                  string         `gorm:"type:varchar(36);primaryKey" json:"id"`
	TenantID            uint64         `gorm:"index" json:"-"`
	ProjectID           string         `gorm:"type:varchar(36);index" json:"project_id"`
	BatchName           string         `gorm:"type:varchar(128)" json:"batch_name"`
	PlanType            string         `gorm:"type:varchar(16);default:initial" json:"plan_type"`
	VersionID           string         `gorm:"type:varchar(36)" json:"version_id"`
	ChangeSummary       string         `gorm:"type:text" json:"change_summary"`
	Status              string         `gorm:"type:varchar(16);default:draft" json:"status"`
	Draft               JSON           `gorm:"type:jsonb" json:"draft"`
	CurrentStep         int            `gorm:"type:smallint;default:1" json:"current_step"`
	RoadmapKnowledgeID  string         `gorm:"type:varchar(36)" json:"roadmap_knowledge_id"`
	SnapshotKnowledgeID string         `gorm:"type:varchar(36)" json:"snapshot_knowledge_id"`
	GeneratedAt         *time.Time     `json:"generated_at"`
	SnapshotAt          *time.Time     `json:"snapshot_at"`
	OperatorID          string         `gorm:"type:varchar(36)" json:"operator_id"`
	ActivatedAt         *time.Time     `json:"activated_at"`
	CreatedBy           string         `gorm:"type:varchar(36)" json:"created_by"`
	CreatedAt           time.Time      `json:"created_at"`
	UpdatedAt           time.Time      `json:"updated_at"`
	DeletedAt           gorm.DeletedAt `gorm:"index" json:"-"`
}

func (PMPlanBatch) TableName() string { return "pm_plan_batches" }

func (b *PMPlanBatch) BeforeCreate(_ *gorm.DB) error {
	if b.ID == "" {
		b.ID = uuid.New().String()
	}
	jsonOrEmpty(&b.Draft, "{}")
	return nil
}

// PMRoadmapCognition 引导第 1 步：项目认知
type PMRoadmapCognition struct {
	TargetUsers string `json:"target_users"`
	CoreValue   string `json:"core_value"`
	Scenarios   string `json:"scenarios"`
	Background  string `json:"background"`
}

// PMRoadmapFlow 引导第 3 步：模块业务流程图（仅支持 Mermaid flowchart 子集）
type PMRoadmapFlow struct {
	Module  string `json:"module"`
	Mermaid string `json:"mermaid"`
}

// PMRoadmapDraft 5 步引导草稿（pm_plan_batches.draft 的 JSON 结构）
type PMRoadmapDraft struct {
	Cognition *PMRoadmapCognition `json:"cognition,omitempty"`
	// Subsystem 本次规划的子系统（feature-list 第一列），由用户指定；行级为空时用它兜底
	Subsystem string          `json:"subsystem,omitempty"`
	Features  []PMFeature     `json:"features"`
	Flows     []PMRoadmapFlow `json:"flows,omitempty"`
	// Scope 首期范围，元素为「模块/功能点/操作项」三级路径
	Scope []string `json:"scope,omitempty"`
}

// PMTask 异步任务进度（轮询接口 GET /api/v1/pm/tasks/:task_id 的载体，无软删）
type PMTask struct {
	ID           string     `gorm:"type:varchar(36);primaryKey" json:"id"`
	TenantID     uint64     `gorm:"index" json:"-"`
	ProjectID    string     `gorm:"type:varchar(36)" json:"project_id"`
	TaskType     string     `gorm:"type:varchar(64)" json:"task_type"`
	Status       string     `gorm:"type:varchar(16);default:pending" json:"status"`
	Progress     int        `gorm:"type:smallint;default:0" json:"progress"`
	Payload      JSON       `gorm:"type:jsonb" json:"payload"`
	Result       JSON       `gorm:"type:jsonb" json:"result"`
	ErrorMessage string     `gorm:"type:text" json:"error_message"`
	CreatedBy    string     `gorm:"type:varchar(36)" json:"created_by"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	FinishedAt   *time.Time `json:"finished_at"`
}

func (PMTask) TableName() string { return "pm_tasks" }

func (t *PMTask) BeforeCreate(_ *gorm.DB) error {
	if t.ID == "" {
		t.ID = uuid.New().String()
	}
	jsonOrEmpty(&t.Payload, "{}")
	jsonOrEmpty(&t.Result, "{}")
	return nil
}

// PMPayload asynq 任务载荷：worker 无 HTTP 上下文，租户与业务标识必须随载荷传递
type PMPayload struct {
	TenantID uint64 `json:"tenant_id"`
	TaskID   string `json:"task_id"`
	// TargetID 生成目标：版本 ID（PRD/原型）或资料 ID（项目资料）
	TargetID string `json:"target_id,omitempty"`
	// Title 渲染标题（版本号/资料名），为空视为不可生成
	Title string `json:"title,omitempty"`
	// Sections 模板渲染的结构化小节
	Sections []PMPayloadSection `json:"sections,omitempty"`
	// ===== 版本产物（PRD/原型）生成扩展 =====
	// Regenerate 是否为重新生成：为 true 时提示词需携带上一轮评审意见
	Regenerate bool `json:"regenerate,omitempty"`
	// Chapters 本次要生成的 PRD 章节 key，空表示全部章节
	Chapters []string `json:"chapters,omitempty"`
}

// PMPayloadSection 生成草稿的一个小节
type PMPayloadSection struct {
	Heading string `json:"heading"`
	Body    string `json:"body"`
}

// ---- 状态机：以「允许迁移集合」表达，便于单测和前端提示复用 ----

var requirementTransitions = map[string]map[string]bool{
	RequirementStatusPendingAnalysis: {
		RequirementStatusConfirmed: true,
		RequirementStatusRejected:  true,
		RequirementStatusClosed:    true,
	},
	RequirementStatusConfirmed: {
		RequirementStatusScheduled:  true,
		RequirementStatusDeveloping: true, // 允许跳过排期直接开发（小需求）
		RequirementStatusRejected:   true,
		RequirementStatusClosed:     true,
	},
	RequirementStatusScheduled: {
		RequirementStatusDeveloping: true,
		RequirementStatusConfirmed:  true, // 从版本移出后回退
		RequirementStatusClosed:     true,
	},
	RequirementStatusDeveloping: {
		RequirementStatusLaunched:  true,
		RequirementStatusScheduled: true,
	},
	RequirementStatusLaunched: {RequirementStatusClosed: true},
	RequirementStatusRejected: {RequirementStatusPendingAnalysis: true},
	RequirementStatusClosed:   {RequirementStatusPendingAnalysis: true},
}

// CanTransitRequirement 判断需求状态迁移是否合法
func CanTransitRequirement(from, to string) bool {
	return requirementTransitions[from][to]
}

var versionTransitions = map[string]map[string]bool{
	VersionStatusPlanning:   {VersionStatusReviewing: true, VersionStatusShelved: true},
	VersionStatusReviewing:  {VersionStatusFinalized: true, VersionStatusPlanning: true, VersionStatusShelved: true},
	VersionStatusFinalized:  {VersionStatusDeveloping: true, VersionStatusShelved: true},
	VersionStatusDeveloping: {VersionStatusLaunched: true},
	VersionStatusShelved:    {VersionStatusPlanning: true},
}

// CanTransitVersion 判断版本状态迁移是否合法（定稿后不可回评审中）
func CanTransitVersion(from, to string) bool {
	return versionTransitions[from][to]
}

// NextVersionNo 生成版本号：patchStep=0 首版固定 1.0.0；
// 1 表示迭代（次版本 +1，修订归零）；>=2 表示同轮重生成（修订 +1）。
// 非法输入按首版处理，避免把脏数据写进版本号。
func NextVersionNo(current string, patchStep int) string {
	cur := strings.TrimPrefix(strings.TrimSpace(current), "v")
	if cur == "" {
		return "1.0.0"
	}
	nums := [3]int{1, 0, 0}
	for i, part := range strings.SplitN(cur, ".", 3) {
		n, err := strconv.Atoi(part)
		if err != nil {
			return "1.0.0"
		}
		nums[i] = n
	}
	switch {
	case patchStep <= 0:
		// 不递增，原样规范化输出
	case patchStep == 1:
		nums[1]++
		nums[2] = 0
	default:
		nums[2]++
	}
	return fmt.Sprintf("%d.%d.%d", nums[0], nums[1], nums[2])
}
