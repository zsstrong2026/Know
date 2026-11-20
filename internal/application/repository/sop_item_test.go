package repository

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Tencent/WeKnora/internal/types"
)

// sopItemSQLCapture 记录 GORM 实际下发的 SQL，用于断言 UPDATE 的 SET 列清单。
type sopItemSQLCapture struct {
	logger.Interface
	mu   sync.Mutex
	sqls []string
}

func (l *sopItemSQLCapture) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	sql, _ := fc()
	l.mu.Lock()
	l.sqls = append(l.sqls, sql)
	l.mu.Unlock()
}

func (l *sopItemSQLCapture) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.sqls))
	copy(out, l.sqls)
	return out
}

// sopItemUpdateStatement 从捕获的 SQL 中取出对 sop_items 的 UPDATE 语句。
func sopItemUpdateStatement(t *testing.T, sqls []string) string {
	t.Helper()
	for _, sql := range sqls {
		if strings.Contains(sql, "UPDATE `sop_items`") {
			return sql
		}
	}
	t.Fatalf("未捕获到 UPDATE sop_items 语句：%s", strings.Join(sqls, "\n"))
	return ""
}

// sopItemSetColumns 解析 UPDATE ... SET 段的列名（按生成顺序）。
// 只依赖反引号包裹的列名，不受参数值内逗号/引号影响。
var sopItemSetColumnRe = regexp.MustCompile("(`[a-z_]+`)\\s*=")

func sopItemSetColumns(t *testing.T, updateSQL string) []string {
	t.Helper()
	upper := strings.ToUpper(updateSQL)
	setIdx := strings.Index(upper, " SET ")
	whereIdx := strings.Index(upper, " WHERE ")
	require.GreaterOrEqual(t, setIdx, 0, "sql:\n%s", updateSQL)
	require.Greater(t, whereIdx, setIdx, "sql:\n%s", updateSQL)
	matches := sopItemSetColumnRe.FindAllStringSubmatch(updateSQL[setIdx+len(" SET "):whereIdx], -1)
	cols := make([]string, 0, len(matches))
	for _, m := range matches {
		cols = append(cols, strings.Trim(m[1], "`"))
	}
	return cols
}

// newSopItemTestDB 建独立 SQLite 内存库：AutoMigrate 建模型表后按迁移脚本
// 原样补部分唯一索引（GORM tag 表达不了 WHERE deleted_at IS NULL）。
func newSopItemTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:sopitemtest_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	require.NoError(t, db.AutoMigrate(
		&types.SopProject{}, &types.SopItem{}, &types.SopItemAttachment{},
	))
	require.NoError(t, db.Exec(
		`CREATE UNIQUE INDEX ux_sop_items_project_no ON sop_items (project_id, no) WHERE deleted_at IS NULL`,
	).Error)
	return db
}

func seedSopItem(t *testing.T, repo *SopItemRepo, tenantID uint64, projectID, no, title string) *types.SopItem {
	t.Helper()
	it := &types.SopItem{TenantID: tenantID, ProjectID: projectID, No: no, Title: title}
	require.NoError(t, repo.Create(context.Background(), it))
	return it
}

// 编号唯一性是项目内唯一：同项目重复 no 必须被索引挡住，跨项目同 no 允许。
func TestItemNoUniquePerProject(t *testing.T) {
	db := newSopItemTestDB(t)
	repo := NewSopItemRepo(db)
	ctx := context.Background()

	seedSopItem(t, repo, 1, "p1", "TP-1", "first")
	err := repo.Create(ctx, &types.SopItem{TenantID: 1, ProjectID: "p1", No: "TP-1", Title: "dup"})
	require.Error(t, err, "同项目重复编号必须失败")

	it := &types.SopItem{TenantID: 1, ProjectID: "p2", No: "TP-1", Title: "same no other project"}
	require.NoError(t, repo.Create(ctx, it), "跨项目同编号允许")
}

