package sop

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
)

func newTplTestService(t *testing.T) (*TemplateService, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(
		&types.SopProject{}, &types.SopTemplate{}, &types.SopTemplateRevision{},
	))
	return NewTemplateService(repository.NewSopTemplateRepo(db), repository.NewSopProjectRepo(db)), db
}

func TestPublishRejectsInvalidDefinitionPlainWords(t *testing.T) {
	svc, db := newTplTestService(t)
	ctx := context.Background()
	tpl := &types.SopTemplate{TenantID: 1, Name: "坏流程", Definition: types.SopDefinition{
		Nodes: []types.SopNode{{ID: "n1", Name: "实现", Type: "agent", OnFailure: "reject"}},
	}}
	require.NoError(t, repository.NewSopTemplateRepo(db).Create(ctx, tpl))

	_, err := svc.Publish(ctx, 1, tpl.ID, "u1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "打回哪一步", "白话逐条返回，不暴露内部标识")
	require.ErrorIs(t, err, ErrSopTemplateInvalid)
}

func TestBuiltinPublishExemptsExecutorCompleteness(t *testing.T) {
	svc, db := newTplTestService(t)
	ctx := context.Background()
	b := &types.SopTemplate{TenantID: 1, Name: "内置", IsBuiltin: true, Definition: types.SopDefinition{
		Nodes: []types.SopNode{{ID: "n1", Name: "内置步骤", Type: "agent",
			Executor: types.SopNodeExecutor{Kind: "platform_agent"}}},
	}}
	// 内置行绕过 Create 的只读守卫（模拟 T7 seed 落库）。
	require.NoError(t, db.Create(b).Error)

	rev, err := svc.Publish(ctx, 1, b.ID, "u1")
	require.NoError(t, err, "内置豁免执行器完整性")
	require.Equal(t, 1, rev.Revision)

	got, err := svc.Get(ctx, 1, b.ID)
	require.NoError(t, err)
	require.Equal(t, types.SopTemplateStatusPublished, got.Status)
	require.Equal(t, rev.ID, got.PublishedRevisionID, "发布态回写指向刚冻的那条 revision")
	require.Equal(t, 1, got.Version)
}

func TestSaveRefreshesDerivedGateCommands(t *testing.T) {
	svc, db := newTplTestService(t)
	ctx := context.Background()
	tpl := &types.SopTemplate{TenantID: 1, Name: "t", Definition: types.SopDefinition{
		Nodes: []types.SopNode{{
			ID: "n1", Name: "写产物", Type: "agent",
			GateCommands: []string{"AI 乱写的"},
			ArtifactOutputs: []types.SopArtifactOutput{{
				Name: "r", Path: "out/a.md", Check: types.SopArtifactCheck{Type: "exists"},
			}},
		}},
	}}
	require.NoError(t, repository.NewSopTemplateRepo(db).Create(ctx, tpl))

	require.NoError(t, svc.Save(ctx, 1, tpl))
	got, err := repository.NewSopTemplateRepo(db).GetByID(ctx, 1, tpl.ID)
	require.NoError(t, err)
	// 路径在 shell 里带引号（编译器的既定口径）；关键是 AI 乱写的命令被覆盖。
	require.Len(t, got.Definition.Nodes[0].GateCommands, 1)
	require.Contains(t, got.Definition.Nodes[0].GateCommands[0], "out/a.md")
	require.NotContains(t, got.Definition.Nodes[0].GateCommands[0], "AI 乱写的",
		"门禁命令永远按定义现算，不信任传入值")
}

// 内置模板不能直接保存/删除：唯一修改路径是复制副本。
func TestBuiltinTemplateIsReadonly(t *testing.T) {
	svc, db := newTplTestService(t)
	ctx := context.Background()
	b := &types.SopTemplate{TenantID: 1, Name: "内置", IsBuiltin: true,
		Definition: types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "agent"}}}}
	require.NoError(t, db.Create(b).Error)

	require.ErrorIs(t, svc.Save(ctx, 1, b), ErrSopTemplateReadonly)
	require.ErrorIs(t, svc.Delete(ctx, 1, b.ID), ErrSopTemplateReadonly)
	require.ErrorIs(t, svc.Save(ctx, 1, &types.SopTemplate{ID: b.ID, TenantID: 1, IsBuiltin: true}),
		ErrSopTemplateReadonly, "调用方自报 is_builtin 也不放行")
}

