package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// 三种规划的合并语义（方案 §6.3）
func TestMergeRoadmap(t *testing.T) {
	existing := []types.PMFeature{
		{ID: "f1", ModuleName: "账号", FeatureName: "登录", OperationName: "短信登录"},
		{ID: "f2", ModuleName: "账号", FeatureName: "登录", OperationName: "密码登录"},
	}
	draft := []types.PMFeature{
		{ModuleName: "账号", FeatureName: "登录", OperationName: "密码登录"}, // 已存在
		{ModuleName: "账号", FeatureName: "登录", OperationName: "扫码登录"}, // 新增
	}

	// 增量规划：保留既有 + 追加新增
	got, err := MergeRoadmap(existing, draft, types.PlanTypeIteration)
	require.NoError(t, err)
	assert.Len(t, got.Added, 1)
	assert.Equal(t, "扫码登录", got.Added[0].OperationName)
	assert.Empty(t, got.Removed) // 增量不删除
	assert.Len(t, got.Kept, len(existing))

	// 重新规划：以草稿为准，未出现在草稿中的既有项标记移除
	got, err = MergeRoadmap(existing, draft, types.PlanTypeReplan)
	require.NoError(t, err)
	assert.Len(t, got.Removed, 1) // 短信登录被移除
	assert.Equal(t, "短信登录", got.Removed[0].OperationName)
	assert.Equal(t, "f2", got.Kept[0].ID) // 命中既有项时沿用原 ID

	// 首期规划：既有必须为空，否则报错，避免误覆盖
	_, err = MergeRoadmap(existing, draft, types.PlanTypeInitial)
	assert.ErrorIs(t, err, ErrRoadmapExistingNotEmpty)

	got, err = MergeRoadmap(nil, draft, types.PlanTypeInitial)
	require.NoError(t, err)
	assert.Len(t, got.Added, len(draft))
	assert.Empty(t, got.Removed)
}

// 草案校验：三级结构必填、同一操作不得重复
func TestValidateRoadmapDraft(t *testing.T) {
	ok := []types.PMFeature{{ModuleName: "账号", FeatureName: "登录", OperationName: "短信登录"}}
	assert.NoError(t, ValidateRoadmapDraft(ok))

	missing := []types.PMFeature{{ModuleName: "账号", FeatureName: "", OperationName: "短信登录"}}
	assert.ErrorIs(t, ValidateRoadmapDraft(missing), ErrRoadmapDraftInvalid)

	dup := append(ok, ok[0])
	assert.ErrorIs(t, ValidateRoadmapDraft(dup), ErrRoadmapDraftDuplicate)

	assert.ErrorIs(t, ValidateRoadmapDraft(nil), ErrRoadmapDraftEmpty)
}

// 生效：旧 active 批次转 archived，新批次 active，并写快照指针
func TestActivateBatchArchivesPrevious(t *testing.T) {
	repo := newFakePMBatchRepo(
		&types.PMPlanBatch{
			ID: "b1", TenantID: 1, ProjectID: "p1",
			PlanType: types.PlanTypeInitial, Status: types.PlanBatchStatusActive,
			RoadmapKnowledgeID: "k-old",
		},
		&types.PMPlanBatch{
			ID: "b2", TenantID: 1, ProjectID: "p1",
			PlanType: types.PlanTypeIteration, Status: types.PlanBatchStatusDraft,
			RoadmapKnowledgeID: "k-new", // 已生成但未单独落快照
		},
	)
	svc := NewPMRoadmapService(PMRoadmapDeps{BatchRepo: repo})

	require.NoError(t, svc.ActivateBatch(context.Background(), 1, "b2", "u1"))

	assert.Equal(t, types.PlanBatchStatusArchived, repo.items["b1"].Status)
	assert.Equal(t, types.PlanBatchStatusActive, repo.items["b2"].Status)
	assert.Equal(t, "k-new", repo.items["b2"].SnapshotKnowledgeID) // 缺失快照时回落到路线图文档
	assert.Equal(t, "u1", repo.items["b2"].OperatorID)
	assert.NotNil(t, repo.items["b2"].SnapshotAt)
	assert.Equal(t, 1, repo.activateCalls)

	// 未生成路线图的批次不允许生效
	repo.items["b3"] = &types.PMPlanBatch{
		ID: "b3", TenantID: 1, ProjectID: "p1",
		PlanType: types.PlanTypeIteration, Status: types.PlanBatchStatusDraft,
	}
	assert.ErrorIs(t, svc.ActivateBatch(context.Background(), 1, "b3", "u1"), ErrRoadmapNotGenerated)
}

