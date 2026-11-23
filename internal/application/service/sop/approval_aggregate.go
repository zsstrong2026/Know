// approval_aggregate.go 是审批中心的三源只读聚合：把「SOP 关口等待」「智能体
// 追问挂起」「自动化待审批」三处待办并成一张表，按等待时长降序交给前端。
//
// 三条口径：
//   - 只读：本文件不写任何业务表，也不改 automation/hitl 的既有代码。聚合源以窄
//     接口注入（SOP 运行仓储方法集正好满足，项目/事项复用 context.go 的两个
//     Getter）；跨租户列表查询由本文件提供的 gorm 适配器完成（T21 接线直接注入）。
//   - 来源失败不拖垮整页：哪一路读失败就少哪一路，其余照常返回（审批中心空白
//     比缺一路更难排查）。
//   - 处置分发不在本层：前端按 entry.kind + actions 调各自接口（SOP → ApprovalService，
//     agent_prompt → 现有答复 API，automation → 现有审批 API）。
package sop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// ApprovalEntry 是审批中心的一行待办（字段名即前端契约）。
type ApprovalEntry struct {
	Kind         string    `json:"kind"` // sop | agent_prompt | automation
	RefID        string    `json:"ref_id"`
	RunID        string    `json:"run_id,omitempty"`
	NodeID       string    `json:"node_id,omitempty"`
	SessionID    string    `json:"session_id,omitempty"`
	Title        string    `json:"title"`
	ProjectName  string    `json:"project_name,omitempty"`
	ItemTitle    string    `json:"item_title,omitempty"`
	Reason       string    `json:"reason"`
	WaitingSince time.Time `json:"waiting_since"`
	Actions      []string  `json:"actions"` // sop: approve|reject|rework|terminate|answer；automation: approve|reject；agent_prompt: answer
}

// 待办来源种类（与前端徽标一一对应）。
const (
	ApprovalKindSOP         = "sop"
	ApprovalKindAgentPrompt = "agent_prompt"
	ApprovalKindAutomation  = "automation"
)

// 处置动作（前端按这些键渲染按钮）。
const (
	ApprovalActionApprove   = "approve"
	ApprovalActionReject    = "reject"
	ApprovalActionRework    = "rework"
	ApprovalActionTerminate = "terminate"
	ApprovalActionAnswer    = "answer"
)

// PendingAutomation 是一路自动化待审批摘要（自动化模块的读结果摊平后交给本服务）。
type PendingAutomation struct {
	RunID          string
	AutomationName string
	SessionID      string
	Reason         string
	WaitingSince   time.Time
}

// ApprovalRunSource 是聚合需要的运行读能力（真实实现 = repository.SopRunRepo）。
//
// 只声明两个方法：聚合不认领、不推进，也不读产物。
type ApprovalRunSource interface {
	ListRuns(
		ctx context.Context, tenantID uint64, f types.SopRunFilter, page, pageSize int,
	) ([]types.SopRun, error)
	ListNodeRuns(ctx context.Context, tenantID uint64, runID string) ([]types.SopNodeRun, error)
}

// OpenPromptLister 列某租户仍开放的智能体提示（真实实现见 NewOpenPromptLister）。
type OpenPromptLister interface {
	ListOpenByTenant(ctx context.Context, tenantID uint64) ([]*types.AgentPrompt, error)
}

// AutomationApprovalLister 列某租户自动化的待审批运行（真实实现见
// NewAutomationApprovalLister）。
type AutomationApprovalLister interface {
	ListPendingApprovals(ctx context.Context, tenantID uint64) ([]PendingAutomation, error)
}

// AggregateDeps 聚合源；全部可 nil（nil 即该路跳过）。
type AggregateDeps struct {
	Runs        ApprovalRunSource
	Projects    ProjectGetter
	Items       ItemGetter
	Prompts     OpenPromptLister
	Automations AutomationApprovalLister
}

// WithAggregateSources 装配聚合源（返回自身，便于构造链式）。
func (s *ApprovalService) WithAggregateSources(d AggregateDeps) *ApprovalService {
	if s != nil {
		s.aggregates = d
	}
	return s
}

// ListPending 三源聚合，按 WaitingSince 升序（等最久的在最前）。
func (s *ApprovalService) ListPending(ctx context.Context, tenantID uint64) ([]ApprovalEntry, error) {
	if s == nil {
		return nil, errors.New("审批服务未就绪：服务未构造")
	}
	var entries []ApprovalEntry
	entries = append(entries, s.listSOPPending(ctx, tenantID)...)
	entries = append(entries, s.listPromptPending(ctx, tenantID)...)
	entries = append(entries, s.listAutomationPending(ctx, tenantID)...)
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].WaitingSince.Before(entries[j].WaitingSince)
	})
	if entries == nil {
		entries = []ApprovalEntry{}
	}
	return entries, nil
}

