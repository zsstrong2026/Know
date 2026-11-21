package repository

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Tencent/WeKnora/internal/types"
)

// newSopRunTestDB 建独立 SQLite 库（临时文件 + WAL + busy_timeout：CAS 抢占测试需要
// 真实并发写，共享缓存内存库会以 SQLITE_LOCKED 直接失败而非等待），
// AutoMigrate 建运行/节点运行/产物三张表后按迁移脚本补部分索引
// （GORM tag 表达不了 WHERE deleted_at IS NULL 的复合索引）。
func newSopRunTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "sop_run.db") + "?_journal_mode=WAL&_busy_timeout=5000"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	require.NoError(t, db.AutoMigrate(&types.SopRun{}, &types.SopNodeRun{}, &types.SopArtifact{}))
	// 迁移脚本里有、GORM tag 未表达的唯一索引与部分索引，按迁移原文补上。
	for _, ddl := range []string{
		"CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_node_runs_run_node ON sop_node_runs(run_id, node_id)",
		"CREATE INDEX IF NOT EXISTS idx_sop_runs_item_created ON sop_runs(item_id, created_at DESC) WHERE deleted_at IS NULL",
		"CREATE INDEX IF NOT EXISTS idx_sop_runs_project_status ON sop_runs(project_id, status) WHERE deleted_at IS NULL",
		"CREATE INDEX IF NOT EXISTS idx_sop_artifacts_run_node ON sop_artifacts(run_id, node_id)",
	} {
		require.NoError(t, db.Exec(ddl).Error)
	}
	return db
}

func TestClaimDueCAS(t *testing.T) {
	db := newSopRunTestDB(t)
	repo := NewSopRunRepo(db)
	ctx := context.Background()
	r := &types.SopRun{TenantID: 1, TemplateID: "t1", RevisionID: "rev1", Status: types.SopRunQueued}
	require.NoError(t, repo.CreateRun(ctx, r))
	lease := time.Now().Add(30 * time.Second)
	got, err := repo.ClaimDue(ctx, lease)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, r.ID, got.ID)
	require.Equal(t, types.SopRunRunning, got.Status)
	// 第二次抢不到（已被占）
	got2, err := repo.ClaimDue(ctx, lease)
	require.NoError(t, err)
	require.Nil(t, got2)
}

// 并发调度器不能双重认领：N 个 goroutine 同时 ClaimDue 抢一个 queued run，
// 只能有一个拿到，库中状态恰好流转一次。
func TestClaimDueNoDoubleClaim(t *testing.T) {
	db := newSopRunTestDB(t)
	repo := NewSopRunRepo(db)
	ctx := context.Background()
	const claimants = 8
	runs := make([]*types.SopRun, 0, claimants)
	for i := 0; i < claimants; i++ {
		r := &types.SopRun{TenantID: 1, Status: types.SopRunQueued}
		require.NoError(t, repo.CreateRun(ctx, r))
		runs = append(runs, r)
	}

	var wg sync.WaitGroup
	claimed := make(chan string, claimants*claimants)
	errs := make(chan error, claimants*claimants)
	for i := 0; i < claimants; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				got, err := repo.ClaimDue(ctx, time.Now().Add(30*time.Second))
				if err != nil {
					errs <- err
					return
				}
				if got == nil {
					return
				}
				claimed <- got.ID
			}
		}()
	}
	wg.Wait()
	close(claimed)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	seen := map[string]int{}
	for id := range claimed {
		seen[id]++
	}
	require.Len(t, seen, claimants, "每个 queued run 必须恰好被认领一次")
	for id, n := range seen {
		require.Equal(t, 1, n, "run %s 被认领了 %d 次", id, n)
	}
	var running int64
	require.NoError(t, db.Model(&types.SopRun{}).Where("status = ?", types.SopRunRunning).Count(&running).Error)
	require.EqualValues(t, claimants, running)
}

func TestExpireLeases(t *testing.T) {
	db := newSopRunTestDB(t)
	repo := NewSopRunRepo(db)
	ctx := context.Background()
	r := &types.SopRun{TenantID: 1, Status: types.SopRunRunning}
	require.NoError(t, repo.CreateRun(ctx, r))
	past := time.Now().Add(-time.Minute)
	require.NoError(t, repo.Heartbeat(ctx, r.ID, past)) // 伪造过期租约
	n, err := repo.ExpireLeases(ctx, time.Now())
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
	got, _ := repo.GetRun(ctx, 1, r.ID)
	require.Equal(t, types.SopRunFailed, got.Status)
	require.Contains(t, got.Error, "租约")
}

