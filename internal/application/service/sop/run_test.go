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

func newRunTestService(t *testing.T) (*RunService, *gorm.DB) {
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
		&types.SopProject{}, &types.SopItem{}, &types.SopTemplate{},
		&types.SopTemplateRevision{}, &types.SopRun{}, &types.SopNodeRun{},
	))
	svc := NewRunService(
		repository.NewSopTemplateRepo(db), repository.NewSopRunRepo(db),
		repository.NewSopProjectRepo(db), repository.NewSopItemRepo(db),
	)
	return svc, db
}

// seedPublishedTemplate 建一份已发布模板（单 agent 节点），返回模板与 revision。
func seedPublishedTemplate(t *testing.T, db *gorm.DB, schema []string) *types.SopTemplate {
	t.Helper()
	ctx := context.Background()
	tpl := &types.SopTemplate{TenantID: 1, Name: "流程", Definition: types.SopDefinition{
		InputSchema: schema,
		Nodes: []types.SopNode{{
			ID: "n1", Name: "实现", Type: "agent",
			Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"},
		}},
	}}
	repo := repository.NewSopTemplateRepo(db)
	require.NoError(t, repo.Create(ctx, tpl))
	_, err := repo.Publish(ctx, 1, tpl.ID, "u1")
	require.NoError(t, err)
	got, err := repo.GetByID(ctx, 1, tpl.ID)
	require.NoError(t, err)
	return got
}

func TestRunCreateRequiresPublishedTemplate(t *testing.T) {
	svc, db := newRunTestService(t)
	ctx := context.Background()
	draft := &types.SopTemplate{TenantID: 1, Name: "草稿",
		Definition: types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "agent"}}}}
	require.NoError(t, repository.NewSopTemplateRepo(db).Create(ctx, draft))

	_, err := svc.Create(ctx, 1, "u1", CreateRunInput{TemplateID: draft.ID})
	require.ErrorIs(t, err, ErrSopTemplateUnpublished)
}

// 运行快照来自发布 revision：发布后改草稿不影响已创建的运行。
func TestRunCreateSnapshotsRevision(t *testing.T) {
	svc, db := newRunTestService(t)
	ctx := context.Background()
	tpl := seedPublishedTemplate(t, db, nil)

	run, err := svc.Create(ctx, 1, "u1", CreateRunInput{TemplateID: tpl.ID})
	require.NoError(t, err)
	require.Equal(t, types.SopRunQueued, run.Status)
	require.Equal(t, types.SopRunTriggerManual, run.TriggerKind)
	require.Equal(t, tpl.PublishedRevisionID, run.RevisionID)
	require.Equal(t, "实现", run.WorkflowSnapshot.Nodes[0].Name)

	// 改草稿（不改 revision）
	tpl.Definition.Nodes[0].Name = "改了"
	require.NoError(t, repository.NewSopTemplateRepo(db).Update(ctx, tpl))

	got, err := svc.Get(ctx, 1, run.ID)
	require.NoError(t, err)
	require.Equal(t, "实现", got.WorkflowSnapshot.Nodes[0].Name, "快照必须冻住")
}

// InputSchema 声明的键必填，缺项时逐项列出。
func TestRunCreateValidatesInputSchema(t *testing.T) {
	svc, db := newRunTestService(t)
	ctx := context.Background()
	tpl := seedPublishedTemplate(t, db, []string{"repo_url", "branch"})

	_, err := svc.Create(ctx, 1, "u1", CreateRunInput{TemplateID: tpl.ID})
	require.ErrorIs(t, err, ErrSopRunInputMissing)
	require.Contains(t, err.Error(), "repo_url")
	require.Contains(t, err.Error(), "branch")

	_, err = svc.Create(ctx, 1, "u1", CreateRunInput{
		TemplateID: tpl.ID, Input: map[string]any{"repo_url": "https://x/a.git", "branch": "  "},
	})
	require.ErrorIs(t, err, ErrSopRunInputMissing, "空白字符串视同缺失")
	require.Contains(t, err.Error(), "branch")

	run, err := svc.Create(ctx, 1, "u1", CreateRunInput{
		TemplateID: tpl.ID, Input: map[string]any{"repo_url": "https://x/a.git", "branch": "main"},
	})
	require.NoError(t, err)
	require.Equal(t, "main", run.Input["branch"])
}

// 事项可选：指定时校验归属，跨项目/不存在都拒绝。
func TestRunCreateWithItem(t *testing.T) {
	svc, db := newRunTestService(t)
	ctx := context.Background()
	tpl := seedPublishedTemplate(t, db, nil)

	proj := &types.SopProject{TenantID: 1, Name: "P", Code: "TP"}
	require.NoError(t, repository.NewSopProjectRepo(db).Create(ctx, proj))
	item := &types.SopItem{TenantID: 1, ProjectID: proj.ID, No: "TP-1", Title: "需求", Status: "todo"}
	require.NoError(t, repository.NewSopItemRepo(db).Create(ctx, item))

	run, err := svc.Create(ctx, 1, "u1", CreateRunInput{TemplateID: tpl.ID, ItemID: item.ID})
	require.NoError(t, err)
	require.Equal(t, item.ID, run.ItemID)

	_, err = svc.Create(ctx, 1, "u1", CreateRunInput{TemplateID: tpl.ID, ItemID: "nope"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "事项不存在")
}

// 触发器可覆盖（T35 定时派发复用同一入口）。
func TestRunCreateHonorsTrigger(t *testing.T) {
	svc, db := newRunTestService(t)
	ctx := context.Background()
	tpl := seedPublishedTemplate(t, db, nil)
	run, err := svc.Create(ctx, 1, "system", CreateRunInput{
		TemplateID: tpl.ID, TriggerKind: types.SopRunTriggerSchedule, TriggerRefID: "trig-1",
	})
	require.NoError(t, err)
	require.Equal(t, types.SopRunTriggerSchedule, run.TriggerKind)
	require.Equal(t, "trig-1", run.TriggerRefID)
}
