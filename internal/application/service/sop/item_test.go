package sop

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
)

func newItemTestService(t *testing.T) (*ItemService, *gorm.DB) {
	t.Helper()
	db := newItemTestDB(t)
	svc := NewItemService(
		repository.NewSopProjectRepo(db), repository.NewSopItemRepo(db),
		repository.NewSopItemExtraRepo(db), repository.NewSopRunRepo(db),
	)
	return svc, db
}

func newItemTestDB(t *testing.T) *gorm.DB {
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
		&types.SopProject{}, &types.SopItem{}, &types.SopItemAttachment{},
		&types.SopItemClarification{}, &types.SopItemActivity{},
		&types.SopRun{}, &types.SopNodeRun{},
	))
	require.NoError(t, db.Exec(
		"CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_node_runs_run_node ON sop_node_runs(run_id, node_id)",
	).Error)
	return db
}

// seedItem 建一个项目 + 一个事项（返回事项）。
func seedItem(t *testing.T, db *gorm.DB, status string) *types.SopItem {
	t.Helper()
	ctx := context.Background()
	proj := &types.SopProject{TenantID: 1, Name: "P", Code: "TP"}
	require.NoError(t, repository.NewSopProjectRepo(db).Create(ctx, proj))
	it := &types.SopItem{
		TenantID: 1, ProjectID: proj.ID, No: "TP-1", Title: "事项", Status: status,
		Source: "internal", Kind: "requirement", Priority: "P2",
	}
	require.NoError(t, repository.NewSopItemRepo(db).Create(ctx, it))
	return it
}

func TestItemCreateAllocatesNumber(t *testing.T) {
	svc, db := newItemTestService(t)
	ctx := context.Background()
	p := &types.SopProject{TenantID: 1, Name: "P", Code: "TP"}
	require.NoError(t, repository.NewSopProjectRepo(db).Create(ctx, p))

	a, err := svc.Create(ctx, 1, "u1", p.ID, CreateItemInput{Title: "第一个"})
	require.NoError(t, err)
	require.Equal(t, "TP-1", a.No)
	require.Equal(t, types.SopItemStatusBacklog, a.Status)
	require.Equal(t, "u1", a.CreatedBy)

	b, err := svc.Create(ctx, 1, "u1", p.ID, CreateItemInput{Title: "第二个"})
	require.NoError(t, err)
	require.Equal(t, "TP-2", b.No, "编号由项目游标自增")

	// 业务活动留痕
	acts, err := svc.ListActivities(ctx, 1, a.ID, types.SopActivityKindBusiness)
	require.NoError(t, err)
	require.Len(t, acts, 1)
	require.Equal(t, "create", acts[0].Action)

	// 空标题拒绝
	_, err = svc.Create(ctx, 1, "u1", p.ID, CreateItemInput{Title: "  "})
	require.ErrorIs(t, err, ErrSopItemTitleRequired)
}

func TestItemStatusTransitionGuard(t *testing.T) {
	svc, db := newItemTestService(t)
	ctx := context.Background()
	it := seedItem(t, db, types.SopItemStatusBacklog)

	require.Error(t, svc.UpdateStatus(ctx, 1, it.ID, types.SopItemStatusDone), "backlog 不能直达 done")
	require.NoError(t, svc.UpdateStatus(ctx, 1, it.ID, types.SopItemStatusTodo))
	require.NoError(t, svc.UpdateStatus(ctx, 1, it.ID, types.SopItemStatusInProgress))
	require.NoError(t, svc.UpdateStatus(ctx, 1, it.ID, types.SopItemStatusInReview))

	got, err := svc.Get(ctx, 1, it.ID)
	require.NoError(t, err)
	require.Equal(t, types.SopItemStatusInReview, got.Status)
	require.Error(t, svc.UpdateStatus(ctx, 1, "nope", types.SopItemStatusDone), "不存在的事项也要拒绝")
}

func TestListWithDerivedExecState(t *testing.T) {
	svc, db := newItemTestService(t)
	ctx := context.Background()
	it := seedItem(t, db, types.SopItemStatusInProgress)
	require.NoError(t, repository.NewSopRunRepo(db).CreateRun(ctx, &types.SopRun{
		TenantID: 1, ProjectID: it.ProjectID, ItemID: it.ID, Status: types.SopRunWaiting,
		WorkflowSnapshot: types.SopDefinition{Nodes: []types.SopNode{
			{ID: "n1", Name: "方案确认"}, {ID: "n2", Name: "实现"}},
		},
	}))
	run, err := svc.runs.ListRuns(ctx, 1, types.SopRunFilter{ItemID: it.ID}, 1, 1)
	require.NoError(t, err)
	require.Len(t, run, 1)
	require.NoError(t, svc.runs.CreateNodeRun(ctx, &types.SopNodeRun{
		TenantID: 1, RunID: run[0].ID, NodeID: "n1", Status: types.SopNodeSucceeded,
	}))
	require.NoError(t, svc.runs.CreateNodeRun(ctx, &types.SopNodeRun{
		TenantID: 1, RunID: run[0].ID, NodeID: "n2", Status: types.SopNodeRunning,
	}))

	list, err := svc.ListWithDerived(ctx, 1, it.ProjectID, types.SopItemFilter{})
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, types.SopExecStateIntervene, list[0].ExecState, "waiting → 需要介入")
	require.Equal(t, "实现", list[0].CurrentNode, "运行中的节点优先展示")
	require.Equal(t, 1, list[0].ProgressDone)
	require.Equal(t, 2, list[0].ProgressTotal)
}