// 未过期租约与无租约的 queued 行不得被回收。
func TestExpireLeasesSkipsLiveAndQueued(t *testing.T) {
	db := newSopRunTestDB(t)
	repo := NewSopRunRepo(db)
	ctx := context.Background()
	live := &types.SopRun{TenantID: 1, Status: types.SopRunRunning}
	queued := &types.SopRun{TenantID: 1, Status: types.SopRunQueued}
	require.NoError(t, repo.CreateRun(ctx, live))
	require.NoError(t, repo.CreateRun(ctx, queued))
	require.NoError(t, repo.Heartbeat(ctx, live.ID, time.Now().Add(time.Minute)))

	n, err := repo.ExpireLeases(ctx, time.Now())
	require.NoError(t, err)
	require.Zero(t, n)
	for _, id := range []string{live.ID, queued.ID} {
		got, err := repo.GetRun(ctx, 1, id)
		require.NoError(t, err)
		require.NotEqual(t, types.SopRunFailed, got.Status)
	}
}

func TestResetNodes(t *testing.T) {
	db := newSopRunTestDB(t)
	repo := NewSopRunRepo(db)
	ctx := context.Background()
	for _, nid := range []string{"n1", "n2"} {
		nr := &types.SopNodeRun{TenantID: 1, RunID: "r1", NodeID: nid,
			Status: types.SopNodeSucceeded, Output: "old"}
		require.NoError(t, repo.CreateNodeRun(ctx, nr))
	}
	require.NoError(t, repo.ResetNodes(ctx, "r1", []string{"n1", "n2"}))
	ns, _ := repo.ListNodeRuns(ctx, 1, "r1")
	for _, n := range ns {
		require.Equal(t, types.SopNodePending, n.Status)
		require.Empty(t, n.Output)
		require.Empty(t, n.Error)
	}
}

// ResetNodes 只动指定 ids，未列出的节点保持原样。
func TestResetNodesScopedToIDs(t *testing.T) {
	db := newSopRunTestDB(t)
	repo := NewSopRunRepo(db)
	ctx := context.Background()
	require.NoError(t, repo.CreateNodeRun(ctx, &types.SopNodeRun{TenantID: 1, RunID: "r1",
		NodeID: "n1", Status: types.SopNodeSucceeded, Output: "a"}))
	require.NoError(t, repo.CreateNodeRun(ctx, &types.SopNodeRun{TenantID: 1, RunID: "r1",
		NodeID: "n2", Status: types.SopNodeSucceeded, Output: "b"}))
	require.NoError(t, repo.ResetNodes(ctx, "r1", []string{"n2"}))
	n1, _ := repo.GetNodeRun(ctx, 1, "r1", "n1")
	require.Equal(t, types.SopNodeSucceeded, n1.Status)
	require.Equal(t, "a", n1.Output)
	n2, _ := repo.GetNodeRun(ctx, 1, "r1", "n2")
	require.Equal(t, types.SopNodePending, n2.Status)
	require.Empty(t, n2.Output)
}

func TestStatsByStatus(t *testing.T) {
	db := newSopRunTestDB(t)
	repo := NewSopRunRepo(db)
	ctx := context.Background()
	for _, s := range []string{"succeeded", "succeeded", "failed", "running"} {
		require.NoError(t, repo.CreateRun(ctx, &types.SopRun{TenantID: 1, Status: s}))
	}
	m, err := repo.StatsByStatus(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, int64(2), m["succeeded"])
	require.Equal(t, int64(1), m["failed"])
}

