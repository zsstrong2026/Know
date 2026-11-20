package types

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ---- 常量：模板状态（draft 草稿 / published 已发布 / archived 已归档）----
const (
	SopTemplateStatusDraft     = "draft"
	SopTemplateStatusPublished = "published"
	SopTemplateStatusArchived  = "archived"
)

// ---- 常量：节点类型 ----
const (
	SopNodeTypeAgent    = "agent"
	SopNodeTypeScript   = "script"
	SopNodeTypeApproval = "approval"
	SopNodeTypeSOP      = "sop"
)

// ---- 常量：节点执行器（平台智能体 / 本地 CLI）----
const (
	SopExecutorPlatformAgent = "platform_agent"
	SopExecutorCLI           = "cli"
)

// ---- 常量：节点失败策略（loop 重试 / reject 打回上游 / fail 失败 / human 转人工）----
const (
	SopOnFailureLoop   = "loop"
	SopOnFailureReject = "reject"
	SopOnFailureFail   = "fail"
	SopOnFailureHuman  = "human"
)

// ---- 常量：产物校验方式（exists 文件非空 / contains 含关键词 / command 自定义命令）----
const (
	SopArtifactCheckExists   = "exists"
	SopArtifactCheckContains = "contains"
	SopArtifactCheckCommand  = "command"
)

// ---- 常量：definition 默认值（与前端检查器默认、spec §16.15 一致）----
const (
	SopDefaultMaxAttempts       = 3
	SopDefaultMaxReworks        = 2
	SopDefaultLoopMaxIterations = 3
	SopDefaultTimeoutMinutes    = 60
	SopDefaultKBScope           = "project"
	// SopDefaultMCPServer 新节点默认只挂 Know 知识库 MCP。
	SopDefaultMCPServer = "know-kb"
)

// SopPosition 节点在画布上的坐标。
type SopPosition struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// SopNodeExecutor 节点执行器配置；JSON 字段名即前后端契约。
type SopNodeExecutor struct {
	Kind         string   `json:"kind"`               // platform_agent | cli
	AgentID      string   `json:"agent_id,omitempty"` // platform_agent 时的 custom_agents id
	CLIKind      string   `json:"cli_kind,omitempty"` // cli 时：10 种之一
	Model        string   `json:"model,omitempty"`    // 空=默认·跟随凭据配置
	CredentialID string   `json:"credential_id,omitempty"`
	RoleID       *string  `json:"role_id"`            // cli 可选角色；null=仅任务书
	KBScope      string   `json:"kb_scope,omitempty"` // project | custom | all
	KBIDs        []string `json:"kb_ids,omitempty"`
}

// SopArtifactCheck 产物校验声明（编译为门禁命令，见 gate_commands）。
type SopArtifactCheck struct {
	Type    string `json:"type"` // exists | contains | command
	Keyword string `json:"keyword,omitempty"`
	Command string `json:"command,omitempty"`
}

// SopArtifactOutput 节点产物约定。
type SopArtifactOutput struct {
	Name      string           `json:"name"`
	Path      string           `json:"path"`
	MediaType string           `json:"media_type"`
	Required  bool             `json:"required"`
	Check     SopArtifactCheck `json:"check"`
}

// SopNode 流程节点。
type SopNode struct {
	ID                string              `json:"id"`
	Name              string              `json:"name"`
	Type              string              `json:"type"` // agent | script | approval | sop
	Executor          SopNodeExecutor     `json:"executor"`
	TaskPrompt        string              `json:"task_prompt"`
	TaskRefs          []string            `json:"task_refs"`
	ExtraRules        string              `json:"extra_rules"`
	Skills            []string            `json:"skills"` // nil=角色全部（JSON null）
	MCP               []string            `json:"mcp"`
	ArtifactOutputs   []SopArtifactOutput `json:"artifact_outputs"`
	GateCommands      []string            `json:"gate_commands"` // 派生字段，AI/用户不直写
	GateExtra         string              `json:"gate_extra"`
	OnFailure         string              `json:"on_failure"` // loop | reject | fail | human
	RejectTo          string              `json:"reject_to"`
	MaxAttempts       int                 `json:"max_attempts"`
	MaxReworks        int                 `json:"max_reworks"`
	LoopMaxIterations int                 `json:"loop_max_iterations"`
	CarryOver         bool                `json:"carry_over"`
	SerialGroup       string              `json:"serial_group"`
	TimeoutMinutes    int                 `json:"timeout_minutes"`
	Position          SopPosition         `json:"position"`
	Script            string              `json:"script,omitempty"`
	CredentialEnv     map[string]string   `json:"credential_env,omitempty"`
	ApproverHint      string              `json:"approver_hint,omitempty"`
	Notification      string              `json:"notification,omitempty"`
	// ApprovalTimeoutHours 0=无限等待（spec §6 默认）。
	ApprovalTimeoutHours int `json:"approval_timeout_hours,omitempty"`
	// ApprovalTimeoutAction notify（默认，提醒）| fail（自动失败）。
	ApprovalTimeoutAction string `json:"approval_timeout_action,omitempty"`
	// AutoIngest 门禁通过后产物自动入绑定 KB（T36）。
	AutoIngest bool `json:"auto_ingest,omitempty"`
	// ChildTemplateID sop 节点引用的子流程模板。
	ChildTemplateID string `json:"child_template_id,omitempty"`
}

// SopEdge 节点依赖边；失败打回不建边，写 node.reject_to。
type SopEdge struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
	When   string `json:"when"` // success（依赖边）
}

