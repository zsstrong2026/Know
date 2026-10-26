package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// 定稿前置校验：最近一轮评审结论必须是 pass / conditional
func TestFinalizeRequiresPassConclusion(t *testing.T) {
	versions := newFakeVersionRepo(&types.PMVersion{
		ID: "v-1", TenantID: 1, ProjectID: "p-1",
		Status: types.VersionStatusReviewing,
	})
	svc := NewPMVersionService(versions, nil)
	ctx := context.Background()

	// 未评审：轮次 0 → 拒绝
	_, err := svc.Finalize(ctx, 1, "v-1")
	assert.ErrorIs(t, err, ErrVersionNotApproved)

	// 最近一轮 reject → 拒绝
	versions.items["v-1"].ReviewRound = 1
	versions.items["v-1"].ReviewConclusion = types.VersionConclusionReject
	_, err = svc.Finalize(ctx, 1, "v-1")
	assert.ErrorIs(t, err, ErrVersionNotApproved)

	// conditional → 通过，版本号生成、finalize_date 写入
	versions.items["v-1"].ReviewConclusion = types.VersionConclusionConditional
	got, err := svc.Finalize(ctx, 1, "v-1")
	require.NoError(t, err)
	assert.Equal(t, types.VersionStatusFinalized, got.Status)
	assert.Equal(t, "1.0.0", got.VersionNo)
	assert.NotNil(t, got.FinalizeDate)
}

// 定稿生成版本号：已有历史版本时按 minor 递增
func TestFinalizeAssignsVersionNo(t *testing.T) {
	versions := newFakeVersionRepo(
		&types.PMVersion{
			ID: "v-old", TenantID: 1, ProjectID: "p-1",
			VersionNo: "1.0.0", Status: types.VersionStatusLaunched,
		},
		&types.PMVersion{
			ID: "v-new", TenantID: 1, ProjectID: "p-1",
			Status: types.VersionStatusReviewing, ReviewRound: 1,
			ReviewConclusion: types.VersionConclusionPass,
		},
	)
	svc := NewPMVersionService(versions, nil)
	ctx := context.Background()

	got, err := svc.Finalize(ctx, 1, "v-new")
	require.NoError(t, err)
	assert.Equal(t, "1.1.0", got.VersionNo)

	// 重复定稿幂等：编号不变，不再触发更新
	updates := versions.updates
	again, err := svc.Finalize(ctx, 1, "v-new")
	require.NoError(t, err)
	assert.Equal(t, "1.1.0", again.VersionNo)
	assert.Equal(t, updates, versions.updates)
}

// 状态联动：版本 → 需求集合状态
func TestVersionStatusCascade(t *testing.T) {
	reqs := newFakeRequirementRepo(
		&types.PMRequirement{ID: "r-confirmed", TenantID: 1, Status: types.RequirementStatusConfirmed},
		&types.PMRequirement{ID: "r-scheduled", TenantID: 1, Status: types.RequirementStatusScheduled},
		&types.PMRequirement{ID: "r-developing", TenantID: 1, Status: types.RequirementStatusDeveloping},
		&types.PMRequirement{ID: "r-launched", TenantID: 1, Status: types.RequirementStatusLaunched},
	)
	versions := newFakeVersionRepo(&types.PMVersion{
		ID: "v-1", TenantID: 1, ProjectID: "p-1",
		Status: types.VersionStatusReviewing, ReviewRound: 1,
		ReviewConclusion: types.VersionConclusionPass,
	})
	versions.links["v-1"] = []string{"r-confirmed", "r-scheduled", "r-developing", "r-launched"}
	svc := NewPMVersionService(versions, reqs)
	ctx := context.Background()

	// 定稿：confirmed → scheduled，其余不受影响
	_, err := svc.Finalize(ctx, 1, "v-1")
	require.NoError(t, err)
	assert.Equal(t, types.RequirementStatusScheduled, reqs.items["r-confirmed"].Status)
	assert.Equal(t, types.RequirementStatusScheduled, reqs.items["r-scheduled"].Status)
	assert.Equal(t, types.RequirementStatusDeveloping, reqs.items["r-developing"].Status)

	// 开发中：scheduled → developing
	require.NoError(t, svc.UpdateStatus(ctx, 1, "v-1", types.VersionStatusDeveloping))
	assert.Equal(t, types.RequirementStatusDeveloping, reqs.items["r-scheduled"].Status)

	// 上线：developing → launched，并写实际上线日
	require.NoError(t, svc.UpdateStatus(ctx, 1, "v-1", types.VersionStatusLaunched))
	assert.Equal(t, types.RequirementStatusLaunched, reqs.items["r-developing"].Status)
	assert.NotNil(t, reqs.items["r-developing"].ActualLaunchDate)
	assert.Equal(t, types.RequirementStatusLaunched, reqs.items["r-launched"].Status)
	assert.NotNil(t, versions.items["v-1"].LaunchDate)

	// 搁置：scheduled / developing → confirmed（回退，不保留排期）
	reqs.items["r-s1"] = &types.PMRequirement{ID: "r-s1", TenantID: 1, Status: types.RequirementStatusScheduled}
	reqs.items["r-s2"] = &types.PMRequirement{ID: "r-s2", TenantID: 1, Status: types.RequirementStatusDeveloping}
	versions.items["v-2"] = &types.PMVersion{
		ID: "v-2", TenantID: 1, ProjectID: "p-1", Status: types.VersionStatusPlanning,
	}
	versions.links["v-2"] = []string{"r-s1", "r-s2"}
	require.NoError(t, svc.UpdateStatus(ctx, 1, "v-2", types.VersionStatusShelved))
	assert.Equal(t, types.RequirementStatusConfirmed, reqs.items["r-s1"].Status)
	assert.Equal(t, types.RequirementStatusConfirmed, reqs.items["r-s2"].Status)
}

