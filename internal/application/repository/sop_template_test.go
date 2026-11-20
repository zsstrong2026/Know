package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Tencent/WeKnora/internal/types"
)

// newSopTplTestDB 建独立 SQLite 内存库并 AutoMigrate 模板与 revision 两张表。
func newSopTplTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:soptpltest_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	require.NoError(t, db.AutoMigrate(&types.SopTemplate{}, &types.SopTemplateRevision{}))
	return db
}

// seedBuiltinTemplate 直接落库一行内置模板，绕过仓储 Create 的内置写入拒绝。
// 对应 T7 以 tenant_id=0 的 SQL migration seed 内置模板的路径（仓储方法不是种子入口）。
func seedBuiltinTemplate(t *testing.T, db *gorm.DB, tpl *types.SopTemplate) {
	t.Helper()
	tpl.IsBuiltin = true
	require.NoError(t, db.Create(tpl).Error)
}

func TestPublishImmutableRevision(t *testing.T) {
	db := newSopTplTestDB(t)
	repo := NewSopTemplateRepo(db)
	ctx := context.Background()
	tpl := &types.SopTemplate{TenantID: 1, Name: "t", Definition: types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Name: "v1", Type: "agent"}}}}
	require.NoError(t, repo.Create(ctx, tpl))
	rev, err := repo.Publish(ctx, 1, tpl.ID, "u1")
	require.NoError(t, err)
	require.Equal(t, 1, rev.Revision)
	// 发布后改草稿不影响 revision
	tpl.Definition.Nodes[0].Name = "v2"
	require.NoError(t, repo.Update(ctx, tpl))
	got, err := repo.GetRevision(ctx, 1, rev.ID)
	require.NoError(t, err)
	require.Equal(t, "v1", got.Definition.Nodes[0].Name, "revision 必须冻结")
	// 再发布产生 revision 2
	rev2, err := repo.Publish(ctx, 1, tpl.ID, "u1")
	require.NoError(t, err)
	require.Equal(t, 2, rev2.Revision)
}

// 内置模板必须 seed-only：Create 收到 IsBuiltin=true 一律拒绝且不落库。
// 读路径带 `OR is_builtin = true`，若放行任意调用方建内置行，就能让一行模板对所有租户可见。
func TestCreateRejectsBuiltinFlag(t *testing.T) {
	db := newSopTplTestDB(t)
	repo := NewSopTemplateRepo(db)
	ctx := context.Background()

	tpl := &types.SopTemplate{TenantID: 3, Name: "想冒充内置", IsBuiltin: true}
	err := repo.Create(ctx, tpl)
	require.ErrorIs(t, err, ErrSopTemplateBuiltinReadonly)

	var count int64
	require.NoError(t, db.Model(&types.SopTemplate{}).Count(&count).Error)
	require.Zero(t, count, "被拒绝的创建不得插入任何行")

	// 别的租户也不应该看到（读路径没有任何内置行可用）
	list, err := repo.ListByTenant(ctx, 9, "")
	require.NoError(t, err)
	require.Empty(t, list)
}

func TestBuiltinReadonlyCopy(t *testing.T) {
	db := newSopTplTestDB(t)
	repo := NewSopTemplateRepo(db)
	ctx := context.Background()
	b := &types.SopTemplate{TenantID: 1, Name: "内置", Status: "published"}
	seedBuiltinTemplate(t, db, b)
	require.Error(t, repo.Update(ctx, b), "内置模板只读")
	cp, err := repo.Copy(ctx, 1, b.ID, "副本")
	require.NoError(t, err)
	require.False(t, cp.IsBuiltin)
	require.Equal(t, "draft", cp.Status)
	require.NotEqual(t, b.ID, cp.ID)
}

