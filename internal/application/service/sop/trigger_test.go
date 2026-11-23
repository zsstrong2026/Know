package sop

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
)

// newTriggerTestService 建 sqlite 内存库 + 真实仓储 + RunService。
func newTriggerTestService(t *testing.T) (*TriggerService, *gorm.DB, string) {
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
		&types.SopTemplate{}, &types.SopTemplateRevision{},
		&types.SopRun{}, &types.SopNodeRun{},
		&types.SopTriggerConfig{}, &types.SopTriggerLog{}, &types.SopDispatchReservation{},
	))
	require.NoError(t, db.Exec(
		"CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_node_runs_run_node ON sop_node_runs(run_id, node_id)",
	).Error)
	// 派发预约的唯一索引（迁移 ux_sop_dispatch_slot 的 sqlite 等价物）。
	require.NoError(t, db.Exec(
		`CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_dispatch_slot
		 ON sop_dispatch_reservations(config_id, item_id, slot)`,
	).Error)

	projects := repository.NewSopProjectRepo(db)
	items := repository.NewSopItemRepo(db)
	runs := repository.NewSopRunRepo(db)
	templates := repository.NewSopTemplateRepo(db)
	ctx := context.Background()
	proj := &types.SopProject{TenantID: 1, Name: "P", Code: "TP"}
	require.NoError(t, projects.Create(ctx, proj))

	svc := NewTriggerService(TriggerDeps{
		Triggers: repository.NewSopTriggerRepo(db), Items: items,
		Runs:      NewRunService(templates, runs, projects, items),
		Templates: templates,
	})
	return svc, db, proj.ID
}

// seedPublishedTpl 建一份已发布模板。
func seedPublishedTpl(t *testing.T, db *gorm.DB) *types.SopTemplate {
	t.Helper()
	ctx := context.Background()
	repo := repository.NewSopTemplateRepo(db)
	tpl := &types.SopTemplate{TenantID: 1, Name: "流程", Definition: types.SopDefinition{
		Nodes: []types.SopNode{{
			ID: "n1", Name: "实现", Type: "agent",
			Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"},
		}},
	}}
	require.NoError(t, repo.Create(ctx, tpl))
	_, err := repo.Publish(ctx, 1, tpl.ID, "u1")
	require.NoError(t, err)
	got, err := repo.GetByID(ctx, 1, tpl.ID)
	require.NoError(t, err)
	return got
}

// seedTriggerItem 建一条待触发事项（可选：绑模板 / 已有运行）。
func seedTriggerItem(
	t *testing.T, db *gorm.DB, projectID, no, source, status, templateID string, withRun bool,
) *types.SopItem {
	t.Helper()
	ctx := context.Background()
	it := &types.SopItem{
		TenantID: 1, ProjectID: projectID, No: no, Title: "事项 " + no,
		Source: source, Status: status, TemplateID: templateID,
	}
	require.NoError(t, repository.NewSopItemRepo(db).Create(ctx, it))
	if withRun {
		require.NoError(t, repository.NewSopRunRepo(db).CreateRun(ctx, &types.SopRun{
			TenantID: 1, ProjectID: projectID, ItemID: it.ID, Status: types.SopRunSucceeded,
		}))
	}
	return it
}

// 五档 interval 与 cron 的下一次触发时刻。
func TestNextTriggerRunIntervals(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	after := time.Date(2026, 11, 19, 10, 0, 0, 0, loc) // 周四

	cases := map[string]time.Time{
		"15m":     after.Add(15 * time.Minute),
		"30m":     after.Add(30 * time.Minute),
		"hourly":  after.Add(time.Hour),
		"daily9":  time.Date(2026, 11, 20, 9, 0, 0, 0, loc),
		"weekly1": time.Date(2026, 11, 23, 9, 0, 0, 0, loc), // 下周一
	}
	for key, want := range cases {
		cfg := &types.SopTriggerConfig{Mode: types.SopTriggerModeInterval,
			IntervalKey: key, Timezone: "Asia/Shanghai"}
		got, err := NextTriggerRun(cfg, after)
		require.NoError(t, err, "key=%s", key)
		require.True(t, got.Equal(want), "key=%s got=%v want=%v", key, *got, want)
	}

	cronCfg := &types.SopTriggerConfig{Mode: types.SopTriggerModeCron,
		Cron: "0 9 * * *", Timezone: "Asia/Shanghai"}
	got, err := NextTriggerRun(cronCfg, after)
	require.NoError(t, err)
	require.True(t, got.Equal(time.Date(2026, 11, 20, 9, 0, 0, 0, loc)), "got=%v", *got)

	// 未知档位/坏 cron 报白话错
	_, err = NextTriggerRun(&types.SopTriggerConfig{Mode: types.SopTriggerModeInterval,
		IntervalKey: "1m"}, after)
	require.Error(t, err)
	_, err = NextTriggerRun(&types.SopTriggerConfig{Mode: types.SopTriggerModeCron,
		Cron: "not a cron"}, after)
	require.Error(t, err)
}

