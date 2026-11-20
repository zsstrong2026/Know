package types

import (
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ---- 常量：事项状态（8 态，唯一写入口是 SopItemRepo.UpdateStatus → CanTransitSopItem）----
const (
	SopItemStatusBacklog    = "backlog"
	SopItemStatusTodo       = "todo"
	SopItemStatusInProgress = "in_progress"
	SopItemStatusInReview   = "in_review"
	SopItemStatusDone       = "done"
	SopItemStatusClosed     = "closed"
	SopItemStatusBlocked    = "blocked"
	SopItemStatusCancelled  = "cancelled"
)

// ---- 常量：执行情况五态（由事项状态 + 最近一次运行状态派生，不落库）----
const (
	SopExecStateNone      = "none"
	SopExecStateRunning   = "running"
	SopExecStateIntervene = "intervene"
	SopExecStatePR        = "pr"
	SopExecStateClosed    = "closed"
)

// SopItem 工作项：8 态状态机 + 项目内编号 + 大需求两级拆分（is_big/parent_id/split_source）。
type SopItem struct {
	ID          string   `gorm:"type:varchar(36);primaryKey" json:"id"`
	TenantID    uint64   `gorm:"index" json:"-"`
	ProjectID   string   `gorm:"type:varchar(36);index" json:"project_id"`
	No          string   `gorm:"type:varchar(24)" json:"no"`
	Title       string   `gorm:"type:varchar(500)" json:"title"`
	Description string   `gorm:"type:text" json:"description"`
	Kind        string   `gorm:"type:varchar(16);default:requirement" json:"kind"`
	Priority    string   `gorm:"type:varchar(4);default:P2" json:"priority"`
	Status      string   `gorm:"type:varchar(16);default:backlog" json:"status"`
	Source      string   `gorm:"type:varchar(16);default:internal" json:"source"`
	Proposer    string   `gorm:"type:varchar(200)" json:"proposer"`
	Owner       string   `gorm:"type:varchar(200)" json:"owner"`
	Tags        []string `gorm:"serializer:json" json:"tags"`
	TemplateID  string   `gorm:"type:varchar(36)" json:"template_id"`
	KBID        string   `gorm:"type:varchar(36)" json:"kb_id"`
	KnowledgeID string   `gorm:"type:varchar(36)" json:"knowledge_id"`
	// Outcome 产物结论（value/notes 等自由结构），DomainData 领域自定义字段。
	Outcome    map[string]any `gorm:"serializer:json" json:"outcome"`
	DomainData map[string]any `gorm:"serializer:json" json:"domain_data"`
	LastRunID  string         `gorm:"type:varchar(36);index" json:"last_run_id"`
	// IsBig 大项容器：本身不可发起运行，执行情况由子项汇总。
	IsBig bool `gorm:"not null;default:false" json:"is_big"`
	// ParentID 非空表示是大项的子项（只允许两级，父项 ParentID 必须为空）。
	ParentID string `gorm:"type:varchar(36)" json:"parent_id"`
	// SplitSource 拆分来源：manual / ai / template（独立事项为空）。
	SplitSource string         `gorm:"type:varchar(16)" json:"split_source"`
	CreatedBy   string         `gorm:"type:varchar(36)" json:"created_by"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

func (SopItem) TableName() string { return "sop_items" }

// BeforeCreate 补主键与时间戳；JSON 列补默认值，避免空值被驱动写成 NULL。
func (it *SopItem) BeforeCreate(_ *gorm.DB) error {
	if it.ID == "" {
		it.ID = uuid.NewString()
	}
	now := time.Now()
	if it.CreatedAt.IsZero() {
		it.CreatedAt = now
	}
	if it.UpdatedAt.IsZero() {
		it.UpdatedAt = now
	}
	if it.Tags == nil {
		it.Tags = []string{}
	}
	if it.Outcome == nil {
		it.Outcome = map[string]any{}
	}
	if it.DomainData == nil {
		it.DomainData = map[string]any{}
	}
	return nil
}

// SopItemAttachment 事项附件：文件字节直存数据库，列表响应不回传 Data。
type SopItemAttachment struct {
	ID        string `gorm:"type:varchar(36);primaryKey" json:"id"`
	TenantID  uint64 `gorm:"index" json:"-"`
	ItemID    string `gorm:"type:varchar(36);index" json:"item_id"`
	Name      string `gorm:"type:varchar(500)" json:"name"`
	MediaType string `gorm:"type:varchar(100)" json:"media_type"`
	Size      int64  `json:"size"`
	Data      []byte `gorm:"type:bytea" json:"-"`
	// TextContent 纯文本类附件抽取的正文，供任务书与 AI 上下文复用。
	TextContent string         `gorm:"type:text" json:"text_content"`
	CreatedAt   time.Time      `json:"created_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

func (SopItemAttachment) TableName() string { return "sop_item_attachments" }

func (a *SopItemAttachment) BeforeCreate(_ *gorm.DB) error {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}
	return nil
}

// SopItemWithDerived 列表行：事项本体 + 前端列表/看板需要的派生字段。
type SopItemWithDerived struct {
	SopItem
	// CurrentNode 当前环节（节点名；终态为「已完成」/「已关闭」；无运行且未终态为空）。
	CurrentNode string `json:"current_node"`
	// ProgressDone/ProgressTotal 最近一次运行的节点完成数 / 节点总数。
	ProgressDone  int `json:"progress_done"`
	ProgressTotal int `json:"progress_total"`
	// ExecState 执行情况五态（none/running/intervene/pr/closed）。
	ExecState string `json:"exec_state"`
}

// SopItemFilter 列表查询条件；零值表示不过滤。
//
// ParentID 为指针：nil=不过滤，指向 "" 时只返回顶层事项（父项），
// 指向某个 id 时只返回该父项的子项。Page/PageSize 在 exec 过滤之后生效。
type SopItemFilter struct {
	Q        string  `json:"q"`
	Status   string  `json:"status"`
	Owner    string  `json:"owner"`
	Source   string  `json:"source"`
	Kind     string  `json:"kind"`
	Exec     string  `json:"exec"`
	ParentID *string `json:"parent_id"`
	Page     int     `json:"page"`
	PageSize int     `json:"page_size"`
}

// sopItemTransitions 事项状态机：from → 允许的 to 集合。
var sopItemTransitions = map[string][]string{
	SopItemStatusBacklog:    {SopItemStatusTodo, SopItemStatusCancelled},
	SopItemStatusTodo:       {SopItemStatusInProgress, SopItemStatusBacklog, SopItemStatusCancelled},
	SopItemStatusInProgress: {SopItemStatusInReview, SopItemStatusBlocked, SopItemStatusDone, SopItemStatusCancelled},
	SopItemStatusInReview:   {SopItemStatusInProgress, SopItemStatusDone, SopItemStatusCancelled},
	SopItemStatusBlocked:    {SopItemStatusInProgress, SopItemStatusTodo, SopItemStatusCancelled},
	SopItemStatusDone:       {SopItemStatusClosed},
	SopItemStatusClosed:     {SopItemStatusTodo},
	SopItemStatusCancelled:  {SopItemStatusTodo},
}

// CanTransitSopItem 判断事项状态迁移是否合法；同状态视为幂等 no-op 而放行。
func CanTransitSopItem(from, to string) bool {
	if from == to {
		return true
	}
	for _, s := range sopItemTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// DeriveExecState 执行情况五态口径（spec §3.2）：已闭环优先于运行态。
func DeriveExecState(itemStatus, runStatus string, hasRun bool) string {
	if itemStatus == SopItemStatusDone || itemStatus == SopItemStatusClosed {
		return SopExecStateClosed
	}
	if !hasRun || runStatus == "cancelled" {
		return SopExecStateNone
	}
	switch runStatus {
	case "running", "paused":
		return SopExecStateRunning
	case "waiting", "failed":
		return SopExecStateIntervene
	case "succeeded":
		return SopExecStatePR
	}
	return SopExecStateNone
}

var sopSourceLabels = map[string]string{
	"customer": "客户反馈", "internal": "平台新建", "data": "数据洞察",
	"competitor": "竞品分析", "strategy": "战略规划", "other": "其他",
}

// SopSourceLabel 来源中文映射；未知来源一律回退「其他」（键值落库不变）。
func SopSourceLabel(source string) string {
	if l, ok := sopSourceLabels[source]; ok {
		return l
	}
	return "其他"
}

// SopExecStateLabels 执行情况五态中文文案（与 DeriveExecState 的取值一一对应）。
var SopExecStateLabels = map[string]string{
	SopExecStateNone:      "尚未执行",
	SopExecStateRunning:   "正在推进",
	SopExecStateIntervene: "需要介入",
	SopExecStatePR:        "待处理 PR",
	SopExecStateClosed:    "已闭环",
}

// DeriveProgress 由节点运行状态列表派生完成数/总数（succeeded、skipped 计入完成）。
func DeriveProgress(statuses []string) (done, total int) {
	for _, s := range statuses {
		total++
		if s == "succeeded" || s == "skipped" {
			done++
		}
	}
	return
}

// DeriveCurrentNode 当前环节：失败 > 待确认 > 运行中 > 终态文案，取各自第一个节点名。
func DeriveCurrentNode(itemStatus string, running, waiting, failed []string) string {
	switch {
	case len(failed) > 0:
		return failed[0]
	case len(waiting) > 0:
		return waiting[0]
	case len(running) > 0:
		return running[0]
	case itemStatus == SopItemStatusDone:
		return "已完成"
	case itemStatus == SopItemStatusClosed:
		return "已关闭"
	}
	return ""
}

// SopNodeRefSep 是列表派生用的节点引用分隔符。
// 服务层把「最近一次运行的节点」压成 []string 注入仓储，每个元素形如 "<节点状态>|<节点名>"：
// 状态给 DeriveProgress 计数，节点名给 DeriveCurrentNode 展示。解析统一走
// ParseSopNodeRef，避免格式约定在调用方各写一遍而漂移。
const SopNodeRefSep = "|"

// ParseSopNodeRef 拆一条节点引用；缺分隔符时整串视为状态、节点名为空。
func ParseSopNodeRef(ref string) (status, name string) {
	status, name, _ = strings.Cut(ref, SopNodeRefSep)
	return status, name
}
