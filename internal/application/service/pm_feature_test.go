package service

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// 路线图同步：按「模块/功能/操作」三级路径 upsert，重复生成不产生重复节点
func TestSyncFromRoadmapIdempotent(t *testing.T) {
	repo := newFakeFeatureRepo()
	svc := NewPMFeatureService(repo, nil)
	ctx := context.Background()
	items := []types.PMFeature{
		{ModuleName: "报表中心", FeatureName: "数据导出", OperationName: "CSV 导出"},
		{ModuleName: "报表中心", FeatureName: "数据导出", OperationName: "Excel 导出"},
		{ModuleName: "报表中心", FeatureName: "筛选"},
	}

	require.NoError(t, svc.SyncFromRoadmap(ctx, 1, "p-1", items))
	assert.Len(t, repo.items, 5) // 模块 + 功能 + 2 操作 + 1 二级功能

	// 记录首次同步的节点 ID，二次同步必须复用
	ids := make(map[string]string, len(repo.items))
	for key, f := range repo.items {
		ids[key] = f.ID
	}
	require.NoError(t, svc.SyncFromRoadmap(ctx, 1, "p-1", items))
	assert.Len(t, repo.items, 5)
	for key, id := range ids {
		assert.Equal(t, id, repo.items[key].ID)
	}

	// 三级父子关系正确
	module := featureByPath(t, repo, "报表中心", "", "")
	feature := featureByPath(t, repo, "报表中心", "数据导出", "")
	op := featureByPath(t, repo, "报表中心", "数据导出", "CSV 导出")
	assert.Empty(t, module.ParentID)
	assert.Equal(t, module.ID, feature.ParentID)
	assert.Equal(t, feature.ID, op.ParentID)
	assert.Equal(t, types.FeatureSourceRoadmap, op.Source)
}

// PRD 同步：新增 → add；归属版本变化 → change；从本版本 PRD 消失 → remove
func TestSyncFromPRDRecordsIteration(t *testing.T) {
	repo := newFakeFeatureRepo()
	repo.planDates["v-1"] = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	repo.planDates["v-2"] = time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	svc := NewPMFeatureService(repo, nil)
	ctx := context.Background()

	csv := types.PMFeature{ModuleName: "报表中心", FeatureName: "数据导出", OperationName: "CSV 导出"}
	pdf := types.PMFeature{ModuleName: "报表中心", FeatureName: "数据导出", OperationName: "PDF 导出"}
	excel := types.PMFeature{ModuleName: "报表中心", FeatureName: "数据导出", OperationName: "Excel 导出"}

	// v1：CSV / PDF 新增
	require.NoError(t, svc.SyncFromPRD(ctx, 1, "p-1", "v-1", []types.PMFeature{csv, pdf}))
	assert.Equal(t, []string{types.FeatureChangeAdd},
		iterationTypes(repo, featureByPath(t, repo, "报表中心", "数据导出", "CSV 导出")))

	// v2：CSV 归属变化 → change；Excel 新增 → add；PDF 仍属 v1，不产生 remove
	require.NoError(t, svc.SyncFromPRD(ctx, 1, "p-1", "v-2", []types.PMFeature{csv, excel}))
	csvNode := featureByPath(t, repo, "报表中心", "数据导出", "CSV 导出")
	assert.Equal(t, []string{types.FeatureChangeAdd, types.FeatureChangeChange}, iterationTypes(repo, csvNode))
	assert.Equal(t, "v-2", lastIteration(repo, csvNode).VersionID)
	assert.Equal(t, []string{types.FeatureChangeAdd},
		iterationTypes(repo, featureByPath(t, repo, "报表中心", "数据导出", "Excel 导出")))
	assert.Equal(t, []string{types.FeatureChangeAdd},
		iterationTypes(repo, featureByPath(t, repo, "报表中心", "数据导出", "PDF 导出")))

	// v2 再次同步且缺 Excel → Excel 从 v2 移除
	require.NoError(t, svc.SyncFromPRD(ctx, 1, "p-1", "v-2", []types.PMFeature{csv}))
	excelNode := featureByPath(t, repo, "报表中心", "数据导出", "Excel 导出")
	assert.Equal(t, []string{types.FeatureChangeAdd, types.FeatureChangeRemove}, iterationTypes(repo, excelNode))
}