// 扫描：状态∧来源∧未跑过 + 上限截断 + 同槽幂等 + 新槽继续。
func TestScanOnceFiltersLimitAndIdempotent(t *testing.T) {
	svc, db, pid := newTriggerTestService(t)
	ctx := context.Background()
	tpl := seedPublishedTpl(t, db)

	seedTriggerItem(t, db, pid, "TP-a", "customer", "todo", "", false)
	seedTriggerItem(t, db, pid, "TP-b", "customer", "todo", "", false)
	seedTriggerItem(t, db, pid, "TP-c", "internal", "todo", "", false)
	seedTriggerItem(t, db, pid, "TP-d", "internal", "todo", "", false)
	seedTriggerItem(t, db, pid, "TP-e", "customer", "done", "", false)
	seedTriggerItem(t, db, pid, "TP-f", "customer", "todo", "", true) // 已跑过

	cfg := &types.SopTriggerConfig{
		TenantID: 1, ProjectID: pid, Mode: types.SopTriggerModeInterval,
		IntervalKey: types.SopTriggerIntervalDaily9, Timezone: "Asia/Shanghai",
		DefaultTemplateID: tpl.ID, Sources: "customer", Statuses: []string{"todo"},
		PerRunLimit: 1, Enabled: true,
	}
	require.NoError(t, repository.NewSopTriggerRepo(db).UpsertConfig(ctx, cfg))
	slot := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)

	log1, err := svc.ScanOnce(ctx, cfg, types.SopTriggerLogAuto, slot)
	require.NoError(t, err)
	require.Equal(t, 2, log1.Scanned, "只有 a、b 命中（来源/状态/未跑过）")
	require.Equal(t, 1, log1.Fired, "per_run_limit=1 截断")
	require.Equal(t, 1, log1.Skipped)

	log2, err := svc.ScanOnce(ctx, cfg, types.SopTriggerLogAuto, slot)
	require.NoError(t, err)
	require.Equal(t, 0, log2.Fired, "同槽重扫必须幂等")

	log3, err := svc.ScanOnce(ctx, cfg, types.SopTriggerLogAuto, slot.Add(24*time.Hour))
	require.NoError(t, err)
	require.Equal(t, 1, log3.Fired, "新槽继续派发剩余的 b")

	runs, err := repository.NewSopRunRepo(db).ListRuns(ctx, 1,
		types.SopRunFilter{ProjectID: pid}, 0, 0)
	require.NoError(t, err)
	// 只看本触发器派发的运行（夹具里那条「已跑过」的运行不算）。
	scheduled := 0
	for _, r := range runs {
		if r.TriggerKind != types.SopRunTriggerSchedule {
			continue
		}
		scheduled++
		require.Equal(t, cfg.ID, r.TriggerRefID, "触发来源可回溯")
	}
	require.Equal(t, 2, scheduled, "两个不同槽各派发一条")
}

// PreferItemTemplate：事项绑定的模板优先于默认模板。
func TestScanOncePreferItemTemplate(t *testing.T) {
	svc, db, pid := newTriggerTestService(t)
	ctx := context.Background()
	tplA := seedPublishedTpl(t, db)
	tplB := seedPublishedTpl(t, db)
	it := seedTriggerItem(t, db, pid, "TP-x", "customer", "todo", tplB.ID, false)

	cfg := &types.SopTriggerConfig{
		TenantID: 1, ProjectID: pid, DefaultTemplateID: tplA.ID, PreferItemTemplate: true,
		Sources: "all", Statuses: []string{"todo"}, PerRunLimit: 10,
		Mode: types.SopTriggerModeInterval, IntervalKey: types.SopTriggerInterval15m, Timezone: "UTC",
	}
	require.NoError(t, repository.NewSopTriggerRepo(db).UpsertConfig(ctx, cfg))

	_, err := svc.ScanOnce(ctx, cfg, types.SopTriggerLogManual, time.Now().Truncate(time.Minute))
	require.NoError(t, err)
	runs, err := repository.NewSopRunRepo(db).ListRuns(ctx, 1,
		types.SopRunFilter{ItemID: it.ID}, 0, 0)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.Equal(t, tplB.ID, runs[0].TemplateID, "事项绑定优先")

	// 已跑过的事项不再自动派发
	_, err = svc.ScanOnce(ctx, cfg, types.SopTriggerLogManual, time.Now().Add(time.Minute).Truncate(time.Minute))
	require.NoError(t, err)
	runs, _ = repository.NewSopRunRepo(db).ListRuns(ctx, 1, types.SopRunFilter{ItemID: it.ID}, 0, 0)
	require.Len(t, runs, 1, "跑过就不跑（返工由人决定）")
}