// 评审记录：轮次 +1，意见项带处理状态，历史轮次不可改
func TestReviewAppendsRound(t *testing.T) {
	versions := newFakeVersionRepo(&types.PMVersion{
		ID: "v-1", TenantID: 1, ProjectID: "p-1", Status: types.VersionStatusPlanning,
	})
	svc := NewPMVersionService(versions, nil)
	ctx := context.Background()

	first := types.JSON(`[{"item":"补充埋点","status":"pending"}]`)
	got, err := svc.Review(ctx, 1, "v-1", types.VersionConclusionConditional, first)
	require.NoError(t, err)
	assert.Equal(t, 1, got.ReviewRound)
	assert.Equal(t, types.VersionStatusReviewing, got.Status)

	second := types.JSON(`[{"item":"补充埋点","status":"done"}]`)
	got, err = svc.Review(ctx, 1, "v-1", types.VersionConclusionPass, second)
	require.NoError(t, err)
	assert.Equal(t, 2, got.ReviewRound)
	assert.Equal(t, types.VersionConclusionPass, got.ReviewConclusion)

	var rounds []types.PMVersionReviewRecord
	require.NoError(t, json.Unmarshal(got.ReviewOpinions, &rounds))
	require.Len(t, rounds, 2)
	assert.Equal(t, 1, rounds[0].Round)
	assert.JSONEq(t, string(first), string(rounds[0].Opinions)) // 历史轮次原样保留
	assert.Equal(t, 2, rounds[1].Round)

	// 定稿后评审关闭
	got.Status = types.VersionStatusFinalized
	versions.items["v-1"].Status = types.VersionStatusFinalized
	_, err = svc.Review(ctx, 1, "v-1", types.VersionConclusionPass, second)
	assert.ErrorIs(t, err, ErrVersionReviewClosed)
}

// ---- 测试替身：版本仓储 ----

var _ interfaces.PMVersionRepository = (*fakeVersionRepo)(nil)

type fakeVersionRepo struct {
	items   map[string]*types.PMVersion
	links   map[string][]string
	updates int
}

func newFakeVersionRepo(versions ...*types.PMVersion) *fakeVersionRepo {
	r := &fakeVersionRepo{
		items: make(map[string]*types.PMVersion, len(versions)),
		links: make(map[string][]string),
	}
	for _, v := range versions {
		r.items[v.ID] = v
	}
	return r
}

func (f *fakeVersionRepo) Create(_ context.Context, v *types.PMVersion) error {
	f.items[v.ID] = v
	return nil
}

func (f *fakeVersionRepo) GetByID(
	_ context.Context, tenantID uint64, id string,
) (*types.PMVersion, error) {
	v, ok := f.items[id]
	if !ok || v.TenantID != tenantID {
		return nil, repository.ErrPMVersionNotFound
	}
	cp := *v
	return &cp, nil
}

func (f *fakeVersionRepo) List(
	_ context.Context, tenantID uint64, projectID string,
) ([]*types.PMVersion, error) {
	var list []*types.PMVersion
	for _, v := range f.items {
		if v.TenantID == tenantID && v.ProjectID == projectID {
			list = append(list, v)
		}
	}
	return list, nil
}

func (f *fakeVersionRepo) Update(_ context.Context, v *types.PMVersion) error {
	if _, ok := f.items[v.ID]; !ok {
		return repository.ErrPMVersionNotFound
	}
	f.updates++
	cp := *v
	f.items[v.ID] = &cp
	return nil
}

// GetLatestVersionNo 只统计已生成版本号的记录（定稿及之后）
func (f *fakeVersionRepo) GetLatestVersionNo(
	_ context.Context, tenantID uint64, projectID string,
) (string, error) {
	latest := ""
	for _, v := range f.items {
		if v.TenantID != tenantID || v.ProjectID != projectID || v.VersionNo == "" {
			continue
		}
		if latest == "" || v.VersionNo > latest {
			latest = v.VersionNo
		}
	}
	return latest, nil
}

func (f *fakeVersionRepo) ReplaceRequirements(
	_ context.Context, versionID string, requirementIDs []string,
) error {
	f.links[versionID] = append([]string(nil), requirementIDs...)
	return nil
}

func (f *fakeVersionRepo) ListRequirementIDs(
	_ context.Context, versionID string,
) ([]string, error) {
	return f.links[versionID], nil
}

// CountVersionsByRequirementID 统计需求被多少个版本引用
func (f *fakeVersionRepo) CountVersionsByRequirementID(
	_ context.Context, requirementID string,
) (int64, error) {
	var n int64
	for _, ids := range f.links {
		for _, id := range ids {
			if id == requirementID {
				n++
			}
		}
	}
	return n, nil
}

// RemoveRequirementFromAllVersions 从全部版本的关联集合中剔除该需求
func (f *fakeVersionRepo) RemoveRequirementFromAllVersions(
	_ context.Context, requirementID string,
) error {
	for versionID, ids := range f.links {
		kept := make([]string, 0, len(ids))
		for _, id := range ids {
			if id != requirementID {
				kept = append(kept, id)
			}
		}
		f.links[versionID] = kept
	}
	return nil
}
