package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// 编译期断言：保证 7 个仓储实现始终满足接口契约
var (
	_ interfaces.PMProjectRepository     = (*pmProjectRepository)(nil)
	_ interfaces.PMRequirementRepository = (*pmRequirementRepository)(nil)
	_ interfaces.PMVersionRepository     = (*pmVersionRepository)(nil)
	_ interfaces.PMFeatureRepository     = (*pmFeatureRepository)(nil)
	_ interfaces.PMMaterialRepository    = (*pmMaterialRepository)(nil)
	_ interfaces.PMPlanBatchRepository   = (*pmPlanBatchRepository)(nil)
	_ interfaces.PMTaskRepository        = (*pmTaskRepository)(nil)
)

// newPMTestDB 建立独立的 SQLite 内存库：AutoMigrate 建表后补齐部分唯一索引，
// 单连接避免共享内存库在不同连接上各建一份导致表不可见。
func newPMTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:pmtest_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	require.NoError(t, db.AutoMigrate(
		&types.PMProject{}, &types.PMRequirement{}, &types.PMVersion{},
		&types.PMVersionRequirement{}, &types.PMFeature{}, &types.PMFeatureIteration{},
		&types.PMMaterial{}, &types.PMPlanBatch{}, &types.PMTask{},
	))

	// 部分唯一索引无法由 GORM tag 表达，按迁移脚本原样补齐
	for _, stmt := range []string{
		`CREATE UNIQUE INDEX uq_pm_projects_tenant_name ON pm_projects (tenant_id, name) WHERE deleted_at IS NULL`,
		`CREATE UNIQUE INDEX uq_pm_versions_no ON pm_versions (tenant_id, project_id, version_no) WHERE version_no <> '' AND deleted_at IS NULL`,
		`CREATE UNIQUE INDEX uq_pm_version_requirements ON pm_version_requirements (version_id, requirement_id)`,
		`CREATE UNIQUE INDEX uq_pm_plan_batches_active ON pm_plan_batches (tenant_id, project_id) WHERE status = 'active' AND deleted_at IS NULL`,
	} {
		require.NoError(t, db.Exec(stmt).Error)
	}
	return db
}

func TestPMProjectRepository(t *testing.T) {
	db := newPMTestDB(t)
	repo := NewPMProjectRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, &types.PMProject{
		TenantID: 1, Name: "智能客服", KBID: "kb-1", Status: "active",
	}))

	got, err := repo.GetByName(ctx, 1, "智能客服")
	require.NoError(t, err)
	require.NotEmpty(t, got.ID, "BeforeCreate 应补全 UUID")
	assert.Equal(t, "kb-1", got.KBID)

	// 跨租户不可见
	_, err = repo.GetByID(ctx, 2, got.ID)
	assert.ErrorIs(t, err, ErrPMProjectNotFound)

	// 同租户重名被唯一索引拦截
	err = repo.Create(ctx, &types.PMProject{TenantID: 1, Name: "智能客服"})
	assert.ErrorIs(t, err, ErrPMProjectNameTaken)
	// 不同租户可同名
	require.NoError(t, repo.Create(ctx, &types.PMProject{TenantID: 2, Name: "智能客服"}))

	// 软删后同名可复用
	require.NoError(t, db.Delete(&types.PMProject{}, "id = ?", got.ID).Error)
	_, err = repo.GetByID(ctx, 1, got.ID)
	assert.ErrorIs(t, err, ErrPMProjectNotFound)
	require.NoError(t, repo.Create(ctx, &types.PMProject{TenantID: 1, Name: "智能客服"}),
		"软删行不占用唯一键")

	// Update 强制租户隔离
	wrong := *got
	wrong.TenantID = 2
	wrong.Name = "越权改名"
	assert.ErrorIs(t, repo.Update(ctx, &wrong), ErrPMProjectNotFound)

	// Update 需能写入零值（清空 kb_id）
	fresh, err := repo.GetByName(ctx, 1, "智能客服")
	require.NoError(t, err)
	fresh.KBID = ""
	require.NoError(t, repo.Update(ctx, fresh))
	reloaded, err := repo.GetByID(ctx, 1, fresh.ID)
	require.NoError(t, err)
	assert.Empty(t, reloaded.KBID, "Select(*) 应把空串写回库")
}