// 人工补充：source=manual，可选关联版本 → 同时写 add 迭代
func TestCreateManualFeature(t *testing.T) {
	repo := newFakeFeatureRepo()
	svc := NewPMFeatureService(repo, nil)
	ctx := context.Background()

	got, err := svc.CreateManual(ctx, 1, types.CreatePMFeatureInput{
		Feature:   &types.PMFeature{ProjectID: "p-1", ModuleName: "报表中心", FeatureName: "权限控制"},
		VersionID: "v-1",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, got.ID)
	assert.Equal(t, uint64(1), got.TenantID)
	assert.Equal(t, types.FeatureSourceManual, got.Source)
	assert.Equal(t, types.FeatureStatusPlanned, got.Status)
	assert.Equal(t, []string{types.FeatureChangeAdd}, iterationTypes(repo, got))

	// 不关联版本时不写迭代
	second, err := svc.CreateManual(ctx, 1, types.CreatePMFeatureInput{
		Feature: &types.PMFeature{ProjectID: "p-1", ModuleName: "报表中心", FeatureName: "水印"},
	})
	require.NoError(t, err)
	assert.Empty(t, repo.iterations[second.ID])
}

// 迭代历史排序：按版本 plan_date 升序，无版本记录排在最后
func TestListIterationsOrderedByVersionPlanDate(t *testing.T) {
	repo := newFakeFeatureRepo()
	repo.planDates["v-1"] = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	repo.planDates["v-2"] = time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	// 故意乱序写入，验证按计划日排序
	repo.iterations["f-1"] = []*types.PMFeatureIteration{
		{ID: "it-3", FeatureID: "f-1", VersionID: "v-2", ChangeType: types.FeatureChangeChange},
		{ID: "it-2", FeatureID: "f-1", ChangeType: types.FeatureChangeAdd},
		{ID: "it-1", FeatureID: "f-1", VersionID: "v-1", ChangeType: types.FeatureChangeAdd},
	}
	svc := NewPMFeatureService(repo, nil)

	got, err := svc.ListIterations(context.Background(), "f-1")
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, "v-1", got[0].VersionID)
	assert.Equal(t, "v-2", got[1].VersionID)
	assert.Equal(t, "", got[2].VersionID) // 无版本记录排最后
}

// ---- 测试辅助 ----

func featureByPath(
	t *testing.T, repo *fakeFeatureRepo, module, feature, operation string,
) *types.PMFeature {
	t.Helper()
	for _, f := range repo.items {
		if f.ModuleName == module && f.FeatureName == feature && f.OperationName == operation {
			return f
		}
	}
	t.Fatalf("feature not found: %s/%s/%s", module, feature, operation)
	return nil
}

func iterationTypes(repo *fakeFeatureRepo, f *types.PMFeature) []string {
	list := repo.iterations[f.ID]
	out := make([]string, 0, len(list))
	for _, it := range list {
		out = append(out, it.ChangeType)
	}
	return out
}

func lastIteration(repo *fakeFeatureRepo, f *types.PMFeature) *types.PMFeatureIteration {
	list := repo.iterations[f.ID]
	if len(list) == 0 {
		return nil
	}
	return list[len(list)-1]
}

// ---- 测试替身：功能清单仓储 ----

var _ interfaces.PMFeatureRepository = (*fakeFeatureRepo)(nil)

type fakeFeatureRepo struct {
	items      map[string]*types.PMFeature
	iterations map[string][]*types.PMFeatureIteration
	planDates  map[string]time.Time
	seq        int
}

func newFakeFeatureRepo() *fakeFeatureRepo {
	return &fakeFeatureRepo{
		items:      make(map[string]*types.PMFeature),
		iterations: make(map[string][]*types.PMFeatureIteration),
		planDates:  make(map[string]time.Time),
	}
}

func (f *fakeFeatureRepo) Create(_ context.Context, feature *types.PMFeature) error {
	if feature.ID == "" {
		f.seq++
		feature.ID = fmt.Sprintf("f-%d", f.seq)
	}
	cp := *feature
	f.items[feature.ID] = &cp
	return nil
}

func (f *fakeFeatureRepo) CreateBatch(ctx context.Context, fs []*types.PMFeature) error {
	for _, feature := range fs {
		if err := f.Create(ctx, feature); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeFeatureRepo) GetByID(
	_ context.Context, tenantID uint64, id string,
) (*types.PMFeature, error) {
	feature, ok := f.items[id]
	if !ok || feature.TenantID != tenantID {
		return nil, repository.ErrPMFeatureNotFound
	}
	cp := *feature
	return &cp, nil
}

func (f *fakeFeatureRepo) ListByProject(
	_ context.Context, tenantID uint64, projectID string,
) ([]*types.PMFeature, error) {
	var list []*types.PMFeature
	for _, feature := range f.items {
		if feature.TenantID == tenantID && feature.ProjectID == projectID {
			list = append(list, feature)
		}
	}
	return list, nil
}

func (f *fakeFeatureRepo) Update(_ context.Context, feature *types.PMFeature) error {
	if _, ok := f.items[feature.ID]; !ok {
		return repository.ErrPMFeatureNotFound
	}
	cp := *feature
	f.items[feature.ID] = &cp
	return nil
}

func (f *fakeFeatureRepo) CreateIteration(
	_ context.Context, it *types.PMFeatureIteration,
) error {
	if it.ID == "" {
		it.ID = fmt.Sprintf("it-%d", len(f.iterations[it.FeatureID])+1)
	}
	f.iterations[it.FeatureID] = append(f.iterations[it.FeatureID], it)
	return nil
}

// ListIterations 模拟 DB 契约：按版本计划日升序，无版本记录排最后
func (f *fakeFeatureRepo) ListIterations(
	_ context.Context, featureID string,
) ([]*types.PMFeatureIteration, error) {
	out := append([]*types.PMFeatureIteration(nil), f.iterations[featureID]...)
	sort.SliceStable(out, func(i, j int) bool {
		di, oki := f.planDates[out[i].VersionID]
		dj, okj := f.planDates[out[j].VersionID]
		if !oki {
			return false
		}
		if !okj {
			return true
		}
		return di.Before(dj)
	})
	return out, nil
}