// 发布回写：published_revision_id / status=published / version=新 revision，
// 且返回的 revision 与活模板互不共享底层切片（冻结快照）。
func TestPublishBackfillsTemplateState(t *testing.T) {
	db := newSopTplTestDB(t)
	repo := NewSopTemplateRepo(db)
	ctx := context.Background()
	tpl := &types.SopTemplate{TenantID: 1, Name: "t", Definition: types.SopDefinition{
		Nodes: []types.SopNode{{ID: "n1", Name: "v1", Type: "agent"}},
	}}
	seedBuiltinTemplate(t, db, tpl)
	rev, err := repo.Publish(ctx, 1, tpl.ID, "u1")
	require.NoError(t, err)

	got, err := repo.GetByID(ctx, 1, tpl.ID)
	require.NoError(t, err)
	require.Equal(t, rev.ID, got.PublishedRevisionID)
	require.Equal(t, types.SopTemplateStatusPublished, got.Status)
	require.Equal(t, 1, got.Version)

	// 内存中的 revision 快照不随源模板定义变动（深拷贝）
	tpl.Definition.Nodes[0].Name = "改过了"
	require.Equal(t, "v1", rev.Definition.Nodes[0].Name)
}

func TestListRevisionsDescending(t *testing.T) {
	db := newSopTplTestDB(t)
	repo := NewSopTemplateRepo(db)
	ctx := context.Background()
	tpl := &types.SopTemplate{TenantID: 1, Name: "t"}
	require.NoError(t, repo.Create(ctx, tpl))
	for i := 0; i < 2; i++ {
		_, err := repo.Publish(ctx, 1, tpl.ID, "u1")
		require.NoError(t, err)
	}
	revs, err := repo.ListRevisions(ctx, 1, tpl.ID)
	require.NoError(t, err)
	require.Len(t, revs, 2)
	require.Equal(t, 2, revs[0].Revision, "按 revision 降序")
	require.Equal(t, 1, revs[1].Revision)
	// 别的租户读不到
	other, err := repo.ListRevisions(ctx, 2, tpl.ID)
	require.NoError(t, err)
	require.Empty(t, other)
}

// ListByTenant 口径：本租户全局（project_id=”）+ 指定项目 + 内置，
// 别的项目、别的租户一律不返回。
func TestListByTenantScope(t *testing.T) {
	db := newSopTplTestDB(t)
	repo := NewSopTemplateRepo(db)
	ctx := context.Background()
	mk := func(tenantID uint64, projectID, name string, builtin bool) *types.SopTemplate {
		tpl := &types.SopTemplate{TenantID: tenantID, ProjectID: projectID, Name: name}
		if builtin {
			seedBuiltinTemplate(t, db, tpl)
		} else {
			require.NoError(t, repo.Create(ctx, tpl))
		}
		return tpl
	}
	global := mk(1, "", "全局", false)
	projected := mk(1, "p1", "本项目", false)
	otherProject := mk(1, "p2", "别的项目", false)
	builtin := mk(1, "p9", "内置", true)
	mk(2, "", "别的租户", false)

	list, err := repo.ListByTenant(ctx, 1, "p1")
	require.NoError(t, err)
	ids := make([]string, 0, len(list))
	for _, tpl := range list {
		ids = append(ids, tpl.ID)
	}
	require.ElementsMatch(t, []string{global.ID, projected.ID, builtin.ID}, ids)
	require.NotContains(t, ids, otherProject.ID)
}

func TestCopyIsIndependentDraft(t *testing.T) {
	db := newSopTplTestDB(t)
	repo := NewSopTemplateRepo(db)
	ctx := context.Background()
	src := &types.SopTemplate{TenantID: 1, ProjectID: "p1", Name: "源", Status: "published", Version: 3,
		MetricCategory: "交付", PublishedRevisionID: "rev-x",
		Definition: types.SopDefinition{
			Nodes:       []types.SopNode{{ID: "n1", Name: "分析", Type: "agent", MCP: []string{"know-kb"}}},
			Edges:       []types.SopEdge{{ID: "e1", Source: "n1", Target: "n2", When: "success"}},
			InputSchema: []string{"title"},
		}}
	seedBuiltinTemplate(t, db, src)

	cp, err := repo.Copy(ctx, 1, src.ID, "副本")
	require.NoError(t, err)
	require.Equal(t, "副本", cp.Name)
	require.Equal(t, "p1", cp.ProjectID)
	require.Equal(t, "交付", cp.MetricCategory)
	require.Empty(t, cp.PublishedRevisionID, "副本是未发布的草稿")
	require.Equal(t, 1, cp.Version)

	// 改副本不得影响源模板（definition 深拷贝）
	cp.Definition.Nodes[0].Name = "副本节点"
	cp.Definition.Nodes[0].MCP[0] = "other"
	require.NoError(t, repo.Update(ctx, cp))
	got, err := repo.GetByID(ctx, 1, src.ID)
	require.NoError(t, err)
	require.Equal(t, "分析", got.Definition.Nodes[0].Name)
	require.Equal(t, "know-kb", got.Definition.Nodes[0].MCP[0])
	require.Equal(t, "published", got.Status)
}