// 没有运行时执行情况是 none，进度为 0/0（不能把「没跑过」算成 0% 的进行中）。
func TestListWithDerivedWithoutRun(t *testing.T) {
	svc, db := newItemTestService(t)
	it := seedItem(t, db, types.SopItemStatusTodo)
	list, err := svc.ListWithDerived(context.Background(), 1, it.ProjectID, types.SopItemFilter{})
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, types.SopExecStateNone, list[0].ExecState)
	require.Zero(t, list[0].ProgressTotal)
	require.Empty(t, list[0].CurrentNode)
}

// 批量迁移：执行中的事项跳过、状态机不允许的进 skipped，其余成功。
func TestBulkUpdateStatusSkipsRunningAndIllegal(t *testing.T) {
	svc, db := newItemTestService(t)
	ctx := context.Background()
	todo := seedItem(t, db, types.SopItemStatusTodo)
	running := seedItem(t, db, types.SopItemStatusInProgress)
	backlog := seedItem(t, db, types.SopItemStatusBacklog)
	require.NoError(t, repository.NewSopRunRepo(db).CreateRun(ctx, &types.SopRun{
		TenantID: 1, ProjectID: running.ProjectID, ItemID: running.ID, Status: types.SopRunRunning,
	}))

	res, err := svc.BulkUpdateStatus(ctx, 1, "u1",
		[]string{todo.ID, running.ID, backlog.ID}, types.SopItemStatusCancelled)
	require.NoError(t, err)
	require.Equal(t, []string{todo.ID, backlog.ID}, res.Updated,
		"todo 与 backlog 都可直接取消")
	require.Equal(t, []string{running.ID}, res.Skipped, "执行中的事项跳过")

	got, _ := svc.Get(ctx, 1, running.ID)
	require.Equal(t, types.SopItemStatusInProgress, got.Status, "跳过的事项状态不变")
}

func TestItemAttachmentWhitelistAndText(t *testing.T) {
	svc, db := newItemTestService(t)
	ctx := context.Background()
	it := seedItem(t, db, types.SopItemStatusBacklog)

	a, err := svc.AddAttachment(ctx, 1, "u1", it.ID, "需求.md", "", []byte("# 需求正文"))
	require.NoError(t, err)
	require.Equal(t, "需求.md", a.Name)
	require.Contains(t, a.TextContent, "需求正文", "文本类附件抽取正文")
	require.Nil(t, a.Data, "响应不回传文件正文")

	_, err = svc.AddAttachment(ctx, 1, "u1", it.ID, "evil.exe", "", []byte("x"))
	require.ErrorIs(t, err, ErrSopAttachmentType)

	_, err = svc.AddAttachment(ctx, 1, "u1", it.ID, "empty.md", "", nil)
	require.ErrorIs(t, err, ErrSopAttachmentEmpty)

	// 路径穿越的文件名只取 basename
	b, err := svc.AddAttachment(ctx, 1, "u1", it.ID, "../../etc/passwd.md", "", []byte("x"))
	require.NoError(t, err)
	require.Equal(t, "passwd.md", b.Name)

	list, err := svc.ListAttachments(ctx, 1, it.ID)
	require.NoError(t, err)
	require.Len(t, list, 2)
	for _, item := range list {
		require.False(t, len(item.Data) > 0, "列表不得回传正文")
	}
}

func TestClarificationAnswerIsOneWay(t *testing.T) {
	svc, db := newItemTestService(t)
	ctx := context.Background()
	it := seedItem(t, db, types.SopItemStatusBacklog)

	c, err := svc.AddClarification(ctx, 1, it.ID, "为什么要做？", "ai")
	require.NoError(t, err)
	require.Equal(t, types.SopClarificationStatusOpen, c.Status)
	require.Equal(t, "ai", c.Source)
	require.Nil(t, c.AnsweredAt)

	require.NoError(t, svc.AnswerClarification(ctx, 1, it.ID, c.ID, "因为要上线"))
	list, err := svc.ListClarifications(ctx, 1, it.ID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, types.SopClarificationStatusAnswered, list[0].Status)
	require.Equal(t, "因为要上线", list[0].Answer)
	require.NotNil(t, list[0].AnsweredAt)

	require.ErrorIs(t, svc.AnswerClarification(ctx, 1, it.ID, c.ID, "再答一次"), ErrSopClarificationOpen,
		"二次答复必须被拒（单向迁移）")
	require.Error(t, svc.AnswerClarification(ctx, 1, it.ID, c.ID, " "), "空答复拒绝")
}