// TestPMProjectRepositoryDelete 校验删除项目时的级联范围与租户隔离
func TestPMProjectRepositoryDelete(t *testing.T) {
	db := newPMTestDB(t)
	repo := NewPMProjectRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, &types.PMProject{TenantID: 1, Name: "待删除"}))
	target, err := repo.GetByName(ctx, 1, "待删除")
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, &types.PMProject{TenantID: 1, Name: "保留"}))
	keep, err := repo.GetByName(ctx, 1, "保留")
	require.NoError(t, err)

	// 两个项目各插一套完整数据，用于验证只清理目标项目
	for _, projectID := range []string{target.ID, keep.ID} {
		createPMSampleData(t, db, 1, projectID)
	}

	require.NoError(t, repo.Delete(ctx, 1, target.ID))

	// 项目本身与他人数据仍在
	_, err = repo.GetByID(ctx, 1, target.ID)
	assert.ErrorIs(t, err, ErrPMProjectNotFound)
	_, err = repo.GetByID(ctx, 1, keep.ID)
	require.NoError(t, err)

	// 业务表软删：默认查询不可见，Unscoped 仍可追溯
	for _, model := range []any{
		&types.PMRequirement{}, &types.PMVersion{}, &types.PMFeature{},
		&types.PMMaterial{}, &types.PMPlanBatch{},
	} {
		var live, all int64
		require.NoError(t, db.Model(model).Where("project_id = ?", target.ID).Count(&live).Error)
		require.NoError(t, db.Unscoped().Model(model).Where("project_id = ?", target.ID).Count(&all).Error)
		assert.Zero(t, live)
		assert.EqualValues(t, 1, all)
	}

	// 关联表与任务无软删列，按硬删校验（含 Unscoped）
	assert.Zero(t, countPMRows(t, db, "pm_version_requirements", "version_id IN (SELECT id FROM pm_versions WHERE project_id = ?)", target.ID))
	assert.Zero(t, countPMRows(t, db, "pm_feature_iterations", "feature_id IN (SELECT id FROM pm_features WHERE project_id = ?)", target.ID))
	assert.Zero(t, countPMRows(t, db, "pm_tasks", "project_id = ?", target.ID))

	// 另一项目的关联数据不受影响
	assert.EqualValues(t, 1, countPMRows(t, db, "pm_version_requirements", "version_id IN (SELECT id FROM pm_versions WHERE project_id = ?)", keep.ID))
	assert.EqualValues(t, 1, countPMRows(t, db, "pm_feature_iterations", "feature_id IN (SELECT id FROM pm_features WHERE project_id = ?)", keep.ID))
	assert.EqualValues(t, 1, countPMRows(t, db, "pm_tasks", "project_id = ?", keep.ID))

	// 重复删除返回未找到
	assert.ErrorIs(t, repo.Delete(ctx, 1, target.ID), ErrPMProjectNotFound)
}

// createPMSampleData 为指定项目插入各业务表的样例数据
func createPMSampleData(t *testing.T, db *gorm.DB, tenantID uint64, projectID string) {
	t.Helper()
	req := &types.PMRequirement{TenantID: tenantID, ProjectID: projectID, Title: "需求"}
	ver := &types.PMVersion{TenantID: tenantID, ProjectID: projectID, Name: "V1"}
	feat := &types.PMFeature{TenantID: tenantID, ProjectID: projectID, FeatureName: "功能"}
	require.NoError(t, db.Create(req).Error)
	require.NoError(t, db.Create(ver).Error)
	require.NoError(t, db.Create(feat).Error)
	require.NoError(t, db.Create(&types.PMVersionRequirement{
		TenantID: tenantID, VersionID: ver.ID, RequirementID: req.ID,
	}).Error)
	require.NoError(t, db.Create(&types.PMFeatureIteration{
		TenantID: tenantID, FeatureID: feat.ID, VersionID: ver.ID, ChangeType: "add",
	}).Error)
	require.NoError(t, db.Create(&types.PMMaterial{
		TenantID: tenantID, ProjectID: projectID, MaterialType: "manual", Title: "手册",
	}).Error)
	require.NoError(t, db.Create(&types.PMPlanBatch{
		TenantID: tenantID, ProjectID: projectID, BatchName: "首期规划",
	}).Error)
	require.NoError(t, db.Create(&types.PMTask{
		TenantID: tenantID, ProjectID: projectID, TaskType: "material_generate",
	}).Error)
}

// countPMRows 统计指定条件（含软删行）的行数
func countPMRows(t *testing.T, db *gorm.DB, table, where string, args ...any) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Table(table).Unscoped().Where(where, args...).Count(&n).Error)
	return n
}