// 内置行 tenant_id=0 的 seed（T7）对所有租户可见且可复制；复制品落在复制方租户。
func TestCopySharedBuiltinFromAnotherTenant(t *testing.T) {
	db := newSopTplTestDB(t)
	repo := NewSopTemplateRepo(db)
	ctx := context.Background()
	seed := &types.SopTemplate{ID: "b0000000-0000-4000-8000-000000000001", TenantID: 0,
		Name: "产品方案交付", Status: "published",
		Definition: types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Name: "分析", Type: "agent"}}}}
	seedBuiltinTemplate(t, db, seed)

	cp, err := repo.Copy(ctx, 7, seed.ID, "我的副本")
	require.NoError(t, err)
	require.Equal(t, uint64(7), cp.TenantID)
	require.False(t, cp.IsBuiltin)
	require.NotEqual(t, seed.ID, cp.ID)
	// 源内置行不被复制动作改动：仍是内置、仍跨租户可读
	src, err := repo.GetByID(ctx, 7, seed.ID)
	require.NoError(t, err)
	require.True(t, src.IsBuiltin)
	// 副本可写、可更新
	cp.Description = "改一下"
	require.NoError(t, repo.Update(ctx, cp))
}

func TestBuiltinGuardsWritePaths(t *testing.T) {
	db := newSopTplTestDB(t)
	repo := NewSopTemplateRepo(db)
	ctx := context.Background()
	b := &types.SopTemplate{TenantID: 1, Name: "内置"}
	seedBuiltinTemplate(t, db, b)

	err := repo.Update(ctx, b)
	require.ErrorIs(t, err, ErrSopTemplateBuiltinReadonly)
	err = repo.Delete(ctx, 1, b.ID)
	require.ErrorIs(t, err, ErrSopTemplateBuiltinReadonly)
	// 内置仍在（未被软删）
	got, err := repo.GetByID(ctx, 1, b.ID)
	require.NoError(t, err)
	require.Equal(t, b.ID, got.ID)
}