// 副本：新 id、草稿、非内置，定义深拷贝。
func TestCopyCreatesIndependentDraft(t *testing.T) {
	svc, db := newTplTestService(t)
	ctx := context.Background()
	src := &types.SopTemplate{TenantID: 1, Name: "源模板", Definition: types.SopDefinition{
		Nodes: []types.SopNode{{
			ID: "n1", Name: "第一步", Type: "agent",
			Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"},
		}},
	}}
	require.NoError(t, repository.NewSopTemplateRepo(db).Create(ctx, src))

	cp, err := svc.Copy(ctx, 1, src.ID, "")
	require.NoError(t, err)
	require.NotEqual(t, src.ID, cp.ID)
	require.False(t, cp.IsBuiltin)
	require.Equal(t, types.SopTemplateStatusDraft, cp.Status)
	require.Equal(t, "源模板 副本", cp.Name)
	require.Equal(t, src.Definition.Nodes[0].Name, cp.Definition.Nodes[0].Name)

	// 改副本不影响源
	cp.Definition.Nodes[0].Name = "改过的"
	require.NoError(t, svc.Save(ctx, 1, cp))
	again, err := svc.Get(ctx, 1, src.ID)
	require.NoError(t, err)
	require.Equal(t, "第一步", again.Definition.Nodes[0].Name)
}

// 发布是只增不改：第二次发布得到 revision=2，旧快照内容不变。
func TestPublishCreatesImmutableRevisions(t *testing.T) {
	svc, db := newTplTestService(t)
	ctx := context.Background()
	tpl := &types.SopTemplate{TenantID: 1, Name: "t", Definition: types.SopDefinition{
		Nodes: []types.SopNode{{ID: "n1", Name: "一", Type: "agent",
			Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"}}},
	}}
	require.NoError(t, repository.NewSopTemplateRepo(db).Create(ctx, tpl))
	rev1, err := svc.Publish(ctx, 1, tpl.ID, "u1")
	require.NoError(t, err)
	require.Equal(t, 1, rev1.Revision)

	tpl.Definition.Nodes[0].Name = "二"
	require.NoError(t, svc.Save(ctx, 1, tpl))
	rev2, err := svc.Publish(ctx, 1, tpl.ID, "u2")
	require.NoError(t, err)
	require.Equal(t, 2, rev2.Revision)

	old, err := svc.GetRevision(ctx, 1, rev1.ID)
	require.NoError(t, err)
	require.Equal(t, "一", old.Definition.Nodes[0].Name, "旧 revision 必须冻住")
	require.Equal(t, "二", rev2.Definition.Nodes[0].Name)

	list, err := svc.ListRevisions(ctx, 1, tpl.ID)
	require.NoError(t, err)
	require.Len(t, list, 2)
	require.Equal(t, 2, list[0].Revision, "revision 降序")
}

// 创建要求名称非空；项目不存在时报错。
func TestCreateTemplateValidation(t *testing.T) {
	svc, _ := newTplTestService(t)
	ctx := context.Background()
	_, err := svc.Create(ctx, 1, "u1", TemplateInput{Name: "  "})
	require.ErrorIs(t, err, ErrSopTemplateNameRequired)

	_, err = svc.Create(ctx, 1, "u1", TemplateInput{Name: "t", ProjectID: "nope"})
	require.Error(t, err)

	tpl, err := svc.Create(ctx, 1, "u1", TemplateInput{
		Name: "正常", Definition: types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "agent"}}},
	})
	require.NoError(t, err)
	require.Equal(t, types.SopTemplateStatusDraft, tpl.Status)
	require.Equal(t, 3, tpl.Definition.Nodes[0].MaxAttempts, "创建时补默认值")
}
