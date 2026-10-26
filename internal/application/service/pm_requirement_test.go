package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// 状态变更必须过状态机，非法迁移不得落库
func TestUpdateRequirementStatusRejectsIllegal(t *testing.T) {
	repo := newFakeRequirementRepo(&types.PMRequirement{
		ID: "r-1", TenantID: 1, Status: types.RequirementStatusPendingAnalysis,
	})
	svc := NewPMRequirementService(repo, nil, nil, nil, nil, nil)
	ctx := context.Background()

	err := svc.UpdateStatus(ctx, 1, "r-1", types.RequirementStatusLaunched)
	assert.ErrorIs(t, err, ErrIllegalRequirementTransition)
	assert.Equal(t, types.RequirementStatusPendingAnalysis, repo.items["r-1"].Status)

	// 合法迁移正常落库
	require.NoError(t, svc.UpdateStatus(ctx, 1, "r-1", types.RequirementStatusConfirmed))
	assert.Equal(t, types.RequirementStatusConfirmed, repo.items["r-1"].Status)
}

// 上线验证回填：只允许在 launched 状态执行；重复回填覆盖旧值并刷新 value_achieved_at
func TestLaunchVerificationOnlyWhenLaunched(t *testing.T) {
	repo := newFakeRequirementRepo(&types.PMRequirement{
		ID: "r-1", TenantID: 1, Status: types.RequirementStatusDeveloping,
	})
	svc := NewPMRequirementService(repo, nil, nil, nil, nil, nil)
	ctx := context.Background()
	in := types.LaunchVerificationInput{
		ValueAchieved: types.RequirementValueAchieved,
		EffectNotes:   "转化率提升 12%",
		RetroNotes:    "上线节奏控制得当",
	}

	_, err := svc.VerifyLaunch(ctx, 1, "r-1", in)
	assert.ErrorIs(t, err, ErrRequirementNotLaunched)
	assert.Nil(t, repo.items["r-1"].ValueAchievedAt)
	assert.Empty(t, repo.items["r-1"].EffectNotes)

	repo.items["r-1"].Status = types.RequirementStatusLaunched
	got, err := svc.VerifyLaunch(ctx, 1, "r-1", in)
	require.NoError(t, err)
	assert.Equal(t, types.RequirementValueAchieved, got.ValueAchieved)
	assert.Equal(t, "转化率提升 12%", got.EffectNotes)
	assert.Equal(t, "上线节奏控制得当", got.RetroNotes)
	require.NotNil(t, got.ValueAchievedAt)
	first := *got.ValueAchievedAt

	// 重复回填视为修正：覆盖旧值并刷新回填时间
	time.Sleep(10 * time.Millisecond)
	got, err = svc.VerifyLaunch(ctx, 1, "r-1", types.LaunchVerificationInput{
		ValueAchieved: types.RequirementValuePartial,
		EffectNotes:   "转化率提升 5%",
		RetroNotes:    "受排期影响未达预期",
	})
	require.NoError(t, err)
	assert.Equal(t, types.RequirementValuePartial, got.ValueAchieved)
	assert.Equal(t, "转化率提升 5%", got.EffectNotes)
	assert.Equal(t, "受排期影响未达预期", got.RetroNotes)
	assert.True(t, got.ValueAchievedAt.After(first))
}

// 回填值合法性与必填校验：枚举 + 单字段长度上限
func TestLaunchVerificationValidation(t *testing.T) {
	repo := newFakeRequirementRepo(&types.PMRequirement{
		ID: "r-1", TenantID: 1, Status: types.RequirementStatusLaunched,
	})
	svc := NewPMRequirementService(repo, nil, nil, nil, nil, nil)
	ctx := context.Background()

	// value_achieved 必填且必须是合法枚举
	_, err := svc.VerifyLaunch(ctx, 1, "r-1", types.LaunchVerificationInput{})
	assert.ErrorIs(t, err, ErrRequirementValueInvalid)
	_, err = svc.VerifyLaunch(ctx, 1, "r-1", types.LaunchVerificationInput{ValueAchieved: "unknown"})
	assert.ErrorIs(t, err, ErrRequirementValueInvalid)
	assert.Nil(t, repo.items["r-1"].ValueAchievedAt)

	// effect_notes / retro_notes 上限 2000 字符
	svcInput := func(effect, retro string) types.LaunchVerificationInput {
		return types.LaunchVerificationInput{
			ValueAchieved: types.RequirementValueAchieved, EffectNotes: effect, RetroNotes: retro,
		}
	}
	_, err = svc.VerifyLaunch(ctx, 1, "r-1", svcInput(strings.Repeat("效", 2001), ""))
	assert.ErrorIs(t, err, ErrRequirementNoteTooLong)
	_, err = svc.VerifyLaunch(ctx, 1, "r-1", svcInput("", strings.Repeat("复", 2001)))
	assert.ErrorIs(t, err, ErrRequirementNoteTooLong)

	// 边界值 2000 允许写入
	_, err = svc.VerifyLaunch(ctx, 1, "r-1", svcInput(strings.Repeat("效", 2000), strings.Repeat("复", 2000)))
	require.NoError(t, err)
	assert.Len(t, []rune(repo.items["r-1"].EffectNotes), 2000)
}

