package sop

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ---- 三源 stub ----

// aggRunRepo 只实现聚合用到的两个读方法。
type aggRunRepo struct {
	runs  []types.SopRun
	nodes map[string][]types.SopNodeRun
	err   error
}

func (r aggRunRepo) ListRuns(
	_ context.Context, tenantID uint64, f types.SopRunFilter, _, _ int,
) ([]types.SopRun, error) {
	if r.err != nil {
		return nil, r.err
	}
	var out []types.SopRun
	for _, run := range r.runs {
		if tenantID != 0 && run.TenantID != tenantID {
			continue
		}
		if f.Status != "" && run.Status != f.Status {
			continue
		}
		out = append(out, run)
	}
	return out, nil
}

func (r aggRunRepo) ListNodeRuns(_ context.Context, _ uint64, runID string) ([]types.SopNodeRun, error) {
	return r.nodes[runID], nil
}

// aggProjectRepo 按 id 返回项目名。
type aggProjectRepo struct{ names map[string]string }

func (p aggProjectRepo) GetByID(_ context.Context, _ uint64, id string) (*types.SopProject, error) {
	name, ok := p.names[id]
	if !ok {
		return nil, errRecordNotFound
	}
	return &types.SopProject{ID: id, Name: name}, nil
}

// aggItemRepo 按 id 返回事项标题。
type aggItemRepo struct{ titles map[string]string }

func (i aggItemRepo) GetByID(_ context.Context, _ uint64, id string) (*types.SopItem, error) {
	title, ok := i.titles[id]
	if !ok {
		return nil, errRecordNotFound
	}
	return &types.SopItem{ID: id, Title: title}, nil
}

// 聚合测试用的两种「失败」：查不到与读失败（不引 gorm，保持 stub 轻量）。
var (
	errRecordNotFound = errors.New("记录不存在")
	errSourceDown     = errors.New("来源不可用")
)

// aggPromptLister 返回预置挂起提示。
type aggPromptLister struct {
	prompts []*types.AgentPrompt
	err     error
}

func (l aggPromptLister) ListOpenByTenant(_ context.Context, _ uint64) ([]*types.AgentPrompt, error) {
	return l.prompts, l.err
}

// aggAutomationLister 返回预置自动化待审批。
type aggAutomationLister struct {
	items []PendingAutomation
	err   error
}

func (l aggAutomationLister) ListPendingApprovals(_ context.Context, _ uint64) ([]PendingAutomation, error) {
	return l.items, l.err
}

// newAggregateTestService 组装三源各一条的聚合服务。
func newAggregateTestService(t *testing.T) *ApprovalService {
	t.Helper()
	now := time.Now()
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Name: "方案确认", Type: types.SopNodeTypeApproval}}}
	svc := NewApprovalService(&stubApprover{}, nil).WithAggregateSources(AggregateDeps{
		Runs: aggRunRepo{
			runs: []types.SopRun{{
				ID: "run1", TenantID: 1, Status: types.SopRunWaiting,
				WorkflowSnapshot: def, ProjectID: "p1", ItemID: "i1",
			}},
			nodes: map[string][]types.SopNodeRun{"run1": {{
				TenantID: 1, RunID: "run1", NodeID: "n1", SessionID: "s-sop",
				Status: types.SopNodeWaitingApproval, GateReason: "等待确认方案",
				UpdatedAt: now.Add(-3 * time.Hour),
			}}},
		},
		Projects: aggProjectRepo{names: map[string]string{"p1": "核心项目"}},
		Items:    aggItemRepo{titles: map[string]string{"i1": "登录改造"}},
		Prompts: aggPromptLister{prompts: []*types.AgentPrompt{{
			ID: "p-1", TenantID: 1, SessionID: "s-free", Kind: types.AgentPromptKindAskUser,
			Request:   types.JSON(`{"question":"用哪个方案？"}`),
			Status:    types.AgentPromptSuspended,
			CreatedAt: now.Add(-1 * time.Hour),
		}}},
		Automations: aggAutomationLister{items: []PendingAutomation{{
			RunID: "ar1", AutomationName: "周报生成", SessionID: "s-auto", PromptID: "p-auto",
			Reason: "等待确认是否发布", WaitingSince: now.Add(-2 * time.Hour),
		}}},
	})
	return svc
}