// 统计口径：成功率只以已结束（succeeded+failed+cancelled）为分母；
// total_tokens 累计 usage；租户隔离。
func TestStatsSummary(t *testing.T) {
	db := newSopRunTestDB(t)
	repo := NewSopRunRepo(db)
	ctx := context.Background()
	mk := func(s string, tokens int64) {
		require.NoError(t, repo.CreateRun(ctx, &types.SopRun{
			TenantID: 1, ProjectID: "p1", Status: s,
			Usage: types.SopUsage{InputTokens: tokens, OutputTokens: tokens},
		}))
	}
	mk(types.SopRunSucceeded, 10)
	mk(types.SopRunSucceeded, 20)
	mk(types.SopRunFailed, 5)
	mk(types.SopRunCancelled, 0)
	mk(types.SopRunRunning, 7)
	mk(types.SopRunQueued, 0)
	// 别的租户不参与统计
	require.NoError(t, repo.CreateRun(ctx, &types.SopRun{
		TenantID: 2, ProjectID: "p1", Status: types.SopRunSucceeded,
		Usage: types.SopUsage{InputTokens: 1000},
	}))

	got, err := repo.StatsSummary(ctx, 1, "")
	require.NoError(t, err)
	require.EqualValues(t, 6, got.TotalCount)
	require.EqualValues(t, 4, got.FinishedCount)
	require.InDelta(t, 0.5, got.SuccessRate, 1e-9, "2 succeeded / 4 finished")
	require.EqualValues(t, 20+40+10+0+14, got.TotalTokens)
	require.EqualValues(t, 2, got.StatusCounts[types.SopRunSucceeded])
	require.EqualValues(t, 1, got.StatusCounts[types.SopRunFailed])
	require.EqualValues(t, 1, got.StatusCounts[types.SopRunRunning])

	// 按项目过滤
	projectScoped, err := repo.StatsSummary(ctx, 1, "p1")
	require.NoError(t, err)
	require.EqualValues(t, 6, projectScoped.TotalCount)
	other, err := repo.StatsSummary(ctx, 1, "p-none")
	require.NoError(t, err)
	require.Zero(t, other.TotalCount)
	require.Zero(t, other.SuccessRate, "无已结束运行时不除零")
}

// ListRuns 过滤条件组合 + 分页 + 倒序（最新在前）。
func TestListRunsFiltersAndPagination(t *testing.T) {
	db := newSopRunTestDB(t)
	repo := NewSopRunRepo(db)
	ctx := context.Background()
	base := time.Now().Add(-time.Hour)
	mk := func(id, project, item, status, template string, offset time.Duration) {
		r := &types.SopRun{ID: id, TenantID: 1, ProjectID: project, ItemID: item, Status: status,
			TemplateID: template, CreatedAt: base.Add(offset)}
		require.NoError(t, repo.CreateRun(ctx, r))
	}
	mk("run1", "p1", "i1", types.SopRunSucceeded, "t1", 0)
	mk("run2", "p1", "i2", types.SopRunFailed, "t1", time.Minute)
	mk("run3", "p2", "i2", types.SopRunRunning, "t2", 2*time.Minute)
	mk("run4", "p1", "i1", types.SopRunQueued, "t2", 3*time.Minute)

	list, err := repo.ListRuns(ctx, 1, SopRunFilter{ProjectID: "p1"}, 1, 10)
	require.NoError(t, err)
	require.Len(t, list, 3)
	require.Equal(t, "run4", list[0].ID, "created_at 倒序")
	require.Equal(t, "run1", list[2].ID)

	list, err = repo.ListRuns(ctx, 1, SopRunFilter{ItemID: "i2"}, 1, 10)
	require.NoError(t, err)
	require.Len(t, list, 2)

	list, err = repo.ListRuns(ctx, 1, SopRunFilter{ProjectID: "p1", Status: types.SopRunFailed, TemplateID: "t1"}, 1, 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "run2", list[0].ID)

	page1, err := repo.ListRuns(ctx, 1, SopRunFilter{ProjectID: "p1"}, 1, 2)
	require.NoError(t, err)
	page2, err := repo.ListRuns(ctx, 1, SopRunFilter{ProjectID: "p1"}, 2, 2)
	require.NoError(t, err)
	require.Len(t, page1, 2)
	require.Len(t, page2, 1)
	require.Equal(t, "run4", page1[0].ID)
	require.Equal(t, "run2", page1[1].ID)
	require.Equal(t, "run1", page2[0].ID)

	// 别的租户看不到
	others, err := repo.ListRuns(ctx, 2, SopRunFilter{ProjectID: "p1"}, 1, 10)
	require.NoError(t, err)
	require.Empty(t, others)
}