// 非法迁移不得落库：backlog→done 被拒绝，且库里仍是 backlog。
func TestUpdateStatusIllegalRejected(t *testing.T) {
	db := newSopItemTestDB(t)
	repo := NewSopItemRepo(db)
	ctx := context.Background()
	it := seedSopItem(t, repo, 1, "p1", "TP-1", "t")

	err := repo.UpdateStatus(ctx, 1, it.ID, types.SopItemStatusDone)
	require.ErrorIs(t, err, ErrSopItemIllegalTransition)
	require.Contains(t, err.Error(), "backlog -> done", "错误信息需带两端状态便于排查")

	got, err := repo.GetByID(ctx, 1, it.ID)
	require.NoError(t, err)
	require.Equal(t, types.SopItemStatusBacklog, got.Status, "非法迁移后状态不得改变")

	require.NoError(t, repo.UpdateStatus(ctx, 1, it.ID, types.SopItemStatusTodo))
	require.NoError(t, repo.UpdateStatus(ctx, 1, it.ID, types.SopItemStatusTodo), "同状态为幂等 no-op")
	got, err = repo.GetByID(ctx, 1, it.ID)
	require.NoError(t, err)
	require.Equal(t, types.SopItemStatusTodo, got.Status)

	require.ErrorIs(t, repo.UpdateStatus(ctx, 2, it.ID, types.SopItemStatusInProgress), gorm.ErrRecordNotFound,
		"跨租户不可迁移")
}

// Update 不得改状态：状态唯一写入口是 UpdateStatus。
// 只断言「回读状态未变」不足以钉住缺陷（旧实现读旧值写回也满足），
// 因此同时断言生成的 UPDATE 语句的 SET 列清单里没有 status。
func TestUpdateKeepsStatus(t *testing.T) {
	db := newSopItemTestDB(t)
	repo := NewSopItemRepo(db)
	ctx := context.Background()
	it := seedSopItem(t, repo, 1, "p1", "TP-1", "t")

	captured := &sopItemSQLCapture{Interface: logger.Discard}
	db.Logger = captured

	it.Title = "改标题"
	it.Status = types.SopItemStatusClosed // 试图绕过状态机
	require.NoError(t, repo.Update(ctx, it))

	updateSQL := sopItemUpdateStatement(t, captured.snapshot())
	require.NotContains(t, updateSQL, "`status`", "UPDATE 语句任何位置都不得出现 status 列：\n%s", updateSQL)
	cols := sopItemSetColumns(t, updateSQL)
	require.NotContains(t, cols, "status", "SET 列清单不得包含 status：%v", cols)

	got, err := repo.GetByID(ctx, 1, it.ID)
	require.NoError(t, err)
	require.Equal(t, "改标题", got.Title)
	require.Equal(t, types.SopItemStatusBacklog, got.Status, "Update 不得写状态")
}

// Update 的可编辑列白名单：项目归属与两级拆分结构、运行绑定、审计列都不得被零值 DTO 冲掉。
func TestUpdateProtectsStructuralColumns(t *testing.T) {
	db := newSopItemTestDB(t)
	repo := NewSopItemRepo(db)
	ctx := context.Background()

	parent := &types.SopItem{
		TenantID: 1, ProjectID: "p1", No: "TP-0", Title: "大项", IsBig: true,
		CreatedBy: "u-0",
	}
	require.NoError(t, repo.Create(ctx, parent))

	// 结构列全部在 Create 时落库（Update 已不允许写这些列）。
	child := &types.SopItem{
		TenantID: 1, ProjectID: "p1", No: "TP-1", Title: "原标题",
		ParentID: parent.ID, SplitSource: "ai", LastRunID: "run-1",
		CreatedBy: "u-1",
	}
	require.NoError(t, repo.Create(ctx, child))

	captured := &sopItemSQLCapture{Interface: logger.Discard}
	db.Logger = captured

	// 模拟「用部分 DTO 构造的入参」：只带 id/tenant/标题，结构列全为零值。
	partial := &types.SopItem{ID: child.ID, TenantID: child.TenantID, Title: "新标题"}
	require.NoError(t, repo.Update(ctx, partial))

	updateSQL := sopItemUpdateStatement(t, captured.snapshot())
	cols := sopItemSetColumns(t, updateSQL)
	require.ElementsMatch(t, []string{
		"title", "description", "kind", "priority", "source", "proposer", "owner",
		"tags", "template_id", "kb_id", "knowledge_id", "outcome", "domain_data", "updated_at",
	}, cols, "SET 列必须是可编辑字段白名单：\n%s", updateSQL)
	for _, forbidden := range []string{
		"status", "project_id", "no", "is_big", "parent_id", "split_source",
		"last_run_id", "created_by", "tenant_id", "id", "created_at", "deleted_at",
	} {
		require.NotContains(t, cols, forbidden, "SET 列不得包含 %s：%v", forbidden, cols)
	}

	got, err := repo.GetByID(ctx, 1, child.ID)
	require.NoError(t, err)
	require.Equal(t, "新标题", got.Title, "可编辑列照常写入")
	require.Equal(t, "p1", got.ProjectID, "不得被重挂到其他项目")
	require.Equal(t, "TP-1", got.No, "编号不得被清空/改写")
	require.Equal(t, parent.ID, got.ParentID, "父子关系不得被清空")
	require.Equal(t, "ai", got.SplitSource, "拆分来源不得被清空")
	require.Equal(t, "run-1", got.LastRunID, "运行绑定不得被清空")
	require.Equal(t, "u-1", got.CreatedBy, "创建人不得被清空")
	require.Equal(t, uint64(1), got.TenantID)

	gotParent, err := repo.GetByID(ctx, 1, parent.ID)
	require.NoError(t, err)
	require.True(t, gotParent.IsBig, "大项标记不得被零值冲掉")
}