func TestListPendingAggregatesThreeSources(t *testing.T) {
	svc := newAggregateTestService(t)
	entries, err := svc.ListPending(context.Background(), 1)
	require.NoError(t, err)
	require.Len(t, entries, 3)

	kinds := map[string]bool{}
	for _, e := range entries {
		kinds[e.Kind] = true
		switch e.Kind {
		case "sop":
			require.Equal(t, "run1", e.RunID)
			require.Equal(t, "n1", e.NodeID)
			require.Equal(t, "方案确认", e.Title, "标题取节点名")
			require.Equal(t, "核心项目", e.ProjectName)
			require.Equal(t, "登录改造", e.ItemTitle)
			require.Equal(t, "等待确认方案", e.Reason)
			require.Equal(t, []string{"approve", "reject", "rework", "terminate"}, e.Actions)
		case "agent_prompt":
			require.Equal(t, "p-1", e.RefID, "SOP 与自动化已覆盖的提示不再重复列出")
			require.Equal(t, "s-free", e.SessionID)
			require.Equal(t, "用哪个方案？", e.Title)
			require.Equal(t, []string{"answer"}, e.Actions)
		case "automation":
			require.Equal(t, "ar1", e.RefID)
			require.Equal(t, "周报生成", e.Title)
			require.Equal(t, []string{"approve", "reject"}, e.Actions)
		}
	}
	require.True(t, kinds["sop"] && kinds["agent_prompt"] && kinds["automation"],
		"三路来源必须都在")
}

func TestListPendingSortedByWaitingSince(t *testing.T) {
	svc := newAggregateTestService(t)
	entries, err := svc.ListPending(context.Background(), 1)
	require.NoError(t, err)
	require.Len(t, entries, 3)
	for i := 1; i < len(entries); i++ {
		require.False(t, entries[i].WaitingSince.Before(entries[i-1].WaitingSince),
			"按等待时长降序：等最久的在最前")
	}
	require.Equal(t, "sop", entries[0].Kind, "等 3 小时的 SOP 排第一")
	require.Equal(t, "agent_prompt", entries[2].Kind, "等 1 小时的排最后")
}

// pending_input 节点给出「回答」动作，理由走默认文案。
func TestListPendingIncludesPendingInput(t *testing.T) {
	now := time.Now()
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Name: "补充信息"}}}
	svc := NewApprovalService(&stubApprover{}, nil).WithAggregateSources(AggregateDeps{
		Runs: aggRunRepo{
			runs: []types.SopRun{{ID: "run2", TenantID: 1, Status: types.SopRunWaiting, WorkflowSnapshot: def}},
			nodes: map[string][]types.SopNodeRun{"run2": {{
				TenantID: 1, RunID: "run2", NodeID: "n1",
				Status: types.SopNodePendingInput, UpdatedAt: now,
			}}},
		},
	})
	entries, err := svc.ListPending(context.Background(), 1)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, []string{"answer"}, entries[0].Actions)
	require.Equal(t, "需要补充信息", entries[0].Reason)
}

// 单源失败不应吞掉其它源：审批中心少了自动化，也不能整页空白。
func TestListPendingToleratesSourceFailure(t *testing.T) {
	svc := newAggregateTestService(t)
	svc.aggregates.Automations = aggAutomationLister{err: errSourceDown}
	entries, err := svc.ListPending(context.Background(), 1)
	require.NoError(t, err)
	require.Len(t, entries, 2, "自动化那路读失败，SOP 与智能体挂起照常返回")
}

func TestListPendingWithoutSourcesIsEmpty(t *testing.T) {
	svc := NewApprovalService(&stubApprover{}, nil)
	entries, err := svc.ListPending(context.Background(), 1)
	require.NoError(t, err)
	require.Empty(t, entries, "未装配聚合源时返回空表，不报错")
}

// 提示的 Request 不是 JSON 或者问题为空时，标题退化为白话兜底，不让整页崩掉。
func TestListPendingPromptTitleFallback(t *testing.T) {
	svc := NewApprovalService(&stubApprover{}, nil).WithAggregateSources(AggregateDeps{
		Prompts: aggPromptLister{prompts: []*types.AgentPrompt{
			{ID: "p-1", TenantID: 1, Kind: types.AgentPromptKindToolApproval, Request: types.JSON(`not-json`)},
			{ID: "p-2", TenantID: 1, Kind: types.AgentPromptKindAskUser, Request: types.JSON(`{"question":"  "}`)},
		}},
	})
	entries, err := svc.ListPending(context.Background(), 1)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	for _, e := range entries {
		require.NotEmpty(t, e.Title, "标题必须有兜底文案")
		require.Equal(t, []string{"answer"}, e.Actions)
	}
}