// listSOPPending 收集 SOP 的 waiting_approval / pending_input 节点。
func (s *ApprovalService) listSOPPending(ctx context.Context, tenantID uint64) []ApprovalEntry {
	d := s.aggregates
	if d.Runs == nil {
		return nil
	}
	runs, err := d.Runs.ListRuns(ctx, tenantID, types.SopRunFilter{Status: types.SopRunWaiting}, 0, 0)
	if err != nil {
		log.Printf("[sop-approval] 审批中心读取等待运行失败：%v", err)
		return nil
	}
	var out []ApprovalEntry
	for i := range runs {
		run := &runs[i]
		nrs, err := d.Runs.ListNodeRuns(ctx, run.TenantID, run.ID)
		if err != nil {
			log.Printf("[sop-approval] 运行 %s 节点读取失败：%v", run.ID, err)
			continue
		}
		for j := range nrs {
			nr := &nrs[j]
			if nr.Status != types.SopNodeWaitingApproval && nr.Status != types.SopNodePendingInput {
				continue
			}
			out = append(out, ApprovalEntry{
				Kind:         ApprovalKindSOP,
				RefID:        run.ID + "/" + nr.NodeID,
				RunID:        run.ID,
				NodeID:       nr.NodeID,
				Title:        sopNodeTitle(run, nr.NodeID),
				ProjectName:  s.projectName(ctx, run),
				ItemTitle:    s.itemTitle(ctx, run),
				Reason:       sopWaitingReason(nr),
				WaitingSince: nr.UpdatedAt,
				Actions:      sopWaitingActions(nr.Status),
			})
		}
	}
	return out
}

// sopNodeTitle 取节点名（快照里没有该节点时退化为节点 id）。
func sopNodeTitle(run *types.SopRun, nodeID string) string {
	for i := range run.WorkflowSnapshot.Nodes {
		n := &run.WorkflowSnapshot.Nodes[i]
		if n.ID == nodeID {
			if name := strings.TrimSpace(n.Name); name != "" {
				return name
			}
			break
		}
	}
	return nodeID
}

// sopWaitingReason 等待理由：审批用节点的 GateReason，补充信息用固定白话。
func sopWaitingReason(nr *types.SopNodeRun) string {
	if nr.Status == types.SopNodePendingInput {
		return "需要补充信息"
	}
	if reason := strings.TrimSpace(nr.GateReason); reason != "" {
		return reason
	}
	return "等待人工审批"
}

// sopWaitingActions 按节点状态给出可执行动作。
func sopWaitingActions(status string) []string {
	if status == types.SopNodePendingInput {
		return []string{ApprovalActionAnswer}
	}
	return []string{ApprovalActionApprove, ApprovalActionReject, ApprovalActionRework, ApprovalActionTerminate}
}

// projectName 查项目名（读不到不算错：审批中心不能因此少一行）。
func (s *ApprovalService) projectName(ctx context.Context, run *types.SopRun) string {
	if s.aggregates.Projects == nil || strings.TrimSpace(run.ProjectID) == "" {
		return ""
	}
	proj, err := s.aggregates.Projects.GetByID(ctx, run.TenantID, run.ProjectID)
	if err != nil || proj == nil {
		return ""
	}
	return proj.Name
}

// itemTitle 查事项标题（同上，读不到不算错）。
func (s *ApprovalService) itemTitle(ctx context.Context, run *types.SopRun) string {
	if s.aggregates.Items == nil || strings.TrimSpace(run.ItemID) == "" {
		return ""
	}
	item, err := s.aggregates.Items.GetByID(ctx, run.TenantID, run.ItemID)
	if err != nil || item == nil {
		return ""
	}
	return item.Title
}

// listPromptPending 收集智能体追问/审批挂起。
func (s *ApprovalService) listPromptPending(ctx context.Context, tenantID uint64) []ApprovalEntry {
	if s.aggregates.Prompts == nil {
		return nil
	}
	prompts, err := s.aggregates.Prompts.ListOpenByTenant(ctx, tenantID)
	if err != nil {
		log.Printf("[sop-approval] 审批中心读取智能体挂起失败：%v", err)
		return nil
	}
	out := make([]ApprovalEntry, 0, len(prompts))
	for _, p := range prompts {
		if p == nil {
			continue
		}
		out = append(out, ApprovalEntry{
			Kind:         ApprovalKindAgentPrompt,
			RefID:        p.ID,
			SessionID:    p.SessionID,
			Title:        promptTitle(p),
			Reason:       promptReason(p),
			WaitingSince: p.CreatedAt,
			Actions:      []string{ApprovalActionAnswer},
		})
	}
	return out
}