// UpdateRun 是全列（白名单内）写入：JSON 列、指针列的零值也要真正落库，
// 否则重试（failed→queued 且清空 finished_at/error）会被「跳过零值」静默吞掉。
func TestUpdateRunWritesZeroValuesAndJSON(t *testing.T) {
	db := newSopRunTestDB(t)
	repo := NewSopRunRepo(db)
	ctx := context.Background()
	now := time.Now()
	r := &types.SopRun{
		TenantID: 1, Status: types.SopRunFailed, Error: "boom", FinishedAt: &now,
		Input:        map[string]any{"k": "v"},
		HumanContext: []types.SopHumanContextEntry{{At: now, Source: "human", NodeID: "n1", Text: "补充"}},
		Usage:        types.SopUsage{InputTokens: 3, OutputTokens: 4, CostUSD: 0.1},
	}
	require.NoError(t, repo.CreateRun(ctx, r))

	got, err := repo.GetRun(ctx, 1, r.ID)
	require.NoError(t, err)
	got.Status = types.SopRunQueued
	got.Error = ""
	got.FinishedAt = nil
	got.HumanContext = append(got.HumanContext, types.SopHumanContextEntry{At: now, Source: "rework", NodeID: "n1", Text: "第二次"})
	require.NoError(t, repo.UpdateRun(ctx, got))

	back, err := repo.GetRun(ctx, 1, r.ID)
	require.NoError(t, err)
	require.Equal(t, types.SopRunQueued, back.Status)
	require.Empty(t, back.Error, "error 必须被清空（重试路径）")
	require.Nil(t, back.FinishedAt, "finished_at 必须被清回 NULL")
	require.Len(t, back.HumanContext, 2)
	require.Equal(t, "v", back.Input["k"])
	require.EqualValues(t, 3, back.Usage.InputTokens)
}