// gorm 适配器：只读两路旧模块的表，不改它们的代码。
func TestAggregateAdaptersReadOldTables(t *testing.T) {
	db := newAggregateAdapterDB(t)
	ctx := context.Background()

	require.NoError(t, db.Create(&types.AgentPrompt{
		TenantID: 1, SessionID: "s1", Kind: types.AgentPromptKindAskUser,
		Request: types.JSON(`{"question":"选哪个？"}`), Status: types.AgentPromptSuspended,
	}).Error)
	require.NoError(t, db.Create(&types.AgentPrompt{
		TenantID: 1, SessionID: "s2", Kind: types.AgentPromptKindAskUser,
		Status: types.AgentPromptAnswered,
	}).Error)
	require.NoError(t, db.Create(&types.AgentPrompt{
		TenantID: 2, SessionID: "s3", Kind: types.AgentPromptKindAskUser,
		Status: types.AgentPromptSuspended,
	}).Error)

	prompts, err := NewOpenPromptLister(db).ListOpenByTenant(ctx, 1)
	require.NoError(t, err)
	require.Len(t, prompts, 1, "只取本租户、只取开放中的提示")
	require.Equal(t, "s1", prompts[0].SessionID)

	require.NoError(t, db.Create(&types.Automation{
		ID: "a1", TenantID: 1, OwnerUserID: "u1", Name: "周报生成", Instruction: "写周报",
		AccessLevel: types.AssistantAccessAutoWrite,
	}).Error)
	// (automation_id, scheduled_at, trigger_kind) 有唯一索引：两条运行要给不同的槽位。
	require.NoError(t, db.Create(&types.AutomationRun{
		ID: "ar1", AutomationID: "a1", TenantID: 1, Status: types.AutomationRunAwaitingApproval,
		ScheduledAt: time.Now().Add(-time.Hour), TriggerKind: "schedule",
	}).Error)
	require.NoError(t, db.Create(&types.AutomationRun{
		ID: "ar2", AutomationID: "a1", TenantID: 1, Status: types.AutomationRunRunning,
		ScheduledAt: time.Now(), TriggerKind: "manual",
	}).Error)

	items, err := NewAutomationApprovalLister(db).ListPendingApprovals(ctx, 1)
	require.NoError(t, err)
	require.Len(t, items, 1, "只取 awaiting_approval")
	require.Equal(t, "ar1", items[0].RunID)
	require.Equal(t, "周报生成", items[0].AutomationName, "连表取自动化名")
}

func newAggregateAdapterDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&types.AgentPrompt{}, &types.Automation{}, &types.AutomationRun{}))
	return db
}

// 同一件事只出现一次：SOP 节点会话上的开放提示、自动化运行挂着的提示，
// 都不再以 agent_prompt 身份重复列出。
func TestListPendingDedupesPromptsCoveredByRuns(t *testing.T) {
	now := time.Now()
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Name: "确认"}}}
	svc := NewApprovalService(&stubApprover{}, nil).WithAggregateSources(AggregateDeps{
		Runs: aggRunRepo{
			runs: []types.SopRun{{ID: "run1", TenantID: 1, Status: types.SopRunWaiting, WorkflowSnapshot: def}},
			nodes: map[string][]types.SopNodeRun{"run1": {{
				TenantID: 1, RunID: "run1", NodeID: "n1", SessionID: "s-sop",
				Status: types.SopNodePendingInput, UpdatedAt: now,
			}}},
		},
		Prompts: aggPromptLister{prompts: []*types.AgentPrompt{
			{ID: "p-sop", TenantID: 1, SessionID: "s-sop", Kind: types.AgentPromptKindAskUser, CreatedAt: now},
			{ID: "p-auto", TenantID: 1, SessionID: "s-auto", Kind: types.AgentPromptKindToolApproval, CreatedAt: now},
			{ID: "p-free", TenantID: 1, SessionID: "s-free", Kind: types.AgentPromptKindAskUser, CreatedAt: now},
		}},
		Automations: aggAutomationLister{items: []PendingAutomation{{
			RunID: "ar1", AutomationName: "自动化", SessionID: "s-auto", PromptID: "p-auto", WaitingSince: now,
		}}},
	})
	entries, err := svc.ListPending(context.Background(), 1)
	require.NoError(t, err)

	var promptIDs []string
	for _, e := range entries {
		if e.Kind == ApprovalKindAgentPrompt {
			promptIDs = append(promptIDs, e.RefID)
		}
	}
	require.Equal(t, []string{"p-free"}, promptIDs,
		"被 SOP 会话与自动化运行覆盖的提示不再以第三行出现")
}

func TestListPendingRejectsZeroTenant(t *testing.T) {
	svc := newAggregateTestService(t)
	_, err := svc.ListPending(context.Background(), 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "租户")
}

// 旧模块口径：工单审批与 MCP 授权没配问题文本时，标题要能说出「谁在请求」。
func TestListPendingToolApprovalTitle(t *testing.T) {
	svc := NewApprovalService(&stubApprover{}, nil).WithAggregateSources(AggregateDeps{
		Prompts: aggPromptLister{prompts: []*types.AgentPrompt{
			{ID: "p-1", TenantID: 1, Kind: types.AgentPromptKindToolApproval,
				Request: types.JSON(`{"service_name":"GitHub","mcp_tool_name":"create_issue"}`)},
			{ID: "p-2", TenantID: 1, Kind: types.AgentPromptKindMCPOAuth,
				Request: types.JSON(`{"service_name":"飞书"}`)},
		}},
	})
	entries, err := svc.ListPending(context.Background(), 1)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	titles := []string{entries[0].Title, entries[1].Title}
	require.Contains(t, titles, "GitHub 调用待确认")
	require.Contains(t, titles, "飞书 授权待确认")
}