// SopDefinition 模板定义（发布即冻结；运行时引用 revision 的快照）。
//
// JSON 字段名是全计划前/后端契约，不得改名。
type SopDefinition struct {
	Nodes               []SopNode `json:"nodes"`
	Edges               []SopEdge `json:"edges"`
	InputSchema         []string  `json:"input_schema"`
	OnSuccessItemStatus string    `json:"on_success_item_status"`
}

// SopTemplate 流程模板：definition 是当前草稿，发布产生不可变 revision。
type SopTemplate struct {
	ID                  string         `gorm:"primaryKey;type:varchar(36)" json:"id"`
	TenantID            uint64         `gorm:"not null" json:"tenant_id"`
	ProjectID           string         `gorm:"type:varchar(36)" json:"project_id"` // 空=全局
	Name                string         `gorm:"type:varchar(200);not null" json:"name"`
	Description         string         `gorm:"type:text" json:"description"`
	Status              string         `gorm:"type:varchar(16);default:'draft'" json:"status"`
	Version             int            `gorm:"default:1" json:"version"`
	PublishedRevisionID string         `gorm:"type:varchar(36)" json:"published_revision_id"`
	Definition          SopDefinition  `gorm:"serializer:json" json:"definition"`
	IsBuiltin           bool           `gorm:"default:false" json:"is_builtin"`
	MetricCategory      string         `gorm:"type:varchar(32)" json:"metric_category"`
	CreatedBy           string         `gorm:"type:varchar(36)" json:"created_by"`
	CreatedAt           time.Time      `json:"created_at"`
	UpdatedAt           time.Time      `json:"updated_at"`
	DeletedAt           gorm.DeletedAt `gorm:"index" json:"-"`
}

func (SopTemplate) TableName() string { return "sop_templates" }

// BeforeCreate 补主键、时间戳与草稿默认值；definition 内的集合补空切片，
// 避免 JSON 列写成 NULL 或与列默认 '{}' 不一致。
func (t *SopTemplate) BeforeCreate(_ *gorm.DB) error {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	now := time.Now()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	if t.UpdatedAt.IsZero() {
		t.UpdatedAt = now
	}
	if t.Status == "" {
		t.Status = SopTemplateStatusDraft
	}
	if t.Version == 0 {
		t.Version = 1
	}
	EnsureSopDefinitionSlices(&t.Definition)
	return nil
}

// SopTemplateRevision 模板发布快照：发布即冻结，永不修改或删除（spec §3.3）。
type SopTemplateRevision struct {
	ID          string        `gorm:"primaryKey;type:varchar(36)" json:"id"`
	TenantID    uint64        `gorm:"not null" json:"tenant_id"`
	TemplateID  string        `gorm:"type:varchar(36);not null" json:"template_id"`
	Revision    int           `gorm:"not null" json:"revision"`
	Definition  SopDefinition `gorm:"serializer:json" json:"definition"`
	PublishedBy string        `gorm:"type:varchar(36)" json:"published_by"`
	CreatedAt   time.Time     `json:"created_at"`
}

func (SopTemplateRevision) TableName() string { return "sop_template_revisions" }

func (r *SopTemplateRevision) BeforeCreate(_ *gorm.DB) error {
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now()
	}
	EnsureSopDefinitionSlices(&r.Definition)
	return nil
}

// EnsureSopDefinitionSlices 把 definition 里的集合 nil 补成空切片，
// 让落库 JSON 恒为数组而非 null（读回后前端无需判空）。
func EnsureSopDefinitionSlices(def *SopDefinition) {
	if def.Nodes == nil {
		def.Nodes = []SopNode{}
	}
	if def.Edges == nil {
		def.Edges = []SopEdge{}
	}
	if def.InputSchema == nil {
		def.InputSchema = []string{}
	}
}

// NormalizeSopDefinition 逐节点补默认值，只补零值、不覆盖显式配置。
//
// carry_over 没有「未设置」的表达位（bool 零值即 false，而 serializer 往返会丢掉
// 额外的 set 标记），因此只在「整个节点看起来还是新建态」（失败策略与三项次数预算
// 全为零）时置 true；一旦节点带任何显式预算（前端/AI 新建节点恒带 3/2/3/60），
// 显式的 carry_over=false 就被保留。Normalize 对同一节点幂等。
func NormalizeSopDefinition(def *SopDefinition) {
	if def == nil {
		return
	}
	for i := range def.Nodes {
		n := &def.Nodes[i]
		isFreshNode := n.OnFailure == "" && n.MaxAttempts == 0 && n.MaxReworks == 0 &&
			n.LoopMaxIterations == 0 && n.TimeoutMinutes == 0

		if n.MaxAttempts == 0 {
			n.MaxAttempts = SopDefaultMaxAttempts
		}
		if n.MaxReworks == 0 {
			n.MaxReworks = SopDefaultMaxReworks
		}
		if n.LoopMaxIterations == 0 {
			n.LoopMaxIterations = SopDefaultLoopMaxIterations
		}
		if n.TimeoutMinutes == 0 {
			n.TimeoutMinutes = SopDefaultTimeoutMinutes
		}
		if n.OnFailure == "" {
			n.OnFailure = SopOnFailureLoop
		}
		// MCP：nil=未设置→默认 know-kb；显式空数组=用户全取消，必须保留。
		if n.MCP == nil {
			n.MCP = []string{SopDefaultMCPServer}
		}
		if n.Executor.KBScope == "" {
			n.Executor.KBScope = SopDefaultKBScope
		}
		for j := range n.ArtifactOutputs {
			if n.ArtifactOutputs[j].Check.Type == "" {
				n.ArtifactOutputs[j].Check.Type = SopArtifactCheckExists
			}
		}
		if isFreshNode {
			n.CarryOver = true
		}
	}
}