// 大项下有子项时不允许删除（避免孤立子项）。
func TestDeleteBigItemWithChildrenRejected(t *testing.T) {
	svc, db := newItemTestService(t)
	ctx := context.Background()
	base := seedItem(t, db, types.SopItemStatusTodo)
	// is_big 是创建态字段（T2 定案：Update 只写可编辑白名单），必须建的时候就给。
	parent, err := svc.Create(ctx, 1, "u1", base.ProjectID, CreateItemInput{Title: "大项", IsBig: true})
	require.NoError(t, err)
	_, err = svc.Create(ctx, 1, "u1", parent.ProjectID, CreateItemInput{Title: "子项", ParentID: parent.ID})
	require.NoError(t, err)

	err = svc.Delete(ctx, 1, "u1", parent.ID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "子项")

	// 父项指向不存在的父事项时创建失败
	_, err = svc.Create(ctx, 1, "u1", parent.ProjectID, CreateItemInput{Title: "孤儿", ParentID: "nope"})
	require.Error(t, err)
}

func TestUpdateKeepsStatus(t *testing.T) {
	svc, db := newItemTestService(t)
	ctx := context.Background()
	it := seedItem(t, db, types.SopItemStatusInProgress)

	title := "新标题"
	updated, err := svc.Update(ctx, 1, "u1", it.ID, UpdateItemInput{Title: &title})
	require.NoError(t, err)
	require.Equal(t, "新标题", updated.Title)
	require.Equal(t, types.SopItemStatusInProgress, updated.Status, "普通编辑不得改状态")

	empty := " "
	_, err = svc.Update(ctx, 1, "u1", it.ID, UpdateItemInput{Title: &empty})
	require.ErrorIs(t, err, ErrSopItemTitleRequired)
}

// 全部子项进入终态后父项自动推进为 done；仍有未完成子项时不动。
func TestParentAutoAdvancesWhenAllChildrenDone(t *testing.T) {
	svc, db := newItemTestService(t)
	ctx := context.Background()
	p := &types.SopProject{TenantID: 1, Name: "P", Code: "TP"}
	require.NoError(t, repository.NewSopProjectRepo(db).Create(ctx, p))

	parent, err := svc.Create(ctx, 1, "u1", p.ID, CreateItemInput{Title: "大需求", IsBig: true})
	require.NoError(t, err)
	kids, err := svc.Split(ctx, 1, "u1", parent.ID, []types.SopChildItemSpec{
		{Title: "子一"}, {Title: "子二"},
	}, types.SopSplitSourceManual)
	require.NoError(t, err)
	require.Len(t, kids, 2)

	for _, to := range []string{types.SopItemStatusTodo, types.SopItemStatusInProgress,
		types.SopItemStatusInReview, types.SopItemStatusDone} {
		require.NoError(t, svc.UpdateStatus(ctx, 1, kids[0].ID, to))
	}
	got, err := svc.Get(ctx, 1, parent.ID)
	require.NoError(t, err)
	require.NotEqual(t, types.SopItemStatusDone, got.Status, "还有未完成子项时父项不动")

	for _, to := range []string{types.SopItemStatusTodo, types.SopItemStatusCancelled, types.SopItemStatusTodo,
		types.SopItemStatusInProgress, types.SopItemStatusInReview, types.SopItemStatusDone} {
		require.NoError(t, svc.UpdateStatus(ctx, 1, kids[1].ID, to))
	}
	got, err = svc.Get(ctx, 1, parent.ID)
	require.NoError(t, err)
	require.Equal(t, types.SopItemStatusDone, got.Status, "全部子项 done/closed 时父项自动 done")
}