// 重新激活：仅 rejected / closed 可调用，重置为 pending_analysis 且清空 ai_analysis
func TestReactivateOnlyFromTerminal(t *testing.T) {
	repo := newFakeRequirementRepo(&types.PMRequirement{
		ID: "r-1", TenantID: 1, Status: types.RequirementStatusDeveloping,
		AIAnalysis: types.JSON(`{"value":"factor"}`), DesignPlan: "旧方案",
	})
	svc := NewPMRequirementService(repo, nil, nil, nil, nil, nil)
	ctx := context.Background()

	assert.ErrorIs(t, svc.Reactivate(ctx, 1, "r-1"), ErrRequirementNotReactivable)
	assert.Equal(t, types.RequirementStatusDeveloping, repo.items["r-1"].Status)

	repo.items["r-1"].Status = types.RequirementStatusRejected
	require.NoError(t, svc.Reactivate(ctx, 1, "r-1"))
	got := repo.items["r-1"]
	assert.Equal(t, types.RequirementStatusPendingAnalysis, got.Status)
	assert.Nil(t, got.AIAnalysis)
	assert.Empty(t, got.DesignPlan)
	assert.Empty(t, got.SessionID)
}

// 删除需求：存在则删除；不存在或跨租户返回 not found
func TestDeleteRequirement(t *testing.T) {
	repo := newFakeRequirementRepo(&types.PMRequirement{
		ID: "r-1", TenantID: 1, ProjectID: "p-1", Status: types.RequirementStatusConfirmed,
	})
	svc := NewPMRequirementService(repo, nil, nil, nil, nil, nil)
	ctx := context.Background()

	assert.ErrorIs(t, svc.DeleteRequirement(ctx, 2, "r-1"), repository.ErrPMRequirementNotFound)
	assert.ErrorIs(t, svc.DeleteRequirement(ctx, 1, "missing"), repository.ErrPMRequirementNotFound)

	require.NoError(t, svc.DeleteRequirement(ctx, 1, "r-1"))
	assert.Empty(t, repo.items)
}

// AI 分析编排：把路线图上下文 + 需求初稿拼成 prompt，返回 session_id 供前端 SSE 订阅；
// 分析仅作辅助，不改变需求状态。
func TestStartAnalysisCreatesSessionWithoutStatusChange(t *testing.T) {
	repo := newFakeRequirementRepo(&types.PMRequirement{
		ID: "r-1", TenantID: 1, ProjectID: "p-1",
		Title: "报表导出", Description: "支持按条件导出 CSV", Priority: types.RequirementPriorityP1,
		Status: types.RequirementStatusPendingAnalysis,
	})
	batches := newFakePMBatchRepo(&types.PMPlanBatch{
		ID: "b-1", TenantID: 1, ProjectID: "p-1", Status: types.PlanBatchStatusActive,
		SnapshotKnowledgeID: "k-snap",
		Draft:               types.JSON(`{"features":[{"module_name":"报表","feature_name":"导出","operation_name":"CSV 导出"}]}`),
	})
	sessions := &fakePMSession{}
	svc := NewPMRequirementService(repo, sessions, batches, nil, nil, nil)
	ctx := context.Background()

	sessionID, err := svc.StartAnalysis(ctx, 1, "r-1")
	require.NoError(t, err)
	assert.Equal(t, "sess-1", sessionID)

	got := repo.items["r-1"]
	assert.Equal(t, types.RequirementStatusPendingAnalysis, got.Status)
	assert.Equal(t, "sess-1", got.SessionID)
	require.Len(t, sessions.created, 1)

	// prompt 同时包含需求初稿与路线图快照上下文
	prompt := sessions.created[0].Description
	// 会话描述带 pm_analysis: 前缀，使其不出现在首页会话列表
	assert.True(t, types.IsPMAnalysisSessionDescription(prompt))
	assert.Contains(t, prompt, "报表导出")
	assert.Contains(t, prompt, "CSV 导出")
	assert.Contains(t, prompt, "k-snap")
	// 分析结论不再包含「建议状态」
	assert.NotContains(t, prompt, "建议状态")
}