// promptTitle 从提示请求里取问题；解析不出时按种类给白话兜底。
func promptTitle(p *types.AgentPrompt) string {
	var req types.AskUserPromptRequest
	if len(p.Request) > 0 && json.Unmarshal(p.Request, &req) == nil {
		if q := strings.TrimSpace(req.Question); q != "" {
			return q
		}
	}
	switch p.Kind {
	case types.AgentPromptKindToolApproval:
		return "工具调用待确认"
	case types.AgentPromptKindMCPOAuth:
		return "MCP 授权待确认"
	default:
		return "智能体等待你的回答"
	}
}

// promptReason 是卡片上的副标题：明确这是哪一类人工介入。
func promptReason(p *types.AgentPrompt) string {
	switch p.Kind {
	case types.AgentPromptKindToolApproval:
		return "智能体请求调用工具，等待确认"
	case types.AgentPromptKindMCPOAuth:
		return "智能体请求 MCP 授权"
	default:
		return "智能体提问，等待回答"
	}
}

// listAutomationPending 收集自动化待审批运行。
func (s *ApprovalService) listAutomationPending(ctx context.Context, tenantID uint64) []ApprovalEntry {
	if s.aggregates.Automations == nil {
		return nil
	}
	items, err := s.aggregates.Automations.ListPendingApprovals(ctx, tenantID)
	if err != nil {
		log.Printf("[sop-approval] 审批中心读取自动化待审批失败：%v", err)
		return nil
	}
	out := make([]ApprovalEntry, 0, len(items))
	for _, it := range items {
		title := strings.TrimSpace(it.AutomationName)
		if title == "" {
			title = "自动化运行"
		}
		out = append(out, ApprovalEntry{
			Kind:         ApprovalKindAutomation,
			RefID:        it.RunID,
			RunID:        it.RunID,
			SessionID:    it.SessionID,
			Title:        title,
			Reason:       firstNotEmpty(it.Reason, "自动化运行等待确认"),
			WaitingSince: it.WaitingSince,
			Actions:      []string{ApprovalActionApprove, ApprovalActionReject},
		})
	}
	return out
}

// firstNotEmpty 返回第一个非空字符串（聚合文案兜底）。
func firstNotEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// ---- gorm 适配器（T21 接线直接注入；只读，不改旧模块）----

// promptLister 按租户列开放中的智能体提示。
type promptLister struct{ db *gorm.DB }

// NewOpenPromptLister 构造智能体提示列表器（pending / suspended 视为开放）。
func NewOpenPromptLister(db *gorm.DB) OpenPromptLister { return &promptLister{db: db} }

func (l *promptLister) ListOpenByTenant(ctx context.Context, tenantID uint64) ([]*types.AgentPrompt, error) {
	if l == nil || l.db == nil {
		return nil, nil
	}
	var rows []*types.AgentPrompt
	err := l.db.WithContext(ctx).
		Where("tenant_id = ? AND status IN ?", tenantID,
			[]string{types.AgentPromptPending, types.AgentPromptSuspended}).
		Order("created_at ASC").
		Find(&rows).Error
	return rows, err
}

// automationLister 按租户列自动化待审批运行（连表取自动化名）。
type automationLister struct{ db *gorm.DB }

// NewAutomationApprovalLister 构造自动化待审批列表器。
func NewAutomationApprovalLister(db *gorm.DB) AutomationApprovalLister {
	return &automationLister{db: db}
}

func (l *automationLister) ListPendingApprovals(
	ctx context.Context, tenantID uint64,
) ([]PendingAutomation, error) {
	if l == nil || l.db == nil {
		return nil, nil
	}
	var rows []struct {
		RunID          string
		SessionID      string
		AutomationName string
		UpdatedAt      time.Time
	}
	err := l.db.WithContext(ctx).
		Table("automation_runs AS r").
		Select("r.id AS run_id, r.session_id AS session_id, a.name AS automation_name, r.updated_at AS updated_at").
		Joins("JOIN automations AS a ON a.id = r.automation_id").
		Where("r.tenant_id = ? AND r.status = ?", tenantID, types.AutomationRunAwaitingApproval).
		Order("r.updated_at ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("读取自动化待审批失败：%w", err)
	}
	out := make([]PendingAutomation, 0, len(rows))
	for _, r := range rows {
		out = append(out, PendingAutomation{
			RunID:          r.RunID,
			AutomationName: r.AutomationName,
			SessionID:      r.SessionID,
			WaitingSince:   r.UpdatedAt,
		})
	}
	return out, nil
}