// GetRun 的租户 0 直通分支：引擎内部按 runID 读取（无租户上下文）。
func TestGetRunInternalBypass(t *testing.T) {
	db := newSopRunTestDB(t)
	repo := NewSopRunRepo(db)
	ctx := context.Background()
	r := &types.SopRun{TenantID: 7, Status: types.SopRunRunning}
	require.NoError(t, repo.CreateRun(ctx, r))

	got, err := repo.GetRun(ctx, 0, r.ID)
	require.NoError(t, err)
	require.Equal(t, uint64(7), got.TenantID)

	// 带租户时按租户校验：别的租户读不到。
	_, err = repo.GetRun(ctx, 1, r.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	_, err = repo.GetRun(ctx, 0, "missing")
	require.True(t, errors.Is(err, gorm.ErrRecordNotFound))
}

// UpdateRun 只写运行态字段白名单：归属与创建态列不被覆盖（跨租户改写面不可达）。
func TestUpdateRunWhitelistIgnoresOwnership(t *testing.T) {
	db := newSopRunTestDB(t)
	repo := NewSopRunRepo(db)
	ctx := context.Background()
	r := &types.SopRun{TenantID: 1, TemplateID: "t1", RevisionID: "rev1", Status: types.SopRunQueued, CreatedBy: "u1"}
	require.NoError(t, repo.CreateRun(ctx, r))

	got, err := repo.GetRun(ctx, 1, r.ID)
	require.NoError(t, err)
	got.Status = types.SopRunSucceeded
	got.WorkspaceDir = "/ws"
	got.TemplateID = "hacked"
	got.CreatedBy = "hacker"
	require.NoError(t, repo.UpdateRun(ctx, got))

	back, err := repo.GetRun(ctx, 1, r.ID)
	require.NoError(t, err)
	require.Equal(t, types.SopRunSucceeded, back.Status)
	require.Equal(t, "/ws", back.WorkspaceDir)
	require.Equal(t, "t1", back.TemplateID, "template_id 是创建态列，UpdateRun 不得改")
	require.Equal(t, uint64(1), back.TenantID)
	require.Equal(t, "u1", back.CreatedBy)

	// 别租户拿同一 id 更新：不得命中，库中状态不变。
	foreign := &types.SopRun{ID: r.ID, TenantID: 2, Status: types.SopRunFailed}
	require.ErrorIs(t, repo.UpdateRun(ctx, foreign), gorm.ErrRecordNotFound)
	final, err := repo.GetRun(ctx, 1, r.ID)
	require.NoError(t, err)
	require.Equal(t, types.SopRunSucceeded, final.Status, "跨租户更新不得落到本租户行上")

	// 把读回对象的 TenantID 改掉再更新同样不命中（归属列是写路径的键，不是被写入的列）。
	hijack, err := repo.GetRun(ctx, 1, r.ID)
	require.NoError(t, err)
	hijack.TenantID = 9
	hijack.Status = types.SopRunCancelled
	require.ErrorIs(t, repo.UpdateRun(ctx, hijack), gorm.ErrRecordNotFound)
}

// 节点运行：唯一 (run_id,node_id)、按 run 升序列出、get 校验租户。
func TestNodeRunCRUD(t *testing.T) {
	db := newSopRunTestDB(t)
	repo := NewSopRunRepo(db)
	ctx := context.Background()
	n2 := &types.SopNodeRun{TenantID: 1, RunID: "r1", NodeID: "n2", Status: types.SopNodePending}
	n1 := &types.SopNodeRun{TenantID: 1, RunID: "r1", NodeID: "n1", Status: types.SopNodeRunning}
	require.NoError(t, repo.CreateNodeRun(ctx, n2))
	require.NoError(t, repo.CreateNodeRun(ctx, n1))

	dup := &types.SopNodeRun{TenantID: 1, RunID: "r1", NodeID: "n1"}
	require.Error(t, repo.CreateNodeRun(ctx, dup), "同一 run 的 node_id 唯一")

	list, err := repo.ListNodeRuns(ctx, 1, "r1")
	require.NoError(t, err)
	require.Len(t, list, 2)

	got, err := repo.GetNodeRun(ctx, 1, "r1", "n1")
	require.NoError(t, err)
	require.Equal(t, types.SopNodeRunning, got.Status)

	_, err = repo.GetNodeRun(ctx, 2, "r1", "n1")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	got.Status = types.SopNodeSucceeded
	got.Output = "done"
	require.NoError(t, repo.UpdateNodeRun(ctx, got))
	back, err := repo.GetNodeRun(ctx, 1, "r1", "n1")
	require.NoError(t, err)
	require.Equal(t, types.SopNodeSucceeded, back.Status)
	require.Equal(t, "done", back.Output)
}

// 产物：创建补 id/attempt、按 run 列出、get 校验租户、软删后不可见。
func TestArtifactCRUD(t *testing.T) {
	db := newSopRunTestDB(t)
	repo := NewSopRunRepo(db)
	ctx := context.Background()
	a := &types.SopArtifact{TenantID: 1, RunID: "r1", NodeID: "n1", Name: "报告", Path: "out/a.md", SHA256: "abc"}
	require.NoError(t, repo.CreateArtifact(ctx, a))
	require.NotEmpty(t, a.ID)
	require.Equal(t, 1, a.Attempt)

	list, err := repo.ListArtifacts(ctx, 1, "r1")
	require.NoError(t, err)
	require.Len(t, list, 1)

	got, err := repo.GetArtifact(ctx, 1, a.ID)
	require.NoError(t, err)
	require.Equal(t, "out/a.md", got.Path)

	_, err = repo.GetArtifact(ctx, 2, a.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	require.NoError(t, db.Where("id = ?", a.ID).Delete(&types.SopArtifact{}).Error)
	list, err = repo.ListArtifacts(ctx, 1, "r1")
	require.NoError(t, err)
	require.Empty(t, list)
}

// CountActiveRuns 只数活跃四态（queued/running/waiting/paused）且按项目与租户隔离。
func TestCountActiveRuns(t *testing.T) {
	db := newSopRunTestDB(t)
	repo := NewSopRunRepo(db)
	ctx := context.Background()
	for _, s := range []string{
		types.SopRunQueued, types.SopRunRunning, types.SopRunWaiting, types.SopRunPaused,
		types.SopRunSucceeded, types.SopRunFailed, types.SopRunCancelled,
	} {
		require.NoError(t, repo.CreateRun(ctx, &types.SopRun{TenantID: 1, ProjectID: "p1", Status: s}))
	}
	require.NoError(t, repo.CreateRun(ctx, &types.SopRun{TenantID: 1, ProjectID: "p2", Status: types.SopRunRunning}))
	require.NoError(t, repo.CreateRun(ctx, &types.SopRun{TenantID: 2, ProjectID: "p1", Status: types.SopRunRunning}))

	projects := NewSopProjectRepo(db)
	n, err := projects.CountActiveRuns(ctx, 1, "p1")
	require.NoError(t, err)
	require.EqualValues(t, 4, n, "只含活跃四态")

	n, err = projects.CountActiveRuns(ctx, 1, "p2")
	require.NoError(t, err)
	require.EqualValues(t, 1, n)

	n, err = projects.CountActiveRuns(ctx, 2, "p1")
	require.NoError(t, err)
	require.EqualValues(t, 1, n, "租户隔离")

	n, err = projects.CountActiveRuns(ctx, 1, "p-none")
	require.NoError(t, err)
	require.Zero(t, n)
}