// q 过滤器中的 % 与 _ 必须按字面量匹配：未被转义时它们会退化成通配符而命中无关行。
func TestListWithDerivedLikeEscaping(t *testing.T) {
	db := newSopItemTestDB(t)
	repo := NewSopItemRepo(db)
	ctx := context.Background()

	pctHit := seedSopItem(t, repo, 1, "p1", "TP-1", "覆盖率 100% 达成")
	pctMiss := seedSopItem(t, repo, 1, "p1", "TP-2", "预算 1000 元")
	underHit := seedSopItem(t, repo, 1, "p1", "TP-3", "字段 a_b 说明")
	underMiss := seedSopItem(t, repo, 1, "p1", "TP-4", "字段 axb 说明")

	ids := func(q string) []string {
		t.Helper()
		list, err := repo.ListWithDerived(ctx, 1, "p1", types.SopItemFilter{Q: q}, nil, nil)
		require.NoError(t, err)
		out := make([]string, 0, len(list))
		for _, x := range list {
			out = append(out, x.ID)
		}
		return out
	}

	// 未转义时 "100%" 的 LIKE 模式 "%100%%" 会把 "1000" 也命中。
	require.Equal(t, []string{pctHit.ID}, ids("100%"))
	// 未转义时 "a_b" 的 LIKE 模式 "%a_b%" 会把 "axb" 也命中。
	require.Equal(t, []string{underHit.ID}, ids("a_b"))
	// 纯数字前缀仍可命中含该字面量的行（正向对照）。
	require.Equal(t, []string{pctMiss.ID}, ids("1000"))
	require.Equal(t, []string{underMiss.ID}, ids("axb"))
}

// 执行情况过滤在派生字段组装后进行；同时校验派生字段本身。
func TestListWithDerivedExecFilter(t *testing.T) {
	db := newSopItemTestDB(t)
	repo := NewSopItemRepo(db)
	ctx := context.Background()

	none := seedSopItem(t, repo, 1, "p1", "TP-1", "无运行")
	running := seedSopItem(t, repo, 1, "p1", "TP-2", "运行中")
	waiting := seedSopItem(t, repo, 1, "p1", "TP-3", "待确认")
	pr := seedSopItem(t, repo, 1, "p1", "TP-4", "待处理 PR")
	require.NoError(t, repo.UpdateStatus(ctx, 1, pr.ID, types.SopItemStatusTodo))
	require.NoError(t, repo.UpdateStatus(ctx, 1, pr.ID, types.SopItemStatusInProgress))
	require.NoError(t, repo.UpdateStatus(ctx, 1, pr.ID, types.SopItemStatusInReview))

	runs := map[string]string{
		running.ID: "running",
		waiting.ID: "waiting",
		pr.ID:      "succeeded",
	}
	nodes := map[string][]string{
		running.ID: {"succeeded|analyze", "running|implement", "pending|review"},
		waiting.ID: {"succeeded|analyze", "waiting_approval|approve"},
		pr.ID:      {"succeeded|analyze", "succeeded|implement"},
	}

	wantByExec := map[string]string{
		"none":      none.ID,
		"running":   running.ID,
		"intervene": waiting.ID,
		"pr":        pr.ID,
	}
	for exec, wantID := range wantByExec {
		list, err := repo.ListWithDerived(ctx, 1, "p1", types.SopItemFilter{Exec: exec}, runs, nodes)
		require.NoError(t, err, "exec=%s", exec)
		require.Len(t, list, 1, "exec=%s 只应命中一条", exec)
		require.Equal(t, wantID, list[0].ID, "exec=%s", exec)
		require.Equal(t, exec, list[0].ExecState)
	}

	all, err := repo.ListWithDerived(ctx, 1, "p1", types.SopItemFilter{}, runs, nodes)
	require.NoError(t, err)
	require.Len(t, all, 4)

	byID := map[string]types.SopItemWithDerived{}
	for _, x := range all {
		byID[x.ID] = x
	}
	require.Equal(t, "implement", byID[running.ID].CurrentNode)
	require.Equal(t, 1, byID[running.ID].ProgressDone)
	require.Equal(t, 3, byID[running.ID].ProgressTotal)
	require.Equal(t, "approve", byID[waiting.ID].CurrentNode)
	require.Equal(t, "pr", byID[pr.ID].ExecState)
	require.Empty(t, byID[none.ID].CurrentNode)
	require.Equal(t, 0, byID[none.ID].ProgressTotal)
}

