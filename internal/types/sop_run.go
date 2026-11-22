package types

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ---- 常量：运行状态（7 态；queued→running→waiting/succeeded/failed/cancelled/paused）----
const (
	SopRunQueued    = "queued"
	SopRunRunning   = "running"
	SopRunWaiting   = "waiting"
	SopRunSucceeded = "succeeded"
	SopRunFailed    = "failed"
	SopRunCancelled = "cancelled"
	SopRunPaused    = "paused"
)

// ---- 常量：节点运行状态（8 态）----
const (
	SopNodePending         = "pending"
	SopNodeRunning         = "running"
	SopNodeWaitingApproval = "waiting_approval"
	SopNodePendingInput    = "pending_input"
	SopNodeSucceeded       = "succeeded"
	SopNodeFailed          = "failed"
	SopNodeCancelled       = "cancelled"
	SopNodeSkipped         = "skipped"
)

// ---- 常量：门禁结果（pass 通过 / rework 打回 / fail 失败 / human 转人工）----
const (
	SopGatePass   = "pass"
	SopGateRework = "rework"
	SopGateFail   = "fail"
	SopGateHuman  = "human"
)

// ---- 常量：运行触发来源（manual 手动 / schedule 定时 / api 外部接口 / child_run 子流程）----
const (
	SopRunTriggerManual   = "manual"
	SopRunTriggerSchedule = "schedule"
	SopRunTriggerAPI      = "api"
	SopRunTriggerChildRun = "child_run"
)

// SOPSessionMarker 标记 SOP 节点执行会话：会话描述带此前缀（后接 run=… node=…），
// 首页对话列表据此隐藏（仿 pm_analysis / meeting_artifact 的隐藏约定）。
//
// 带方括号是用户可见习惯：即使在会话详情里露出，也一眼能认出是系统会话。
const SOPSessionMarker = "[SOP] "

// SopRunActiveStatuses 活跃态：占用「进行中运行」额度（CountActiveRuns 口径）。
var SopRunActiveStatuses = []string{
	SopRunQueued, SopRunRunning, SopRunWaiting, SopRunPaused,
}

// SopRunFinishedStatuses 终态（已结束）：统计成功率时分母。
var SopRunFinishedStatuses = []string{
	SopRunSucceeded, SopRunFailed, SopRunCancelled,
}

// IsSopRunActive 判断运行是否仍占活跃额度（queued/running/waiting/paused）。
func IsSopRunActive(status string) bool {
	for _, s := range SopRunActiveStatuses {
		if s == status {
			return true
		}
	}
	return false
}

// IsSopRunFinished 判断运行是否已结束（succeeded/failed/cancelled）。
func IsSopRunFinished(status string) bool {
	for _, s := range SopRunFinishedStatuses {
		if s == status {
			return true
		}
	}
	return false
}

// SopHumanContextEntry 人工介入上下文条目：审批意见、补充信息、系统提醒都追加到这里，
// 执行时注入任务书（只增不改）。
type SopHumanContextEntry struct {
	At     time.Time `json:"at"`
	Source string    `json:"source"`
	NodeID string    `json:"node_id"`
	Text   string    `json:"text"`
}

// SopUsage 模型用量：节点执行后逐次累计到节点与运行。
type SopUsage struct {
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

// SopRun 一次流程运行：workflow_snapshot 是发布 revision 的冻结快照（spec §3.3）。
//
// 租约语义：queued 的行由调度器以 CAS 抢占为 running 并写 lease_until；执行中每 10s
// 续租；进程崩溃后 lease_until 过期，ExpireLeases 把它回收为 failed（可重试）。
type SopRun struct {
	ID               string                 `gorm:"type:varchar(36);primaryKey" json:"id"`
	TenantID         uint64                 `gorm:"index" json:"tenant_id"`
	TemplateID       string                 `gorm:"type:varchar(36)" json:"template_id"`
	RevisionID       string                 `gorm:"type:varchar(36)" json:"revision_id"`
	WorkflowSnapshot SopDefinition          `gorm:"serializer:json" json:"workflow_snapshot"`
	ProjectID        string                 `gorm:"type:varchar(36)" json:"project_id"`
	ItemID           string                 `gorm:"type:varchar(36);index" json:"item_id"`
	ParentRunID      string                 `gorm:"type:varchar(36)" json:"parent_run_id"`
	TriggerKind      string                 `gorm:"type:varchar(16);default:manual" json:"trigger_kind"`
	TriggerRefID     string                 `gorm:"type:varchar(36)" json:"trigger_ref_id"`
	Status           string                 `gorm:"type:varchar(16);default:queued" json:"status"`
	Input            map[string]any         `gorm:"serializer:json" json:"input"`
	HumanContext     []SopHumanContextEntry `gorm:"serializer:json" json:"human_context"`
	// ProjectContext 本次运行注入任务书的「项目现状」文本（T64 写入）。
	ProjectContext  string     `gorm:"type:text" json:"project_context"`
	WorkspaceDir    string     `gorm:"type:varchar(500)" json:"workspace_dir"`
	LeaseUntil      *time.Time `json:"lease_until"`
	HeartbeatAt     *time.Time `json:"heartbeat_at"`
	CancelRequested bool       `gorm:"not null;default:false" json:"cancel_requested"`
	PauseRequested  bool       `gorm:"not null;default:false" json:"pause_requested"`
	Usage           SopUsage   `gorm:"serializer:json" json:"usage"`
	// ExecutionDurationMs 执行时长（毫秒），由引擎在终态回填。
	ExecutionDurationMs int64          `gorm:"default:0" json:"execution_duration_ms"`
	Error               string         `gorm:"type:text" json:"error"`
	StartedAt           *time.Time     `json:"started_at"`
	FinishedAt          *time.Time     `json:"finished_at"`
	CreatedBy           string         `gorm:"type:varchar(36)" json:"created_by,omitempty"`
	CreatedAt           time.Time      `json:"created_at"`
	UpdatedAt           time.Time      `json:"updated_at"`
	DeletedAt           gorm.DeletedAt `gorm:"index" json:"-"`
}

func (SopRun) TableName() string { return "sop_runs" }

// BeforeCreate 补主键、时间戳与默认状态/JSON 列：serializer 列写成 NULL 会与列默认
// '{}' / '[]' 不一致，读回后也拿不到可用的空集合。
func (r *SopRun) BeforeCreate(_ *gorm.DB) error {
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	now := time.Now()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = now
	}
	if r.Status == "" {
		r.Status = SopRunQueued
	}
	if r.TriggerKind == "" {
		r.TriggerKind = SopRunTriggerManual
	}
	if r.Input == nil {
		r.Input = map[string]any{}
	}
	if r.HumanContext == nil {
		r.HumanContext = []SopHumanContextEntry{}
	}
	EnsureSopDefinitionSlices(&r.WorkflowSnapshot)
	return nil
}