// 没有模板可用：跳过并写白话备注。
func TestScanOnceSkipsWithoutTemplate(t *testing.T) {
	svc, db, pid := newTriggerTestService(t)
	ctx := context.Background()
	seedTriggerItem(t, db, pid, "TP-n", "customer", "todo", "", false)
	cfg := &types.SopTriggerConfig{
		TenantID: 1, ProjectID: pid, Mode: types.SopTriggerModeInterval,
		IntervalKey: types.SopTriggerInterval15m, Timezone: "UTC",
		Statuses: []string{"todo"}, Sources: "all", PerRunLimit: 10,
	}
	require.NoError(t, repository.NewSopTriggerRepo(db).UpsertConfig(ctx, cfg))

	logRec, err := svc.ScanOnce(ctx, cfg, types.SopTriggerLogManual, time.Now().Truncate(time.Minute))
	require.NoError(t, err)
	require.Equal(t, 0, logRec.Fired)
	require.Contains(t, logRec.Note, "未绑定 SOP 模板")
}

// RunNow：未启用也允许手动；找不到配置时报错。
func TestRunNow(t *testing.T) {
	svc, db, pid := newTriggerTestService(t)
	ctx := context.Background()
	err := svc.RunNow(ctx, 1, "u1", pid)
	require.Error(t, err, "没有配置时明确报错")

	tpl := seedPublishedTpl(t, db)
	cfg := &types.SopTriggerConfig{
		TenantID: 1, ProjectID: pid, DefaultTemplateID: tpl.ID,
		Mode: types.SopTriggerModeInterval, IntervalKey: types.SopTriggerInterval15m,
		Timezone: "UTC", Statuses: []string{"todo"}, Sources: "all", PerRunLimit: 5,
		Enabled: false, // 未启用也要能手跑
	}
	require.NoError(t, repository.NewSopTriggerRepo(db).UpsertConfig(ctx, cfg))
	seedTriggerItem(t, db, pid, "TP-rn", "customer", "todo", "", false)

	require.NoError(t, svc.RunNow(ctx, 1, "u1", pid))
	logs, err := repository.NewSopTriggerRepo(db).ListLogs(ctx, 1, cfg.ID, 1, 10)
	require.NoError(t, err)
	require.Len(t, logs, 1)
	require.Equal(t, types.SopTriggerLogManual, logs[0].Kind)
	require.Equal(t, 1, logs[0].Fired)
}

// 调度器 tick：NextRunAt 为空时补算，到点时扫描。
func TestTriggerSchedulerTick(t *testing.T) {
	svc, db, pid := newTriggerTestService(t)
	ctx := context.Background()
	tpl := seedPublishedTpl(t, db)
	cfg := &types.SopTriggerConfig{
		TenantID: 1, ProjectID: pid, DefaultTemplateID: tpl.ID,
		Mode: types.SopTriggerModeInterval, IntervalKey: types.SopTriggerInterval15m,
		Timezone: "UTC", Statuses: []string{"todo"}, Sources: "all", PerRunLimit: 5,
		Enabled: true,
	}
	require.NoError(t, repository.NewSopTriggerRepo(db).UpsertConfig(ctx, cfg))
	seedTriggerItem(t, db, pid, "TP-s", "customer", "todo", "", false)

	scheduler := NewTriggerScheduler(svc.d)
	scheduler.tick(ctx) // 第一轮：补算 NextRunAt

	stored, err := repository.NewSopTriggerRepo(db).GetConfig(ctx, 1, pid)
	require.NoError(t, err)
	require.NotNil(t, stored.NextRunAt, "空的下次触发时间要被补算")

	// 把 NextRunAt 拨到过去，下一轮 tick 应当派发
	past := time.Now().Add(-time.Minute)
	stored.NextRunAt = &past
	require.NoError(t, repository.NewSopTriggerRepo(db).UpsertConfig(ctx, stored))

	scheduler.tick(ctx)
	runs, err := repository.NewSopRunRepo(db).ListRuns(ctx, 1,
		types.SopRunFilter{ProjectID: pid}, 0, 0)
	require.NoError(t, err)
	require.Len(t, runs, 1, "到点必须发起运行")
	require.Equal(t, types.SopRunTriggerSchedule, runs[0].TriggerKind)
}