// done/closed 事项排序语义：当前环节=已完成/已关闭，exec 归 closed。
func TestListWithDerivedClosedItems(t *testing.T) {
	db := newSopItemTestDB(t)
	repo := NewSopItemRepo(db)
	ctx := context.Background()

	done := seedSopItem(t, repo, 1, "p1", "TP-1", "done")
	for _, to := range []string{types.SopItemStatusTodo, types.SopItemStatusInProgress,
		types.SopItemStatusInReview, types.SopItemStatusDone} {
		require.NoError(t, repo.UpdateStatus(ctx, 1, done.ID, to))
	}

	list, err := repo.ListWithDerived(ctx, 1, "p1", types.SopItemFilter{},
		map[string]string{done.ID: "failed"}, nil)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "closed", list[0].ExecState, "已闭环优先于运行态")
	require.Equal(t, "已完成", list[0].CurrentNode)
}

// 基础过滤：q 命中标题/编号/描述，status/owner/source/kind/parent 精确，分页在 exec 过滤后生效。
func TestListWithDerivedBasicFilters(t *testing.T) {
	db := newSopItemTestDB(t)
	repo := NewSopItemRepo(db)
	ctx := context.Background()

	a := seedSopItem(t, repo, 1, "p1", "TP-1", "批量导出 PDF")
	a.Description = "支持目录导出"
	a.Owner = "林晓"
	a.Source = "customer"
	a.Kind = "requirement"
	require.NoError(t, repo.Update(ctx, a))

	b := seedSopItem(t, repo, 1, "p1", "TP-2", "权限体系")
	b.Kind = "task"
	b.Source = "internal"
	require.NoError(t, repo.Update(ctx, b))

	// parent_id / split_source 属结构列，Update 不写（见 Update 白名单），故在 Create 时落库。
	child := &types.SopItem{
		TenantID: 1, ProjectID: "p1", No: "TP-3", Title: "权限模型",
		ParentID: b.ID, SplitSource: "manual",
	}
	require.NoError(t, repo.Create(ctx, child))

	// 另一个项目的同名编号/相近标题不得混进 p1 的列表（下面每次 check 都断言完整 id 序列）。
	seedSopItem(t, repo, 1, "p2", "TP-1", "权限体系的另一个项目版本")

	check := func(f types.SopItemFilter, want ...string) {
		t.Helper()
		list, err := repo.ListWithDerived(ctx, 1, "p1", f, nil, nil)
		require.NoError(t, err)
		ids := make([]string, 0, len(list))
		for _, x := range list {
			ids = append(ids, x.ID)
		}
		require.Equal(t, want, ids, "filter=%+v", f)
	}

	check(types.SopItemFilter{Q: "权限"}, child.ID, b.ID)                                  // 标题命中
	check(types.SopItemFilter{Q: "TP-1"}, a.ID)                                          // 编号命中
	check(types.SopItemFilter{Q: "目录"}, a.ID)                                            // 描述命中
	check(types.SopItemFilter{Owner: "林晓"}, a.ID)                                        // 责任人
	check(types.SopItemFilter{Source: "customer"}, a.ID)                                 // 来源
	check(types.SopItemFilter{Kind: "task"}, b.ID)                                       // 类型
	check(types.SopItemFilter{ParentID: &b.ID}, child.ID)                                // 父项过滤
	check(types.SopItemFilter{Status: types.SopItemStatusBacklog}, child.ID, b.ID, a.ID) // newest first
	check(types.SopItemFilter{Page: 1, PageSize: 2}, child.ID, b.ID)                     // 分页
	check(types.SopItemFilter{Page: 2, PageSize: 2}, a.ID)
}