// SopNodeRun 节点运行记录：一次 run 内每个节点一行（(run_id, node_id) 唯一）。
// 无软删：运行历史不可变，重跑走 ResetNodes 更新状态。
type SopNodeRun struct {
	ID       string `gorm:"type:varchar(36);primaryKey" json:"id"`
	TenantID uint64 `gorm:"index" json:"tenant_id"`
	// (run_id, node_id) 唯一由迁移里的 ux_sop_node_runs_run_node 约束（GORM tag 不镜像
	// 唯一索引，与 sop_items 的迁移口径一致）。
	RunID           string     `gorm:"type:varchar(36);index" json:"run_id"`
	NodeID          string     `gorm:"type:varchar(36)" json:"node_id"`
	Status          string     `gorm:"type:varchar(24);default:pending" json:"status"`
	Attempt         int        `gorm:"default:0" json:"attempt"`
	ReworkCount     int        `gorm:"default:0" json:"rework_count"`
	Output          string     `gorm:"type:text" json:"output"`
	Error           string     `gorm:"type:text" json:"error"`
	Feedback        string     `gorm:"type:text" json:"feedback"`
	GateResult      string     `gorm:"type:varchar(16)" json:"gate_result"`
	GateReason      string     `gorm:"type:text" json:"gate_reason"`
	SessionID       string     `gorm:"type:varchar(36)" json:"session_id"`
	ResumeSessionID string     `gorm:"type:varchar(128)" json:"resume_session_id"`
	ChildRunID      string     `gorm:"type:varchar(36)" json:"child_run_id"`
	Usage           SopUsage   `gorm:"serializer:json" json:"usage"`
	StartedAt       *time.Time `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func (SopNodeRun) TableName() string { return "sop_node_runs" }

func (n *SopNodeRun) BeforeCreate(_ *gorm.DB) error {
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
	if n.Status == "" {
		n.Status = SopNodePending
	}
	return nil
}

// SopArtifact 节点产物：文件在工作区内的相对路径 + 内容指纹（sha256），
// 可选回填入库后的 KB / Knowledge 关联。
type SopArtifact struct {
	ID          string         `gorm:"type:varchar(36);primaryKey" json:"id"`
	TenantID    uint64         `gorm:"index" json:"tenant_id"`
	RunID       string         `gorm:"type:varchar(36);index" json:"run_id"`
	NodeID      string         `gorm:"type:varchar(36)" json:"node_id"`
	Attempt     int            `gorm:"default:1" json:"attempt"`
	Name        string         `gorm:"type:varchar(500);not null" json:"name"`
	Path        string         `gorm:"type:varchar(500);not null" json:"path"`
	MediaType   string         `gorm:"type:varchar(100)" json:"media_type"`
	Size        int64          `json:"size"`
	SHA256      string         `gorm:"type:varchar(64)" json:"sha256"`
	KBID        string         `gorm:"type:varchar(36)" json:"kb_id"`
	KnowledgeID string         `gorm:"type:varchar(36)" json:"knowledge_id"`
	IngestedAt  *time.Time     `json:"ingested_at"`
	CreatedAt   time.Time      `json:"created_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

func (SopArtifact) TableName() string { return "sop_artifacts" }

func (a *SopArtifact) BeforeCreate(_ *gorm.DB) error {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}
	if a.Attempt == 0 {
		a.Attempt = 1
	}
	return nil
}

// SopRunFilter 运行列表查询条件；零值表示不过滤。
//
// Page/PageSize 走 ListRuns 的分页参数，不进本结构。
type SopRunFilter struct {
	// ProjectID 空=不限项目；ItemID 空=不限事项。
	ProjectID  string `json:"project_id"`
	ItemID     string `json:"item_id"`
	Status     string `json:"status"`
	TemplateID string `json:"template_id"`
	// Q 模糊搜索：匹配运行 id（事项标题/编号与 SOP 名由调用方先解析成 ID 入 filter）。
	Q string `json:"q"`
}

// SopRunStats 运行统计摘要（运行记录页头口径）。
type SopRunStats struct {
	// SuccessRate 成功率 = succeeded / 已结束（succeeded+failed+cancelled）。
	SuccessRate float64 `json:"success_rate"`
	// FinishedCount 已结束总数；TotalCount 全部运行数。
	FinishedCount int64 `json:"finished_count"`
	TotalCount    int64 `json:"total_count"`
	// TotalTokens 累计 token（input+output）。
	TotalTokens int64 `json:"total_tokens"`
	// StatusCounts 各状态计数（前端 chips 用，缺省状态不出现）。
	StatusCounts map[string]int64 `json:"status_counts"`
}