// 删除：仅草稿批次可删，生效/归档批次拒绝
func TestDeleteBatchOnlyDraft(t *testing.T) {
	repo := newFakePMBatchRepo(
		&types.PMPlanBatch{ID: "d1", TenantID: 1, ProjectID: "p1", Status: types.PlanBatchStatusDraft},
		&types.PMPlanBatch{ID: "a1", TenantID: 1, ProjectID: "p1", Status: types.PlanBatchStatusActive},
		&types.PMPlanBatch{ID: "r1", TenantID: 1, ProjectID: "p1", Status: types.PlanBatchStatusArchived},
	)
	svc := NewPMRoadmapService(PMRoadmapDeps{BatchRepo: repo})
	ctx := context.Background()

	require.NoError(t, svc.DeleteBatch(ctx, 1, "d1"))
	_, ok := repo.items["d1"]
	assert.False(t, ok, "草稿批次应被删除")

	assert.ErrorIs(t, svc.DeleteBatch(ctx, 1, "a1"), ErrRoadmapBatchNotEditable)
	assert.ErrorIs(t, svc.DeleteBatch(ctx, 1, "r1"), ErrRoadmapBatchNotEditable)
	assert.ErrorIs(t, svc.DeleteBatch(ctx, 1, "absent"), repository.ErrPMPlanBatchNotFound)
}

// ---- 测试替身：规划批次仓储 ----

var _ interfaces.PMPlanBatchRepository = (*fakePMBatchRepo)(nil)

type fakePMBatchRepo struct {
	items         map[string]*types.PMPlanBatch
	activateCalls int
}

func newFakePMBatchRepo(batches ...*types.PMPlanBatch) *fakePMBatchRepo {
	r := &fakePMBatchRepo{items: make(map[string]*types.PMPlanBatch, len(batches))}
	for _, b := range batches {
		r.items[b.ID] = b
	}
	return r
}

func (f *fakePMBatchRepo) Create(_ context.Context, b *types.PMPlanBatch) error {
	f.items[b.ID] = b
	return nil
}

func (f *fakePMBatchRepo) GetByID(
	_ context.Context, tenantID uint64, id string,
) (*types.PMPlanBatch, error) {
	b, ok := f.items[id]
	if !ok || b.TenantID != tenantID {
		return nil, repository.ErrPMPlanBatchNotFound
	}
	cp := *b
	return &cp, nil
}

func (f *fakePMBatchRepo) ListByProject(
	_ context.Context, tenantID uint64, projectID string,
) ([]*types.PMPlanBatch, error) {
	var list []*types.PMPlanBatch
	for _, b := range f.items {
		if b.TenantID == tenantID && b.ProjectID == projectID {
			list = append(list, b)
		}
	}
	return list, nil
}

func (f *fakePMBatchRepo) Update(_ context.Context, b *types.PMPlanBatch) error {
	if _, ok := f.items[b.ID]; !ok {
		return repository.ErrPMPlanBatchNotFound
	}
	cp := *b
	f.items[b.ID] = &cp
	return nil
}

func (f *fakePMBatchRepo) GetActiveByProject(
	_ context.Context, tenantID uint64, projectID string,
) (*types.PMPlanBatch, error) {
	for _, b := range f.items {
		if b.TenantID == tenantID && b.ProjectID == projectID &&
			b.Status == types.PlanBatchStatusActive {
			cp := *b
			return &cp, nil
		}
	}
	return nil, repository.ErrPMPlanBatchNotFound
}

// ActivateBatch 与真实仓储语义一致：同项目旧 active 归档 + 目标批次生效
func (f *fakePMBatchRepo) ActivateBatch(
	_ context.Context, tenantID uint64, projectID string, batchID string,
) error {
	f.activateCalls++
	target, ok := f.items[batchID]
	if !ok || target.TenantID != tenantID || target.ProjectID != projectID {
		return repository.ErrPMPlanBatchNotFound
	}
	for _, b := range f.items {
		if b.TenantID == tenantID && b.ProjectID == projectID &&
			b.Status == types.PlanBatchStatusActive && b.ID != batchID {
			b.Status = types.PlanBatchStatusArchived
		}
	}
	now := time.Now()
	target.Status = types.PlanBatchStatusActive
	target.ActivatedAt = &now
	return nil
}

func (f *fakePMBatchRepo) Delete(_ context.Context, tenantID uint64, id string) error {
	b, ok := f.items[id]
	if !ok || b.TenantID != tenantID {
		return repository.ErrPMPlanBatchNotFound
	}
	delete(f.items, id)
	return nil
}