func TestTemplateTenantIsolation(t *testing.T) {
	db := newSopTplTestDB(t)
	repo := NewSopTemplateRepo(db)
	ctx := context.Background()
	tpl := &types.SopTemplate{TenantID: 1, Name: "t"}
	require.NoError(t, repo.Create(ctx, tpl))

	_, err := repo.GetByID(ctx, 2, tpl.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	// 跨租户 Update 不落库
	hijack := *tpl
	hijack.TenantID = 2
	hijack.Name = "被改名"
	require.ErrorIs(t, repo.Update(ctx, &hijack), gorm.ErrRecordNotFound)
	got, err := repo.GetByID(ctx, 1, tpl.ID)
	require.NoError(t, err)
	require.Equal(t, "t", got.Name)

	require.ErrorIs(t, repo.Delete(ctx, 2, tpl.ID), gorm.ErrRecordNotFound)

	_, err = repo.Publish(ctx, 2, tpl.ID, "u2")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

// 内置模板只读：跨租户可见（读），但 Update / Delete / Publish 一律拒绝，
// 发布也不会把发布态回写到别人的内置行上。
func TestBuiltinCrossTenantReadOnly(t *testing.T) {
	db := newSopTplTestDB(t)
	repo := NewSopTemplateRepo(db)
	ctx := context.Background()
	b := &types.SopTemplate{TenantID: 1, Name: "内置"}
	seedBuiltinTemplate(t, db, b)

	got, err := repo.GetByID(ctx, 9, b.ID)
	require.NoError(t, err, "内置模板应跨租户只读可见")
	require.Equal(t, b.ID, got.ID)

	other := *b
	other.TenantID = 9
	require.ErrorIs(t, repo.Update(ctx, &other), ErrSopTemplateBuiltinReadonly)
	require.ErrorIs(t, repo.Delete(ctx, 9, b.ID), ErrSopTemplateBuiltinReadonly)
	// 跨租户发布不得把自己的发布态写到别人的内置行上
	_, err = repo.Publish(ctx, 9, b.ID, "u9")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	// 发布态未被改写
	after, err := repo.GetByID(ctx, 1, b.ID)
	require.NoError(t, err)
	require.Equal(t, types.SopTemplateStatusDraft, after.Status)
	require.Empty(t, after.PublishedRevisionID)
}

// 草稿未发布时 published_revision_id 为空、status 保持 draft（Publish 之外的路径不写状态）。
func TestCreateDefaultsDraft(t *testing.T) {
	db := newSopTplTestDB(t)
	repo := NewSopTemplateRepo(db)
	ctx := context.Background()
	tpl := &types.SopTemplate{TenantID: 1, Name: "t"}
	require.NoError(t, repo.Create(ctx, tpl))
	got, err := repo.GetByID(ctx, 1, tpl.ID)
	require.NoError(t, err)
	require.Equal(t, types.SopTemplateStatusDraft, got.Status)
	require.Equal(t, 1, got.Version)
	require.Empty(t, got.PublishedRevisionID)
	require.NotEmpty(t, got.ID, "BeforeCreate 必须补 uuid")
	require.NotNil(t, got.Definition.Nodes, "definition JSON 不得为 NULL")
}

// Update 必须真正落库 definition（serializer 列按白名单写入，不能静默丢失）。
func TestUpdatePersistsDefinition(t *testing.T) {
	db := newSopTplTestDB(t)
	repo := NewSopTemplateRepo(db)
	ctx := context.Background()
	tpl := &types.SopTemplate{TenantID: 1, Name: "t", Definition: types.SopDefinition{
		Nodes: []types.SopNode{{ID: "n1", Name: "v1", Type: "agent", MCP: []string{"know-kb"}}},
	}}
	require.NoError(t, repo.Create(ctx, tpl))

	tpl.Definition.Nodes[0].Name = "v2"
	tpl.Definition.Nodes[0].OnFailure = "reject"
	tpl.Definition.Edges = []types.SopEdge{{ID: "e1", Source: "n1", Target: "n2", When: "success"}}
	require.NoError(t, repo.Update(ctx, tpl))

	got, err := repo.GetByID(ctx, 1, tpl.ID)
	require.NoError(t, err)
	require.Equal(t, "v2", got.Definition.Nodes[0].Name)
	require.Equal(t, "reject", got.Definition.Nodes[0].OnFailure)
	require.Len(t, got.Definition.Edges, 1)
	require.Equal(t, "n2", got.Definition.Edges[0].Target)

	// 清空描述是合法编辑：白名单列上的零值必须落库（不能被零值跳过吞掉）
	tpl.Description = ""
	require.NoError(t, repo.Update(ctx, tpl))
	got, err = repo.GetByID(ctx, 1, tpl.ID)
	require.NoError(t, err)
	require.Empty(t, got.Description)
}

// Update 不覆盖发布态归属列（status/version/published_revision_id 只由 Publish 推进）：
// 调用方拿旧快照回存时不得把发布态冲回草稿或伪造版本号。
func TestUpdateKeepsPublishOwnedColumns(t *testing.T) {
	db := newSopTplTestDB(t)
	repo := NewSopTemplateRepo(db)
	ctx := context.Background()
	tpl := &types.SopTemplate{TenantID: 1, Name: "t"}
	require.NoError(t, repo.Create(ctx, tpl))

	rev, err := repo.Publish(ctx, 1, tpl.ID, "u1")
	require.NoError(t, err)

	edited := *tpl
	edited.Status = types.SopTemplateStatusArchived
	edited.Version = 99
	edited.Name = "改名"
	require.NoError(t, repo.Update(ctx, &edited))

	got, err := repo.GetByID(ctx, 1, tpl.ID)
	require.NoError(t, err)
	require.Equal(t, "改名", got.Name)
	require.Equal(t, types.SopTemplateStatusPublished, got.Status, "status 只能由 Publish 写")
	require.Equal(t, 1, got.Version, "version 只能由 Publish 写")
	require.Equal(t, rev.ID, got.PublishedRevisionID)
}