// 批量改状态：running 派生事项跳过、非法迁移跳过、合法迁移逐条落库；跨租户不可见。
func TestBulkUpdateStatus(t *testing.T) {
	db := newSopItemTestDB(t)
	repo := NewSopItemRepo(db)
	ctx := context.Background()

	ok := seedSopItem(t, repo, 1, "p1", "TP-1", "可关闭")
	busy := seedSopItem(t, repo, 1, "p1", "TP-2", "运行中")
	illegal := seedSopItem(t, repo, 1, "p1", "TP-3", "非法迁移")
	cancelled := seedSopItem(t, repo, 1, "p1", "TP-4", "已取消")
	// ok 先走到 done，closed 只允许从 done 进入；busy 停在 in_progress 并被标记运行中跳过。
	for _, to := range []string{types.SopItemStatusTodo, types.SopItemStatusInProgress,
		types.SopItemStatusInReview, types.SopItemStatusDone} {
		require.NoError(t, repo.UpdateStatus(ctx, 1, ok.ID, to))
	}
	require.NoError(t, repo.UpdateStatus(ctx, 1, busy.ID, types.SopItemStatusTodo))
	require.NoError(t, repo.UpdateStatus(ctx, 1, busy.ID, types.SopItemStatusInProgress))
	require.NoError(t, repo.UpdateStatus(ctx, 1, cancelled.ID, types.SopItemStatusCancelled))

	other := seedSopItem(t, repo, 2, "p-other-tenant", "TP-1", "别租户")

	updated, skipped := repo.BulkUpdateStatus(ctx, 1,
		[]string{ok.ID, busy.ID, illegal.ID, cancelled.ID, other.ID, "missing"},
		types.SopItemStatusClosed,
		map[string]bool{busy.ID: true})
	require.Equal(t, []string{ok.ID}, updated)
	require.ElementsMatch(t, []string{busy.ID, illegal.ID, cancelled.ID, other.ID, "missing"}, skipped)

	got, err := repo.GetByID(ctx, 1, ok.ID)
	require.NoError(t, err)
	require.Equal(t, types.SopItemStatusClosed, got.Status)
	got, err = repo.GetByID(ctx, 1, busy.ID)
	require.NoError(t, err)
	require.Equal(t, types.SopItemStatusInProgress, got.Status, "running 事项必须保留原状态")
	got, err = repo.GetByID(ctx, 2, other.ID)
	require.NoError(t, err)
	require.Equal(t, types.SopItemStatusBacklog, got.Status, "跨租户事项不得被改写")
}

// 附件：字节与抽取文本落库；列表不回传 data。
func TestSopItemAttachments(t *testing.T) {
	db := newSopItemTestDB(t)
	repo := NewSopItemRepo(db)
	ctx := context.Background()
	it := seedSopItem(t, repo, 1, "p1", "TP-1", "带附件")

	require.NoError(t, repo.AddAttachment(ctx, &types.SopItemAttachment{
		TenantID: 1, ItemID: it.ID, Name: "spec.md", MediaType: "text/markdown",
		Size: 5, Data: []byte("hello"), TextContent: "hello",
	}))
	require.NoError(t, repo.AddAttachment(ctx, &types.SopItemAttachment{
		TenantID: 1, ItemID: it.ID, Name: "pic.png", MediaType: "image/png", Size: 3, Data: []byte{1, 2, 3},
	}))

	list, err := repo.ListAttachments(ctx, 1, it.ID)
	require.NoError(t, err)
	require.Len(t, list, 2)
	require.Equal(t, "spec.md", list[0].Name)
	require.Nil(t, list[0].Data, "列表不取文件正文")
	require.Equal(t, "hello", list[0].TextContent)

	empty, err := repo.ListAttachments(ctx, 2, it.ID)
	require.NoError(t, err)
	require.Empty(t, empty, "跨租户不可见")
}

// 租户隔离与软删：GetByID/Update/Delete/ListByProjectNo 一律按 tenant_id 过滤。
func TestSopItemTenantScopeAndDelete(t *testing.T) {
	db := newSopItemTestDB(t)
	repo := NewSopItemRepo(db)
	ctx := context.Background()
	it := seedSopItem(t, repo, 1, "p1", "TP-1", "t")

	_, err := repo.GetByID(ctx, 2, it.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	cross := *it
	cross.TenantID = 2
	cross.Title = "hijacked"
	require.ErrorIs(t, repo.Update(ctx, &cross), gorm.ErrRecordNotFound)
	got, err := repo.GetByID(ctx, 1, it.ID)
	require.NoError(t, err)
	require.Equal(t, "t", got.Title)

	require.ErrorIs(t, repo.Delete(ctx, 2, it.ID), gorm.ErrRecordNotFound)
	require.NoError(t, repo.Delete(ctx, 1, it.ID))
	_, err = repo.GetByID(ctx, 1, it.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	list, err := repo.ListByProjectNo(ctx, 1, "p1")
	require.NoError(t, err)
	require.Empty(t, list, "软删后不可见")
}