// AI 分析：终止态（已驳回/已关闭）不再提供分析入口
func TestStartAnalysisRejectedInTerminalStatus(t *testing.T) {
	repo := newFakeRequirementRepo(&types.PMRequirement{
		ID: "r-1", TenantID: 1, Status: types.RequirementStatusRejected,
	})
	svc := NewPMRequirementService(repo, &fakePMSession{}, nil, nil, nil, nil)

	_, err := svc.StartAnalysis(context.Background(), 1, "r-1")
	assert.ErrorIs(t, err, ErrRequirementAnalysisState)
}

// AI 分析结果回写：不再限制需求状态（分析仅作辅助）
func TestSaveAnalysisAllowsAnyStatus(t *testing.T) {
	repo := newFakeRequirementRepo(&types.PMRequirement{
		ID: "r-1", TenantID: 1, Status: types.RequirementStatusConfirmed,
	})
	svc := NewPMRequirementService(repo, nil, nil, nil, nil, nil)

	err := svc.SaveAnalysis(
		context.Background(), 1, "r-1", types.JSON(`{"value":"留存提升"}`), "方案",
	)
	require.NoError(t, err)
	got := repo.items["r-1"]
	assert.Equal(t, types.RequirementStatusConfirmed, got.Status)
	assert.Equal(t, "方案", got.DesignPlan)
}

// ---- 测试替身：需求仓储 ----

var _ interfaces.PMRequirementRepository = (*fakeRequirementRepo)(nil)

type fakeRequirementRepo struct {
	items map[string]*types.PMRequirement
}

func newFakeRequirementRepo(reqs ...*types.PMRequirement) *fakeRequirementRepo {
	r := &fakeRequirementRepo{items: make(map[string]*types.PMRequirement, len(reqs))}
	for _, req := range reqs {
		r.items[req.ID] = req
	}
	return r
}

func (f *fakeRequirementRepo) Create(_ context.Context, r *types.PMRequirement) error {
	if r.ID == "" {
		r.ID = "r-new"
	}
	f.items[r.ID] = r
	return nil
}

func (f *fakeRequirementRepo) GetByID(
	_ context.Context, tenantID uint64, id string,
) (*types.PMRequirement, error) {
	r, ok := f.items[id]
	if !ok || r.TenantID != tenantID {
		return nil, repository.ErrPMRequirementNotFound
	}
	cp := *r
	return &cp, nil
}

func (f *fakeRequirementRepo) List(
	_ context.Context, tenantID uint64, projectID string, status string,
) ([]*types.PMRequirement, error) {
	var list []*types.PMRequirement
	for _, r := range f.items {
		if r.TenantID != tenantID || r.ProjectID != projectID {
			continue
		}
		if status != "" && r.Status != status {
			continue
		}
		list = append(list, r)
	}
	return list, nil
}

func (f *fakeRequirementRepo) ListByIDs(
	_ context.Context, tenantID uint64, ids []string,
) ([]*types.PMRequirement, error) {
	var list []*types.PMRequirement
	for _, id := range ids {
		if r, ok := f.items[id]; ok && r.TenantID == tenantID {
			list = append(list, r)
		}
	}
	return list, nil
}

func (f *fakeRequirementRepo) Update(_ context.Context, r *types.PMRequirement) error {
	if _, ok := f.items[r.ID]; !ok {
		return repository.ErrPMRequirementNotFound
	}
	cp := *r
	f.items[r.ID] = &cp
	return nil
}

func (f *fakeRequirementRepo) Delete(_ context.Context, tenantID uint64, id string) error {
	r, ok := f.items[id]
	if !ok || r.TenantID != tenantID {
		return repository.ErrPMRequirementNotFound
	}
	delete(f.items, id)
	return nil
}