// 三级嵌套拒绝、空清单拒绝、超限拒绝；子项编号与独立事项同一序列。
func TestSplitGuardsAndNumbering(t *testing.T) {
	svc, db := newItemTestService(t)
	ctx := context.Background()
	p := &types.SopProject{TenantID: 1, Name: "P", Code: "TP"}
	require.NoError(t, repository.NewSopProjectRepo(db).Create(ctx, p))

	standalone, err := svc.Create(ctx, 1, "u1", p.ID, CreateItemInput{Title: "独立"})
	require.NoError(t, err)
	parent, err := svc.Create(ctx, 1, "u1", p.ID, CreateItemInput{Title: "大需求"})
	require.NoError(t, err)

	_, err = svc.Split(ctx, 1, "u1", parent.ID, nil, "")
	require.ErrorIs(t, err, ErrSopItemSplitEmpty)

	specs := make([]types.SopChildItemSpec, 21)
	for i := range specs {
		specs[i] = types.SopChildItemSpec{Title: fmt.Sprintf("子%d", i)}
	}
	_, err = svc.Split(ctx, 1, "u1", parent.ID, specs, "")
	require.ErrorIs(t, err, ErrSopItemSplitTooMany)

	kids, err := svc.Split(ctx, 1, "u1", parent.ID, []types.SopChildItemSpec{{Title: "子一"}}, "")
	require.NoError(t, err)
	// 编号序列：独立(TP-1) → 大需求(TP-2) → 超限尝试不占号 → 子一(TP-3)
	require.Equal(t, "TP-3", kids[0].No, "子项编号与独立事项同一序列")

	_, err = svc.Split(ctx, 1, "u1", kids[0].ID, []types.SopChildItemSpec{{Title: "三级"}}, "")
	require.ErrorIs(t, err, ErrSopItemSplitTooDeep)

	listed, err := svc.ListChildren(ctx, 1, parent.ID)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, types.SopSplitSourceManual, listed[0].SplitSource)

	require.NoError(t, svc.DetachChild(ctx, 1, "u1", parent.ID, kids[0].ID))
	detached, err := svc.Get(ctx, 1, kids[0].ID)
	require.NoError(t, err)
	require.Empty(t, detached.ParentID)
	require.Empty(t, detached.SplitSource)
	_ = standalone
}

// 大项与已有子项的父项都不能直接触发。
func TestEnsureNotBigParent(t *testing.T) {
	svc, db := newItemTestService(t)
	ctx := context.Background()
	p := &types.SopProject{TenantID: 1, Name: "P", Code: "TP"}
	require.NoError(t, repository.NewSopProjectRepo(db).Create(ctx, p))

	big, err := svc.Create(ctx, 1, "u1", p.ID, CreateItemInput{Title: "大项", IsBig: true})
	require.NoError(t, err)
	require.ErrorIs(t, svc.EnsureNotBigParent(ctx, 1, big.ID), ErrSopItemParentIsBig)

	plain, err := svc.Create(ctx, 1, "u1", p.ID, CreateItemInput{Title: "普通"})
	require.NoError(t, err)
	require.NoError(t, svc.EnsureNotBigParent(ctx, 1, plain.ID))

	_, err = svc.Split(ctx, 1, "u1", plain.ID, []types.SopChildItemSpec{{Title: "子"}}, "")
	require.NoError(t, err)
	require.ErrorIs(t, svc.EnsureNotBigParent(ctx, 1, plain.ID), ErrSopItemParentIsBig,
		"被拆过的父项同样不能直接触发")
}

// 列表里大项带子项汇总：执行情况按子项算，空容器=none。
func TestListRollsUpParentExec(t *testing.T) {
	svc, db := newItemTestService(t)
	ctx := context.Background()
	p := &types.SopProject{TenantID: 1, Name: "P", Code: "TP"}
	require.NoError(t, repository.NewSopProjectRepo(db).Create(ctx, p))

	empty, err := svc.Create(ctx, 1, "u1", p.ID, CreateItemInput{Title: "空容器", IsBig: true})
	require.NoError(t, err)
	full, err := svc.Create(ctx, 1, "u1", p.ID, CreateItemInput{Title: "有子项"})
	require.NoError(t, err)
	kids, err := svc.Split(ctx, 1, "u1", full.ID, []types.SopChildItemSpec{
		{Title: "子一"}, {Title: "子二"},
	}, "")
	require.NoError(t, err)
	for _, to := range []string{types.SopItemStatusTodo, types.SopItemStatusInProgress,
		types.SopItemStatusInReview, types.SopItemStatusDone, types.SopItemStatusClosed} {
		require.NoError(t, svc.UpdateStatus(ctx, 1, kids[0].ID, to))
	}

	list, err := svc.ListWithDerived(ctx, 1, p.ID, types.SopItemFilter{})
	require.NoError(t, err)
	byID := map[string]types.SopItemWithDerived{}
	for _, it := range list {
		byID[it.ID] = it
	}
	require.Equal(t, types.SopExecStateNone, byID[empty.ID].ChildExec, "空容器执行情况=none")
	require.Zero(t, byID[empty.ID].ChildTotal)
	require.Equal(t, 2, byID[full.ID].ChildTotal)
	require.Equal(t, 1, byID[full.ID].ChildDone)
	require.NotEmpty(t, byID[full.ID].ChildExec)
}
