package repository

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Tencent/WeKnora/internal/types"
)

// newSopTriggerTestDB 建独立 SQLite 内存库并 AutoMigrate 触发配置/历史/派发预约三张表，
// 再按迁移原文补 GORM tag 表达不了的部分唯一索引（WHERE deleted_at IS NULL）。
func newSopTriggerTestDB(t *testing.T) *gorm.DB {
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
		&types.SopTriggerConfig{}, &types.SopTriggerLog{}, &types.SopDispatchReservation{},
	))
	// GORM tag 无法表达「唯一 + WHERE deleted_at IS NULL」，按迁移原文补齐。
	for _, ddl := range []string{
		"CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_trigger_project " +
			"ON sop_trigger_configs(project_id) WHERE deleted_at IS NULL",
		"CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_dispatch_slot " +
			"ON sop_dispatch_reservations(config_id, item_id, slot)",
	} {
		require.NoError(t, db.Exec(ddl).Error)
	}
	return db
}

// TestUpsertConfigPerProject 同一项目二次 Upsert 更新同一行（不新增、不报唯一键错），
// 且租户/项目归属不被调用方的零值冲掉。
func TestUpsertConfigPerProject(t *testing.T) {
	db := newSopTriggerTestDB(t)
	repo := NewSopTriggerRepo(db)
	ctx := context.Background()

	first := &types.SopTriggerConfig{
		TenantID: 1, ProjectID: "p1", Mode: types.SopTriggerModeInterval,
		IntervalKey: types.SopTriggerIntervalHourly, Timezone: "Asia/Shanghai",
		Sources: "all", Statuses: []string{types.SopItemStatusTodo}, PerRunLimit: 3,
		Enabled: true,
	}
	require.NoError(t, repo.UpsertConfig(ctx, first))
	require.NotEmpty(t, first.ID, "Upsert 必须回填 id")

	second := &types.SopTriggerConfig{
		ID: first.ID, TenantID: 1, ProjectID: "p1", Mode: types.SopTriggerModeCron,
		Cron: "0 9 * * *", Timezone: "Asia/Shanghai",
		Sources: "customer", Statuses: []string{types.SopItemStatusBacklog}, PerRunLimit: 7,
		Enabled: false,
	}
	require.NoError(t, repo.UpsertConfig(ctx, second))
	require.Equal(t, first.ID, second.ID, "同项目二次 Upsert 必须复用原行 id")

	var count int64
	require.NoError(t, db.Model(&types.SopTriggerConfig{}).Count(&count).Error)
	require.EqualValues(t, 1, count, "同项目只能有一行配置")

	got, err := repo.GetConfig(ctx, 1, "p1")
	require.NoError(t, err)
	require.Equal(t, types.SopTriggerModeCron, got.Mode)
	require.Equal(t, "0 9 * * *", got.Cron)
	require.Equal(t, 7, got.PerRunLimit)
	require.False(t, got.Enabled)
	require.Equal(t, []string{types.SopItemStatusBacklog}, got.Statuses)
}

// TestUpsertConfigTenantScoped 租户隔离：GetConfig 只在本租户内命中，
// 别的租户看不到本租户的配置。
//
// 唯一索引按迁移原文只约束 project_id（项目 id 是全局唯一 uuid，天然不跨租户重复），
// 故这里用不同的 project_id 建第二行，验证读取侧的租户收口。
func TestUpsertConfigTenantScoped(t *testing.T) {
	db := newSopTriggerTestDB(t)
	repo := NewSopTriggerRepo(db)
	ctx := context.Background()
	cfg := &types.SopTriggerConfig{
		TenantID: 1, ProjectID: "p1", Mode: types.SopTriggerModeInterval,
		IntervalKey: types.SopTriggerInterval15m, Timezone: "UTC", PerRunLimit: 2,
		Statuses: []string{types.SopItemStatusTodo},
	}
	require.NoError(t, repo.UpsertConfig(ctx, cfg))
	other := &types.SopTriggerConfig{
		TenantID: 2, ProjectID: "p2", Mode: types.SopTriggerModeInterval,
		IntervalKey: types.SopTriggerInterval30m, Timezone: "UTC", PerRunLimit: 2,
		Statuses: []string{types.SopItemStatusTodo},
	}
	require.NoError(t, repo.UpsertConfig(ctx, other))
	require.NotEqual(t, cfg.ID, other.ID)

	got, err := repo.GetConfig(ctx, 1, "p1")
	require.NoError(t, err)
	require.Equal(t, cfg.ID, got.ID)

	_, err = repo.GetConfig(ctx, 2, "p1")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound, "跨租户不可见")
	_, err = repo.GetConfig(ctx, 3, "p1")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound, "跨租户不可见")
}

// TestReserveDispatchIdempotent 同一 (config, item, slot) 只允许预约一次：
// 第二次返回 false 且不报错；换槽可再预约。
func TestReserveDispatchIdempotent(t *testing.T) {
	db := newSopTriggerTestDB(t)
	repo := NewSopTriggerRepo(db)
	ctx := context.Background()
	slot := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)

	ok, err := repo.ReserveDispatch(ctx, "cfg1", "item1", slot)
	require.NoError(t, err)
	require.True(t, ok, "首次预约必须成功")

	ok, err = repo.ReserveDispatch(ctx, "cfg1", "item1", slot)
	require.NoError(t, err)
	require.False(t, ok, "同槽重复预约必须返回 false 而不是错误")

	ok, err = repo.ReserveDispatch(ctx, "cfg1", "item2", slot)
	require.NoError(t, err)
	require.True(t, ok, "同槽不同事项可各自预约")

	ok, err = repo.ReserveDispatch(ctx, "cfg1", "item1", slot.Add(24*time.Hour))
	require.NoError(t, err)
	require.True(t, ok, "不同调度槽可再次预约")
}

// TestPerRunLimitBounds 单次派发上限只接受 1–10；越界在 Upsert 时即拒绝。
func TestPerRunLimitBounds(t *testing.T) {
	db := newSopTriggerTestDB(t)
	repo := NewSopTriggerRepo(db)
	ctx := context.Background()
	base := func(limit int) *types.SopTriggerConfig {
		return &types.SopTriggerConfig{
			TenantID: 1, ProjectID: "p1", Mode: types.SopTriggerModeInterval,
			IntervalKey: types.SopTriggerIntervalDaily9, Timezone: "Asia/Shanghai",
			Statuses: []string{types.SopItemStatusTodo}, PerRunLimit: limit,
		}
	}
	for _, bad := range []int{0, -1, 11, 100} {
		err := repo.UpsertConfig(ctx, base(bad))
		require.ErrorIsf(t, err, types.ErrInvalidSopTrigger, "per_run_limit=%d 必须被拒绝", bad)
	}
	var count int64
	require.NoError(t, db.Model(&types.SopTriggerConfig{}).Count(&count).Error)
	require.Zero(t, count, "校验失败不得落库")

	for _, good := range []int{1, 5, 10} {
		require.NoErrorf(t, repo.UpsertConfig(ctx, base(good)), "per_run_limit=%d 合法", good)
	}
}

// TestUpsertConfigBooleanColumnsExplicitFalse 布尔开关必须能显式存成 false：
// 结构体零值 false 不得被列默认值（prefer_item_template DEFAULT TRUE）顶掉，
// 否则用户关掉「优先事项模板」的配置永远保存不上。
func TestUpsertConfigBooleanColumnsExplicitFalse(t *testing.T) {
	db := newSopTriggerTestDB(t)
	repo := NewSopTriggerRepo(db)
	ctx := context.Background()
	cfg := &types.SopTriggerConfig{
		TenantID: 1, ProjectID: "p1", Mode: types.SopTriggerModeInterval,
		IntervalKey: types.SopTriggerInterval15m, Timezone: "UTC", PerRunLimit: 2,
		Statuses:           []string{types.SopItemStatusTodo},
		PreferItemTemplate: false, Enabled: false,
	}
	require.NoError(t, repo.UpsertConfig(ctx, cfg))

	var raw struct {
		Prefer  int
		Enabled int
	}
	require.NoError(t, db.Raw(
		"SELECT prefer_item_template AS prefer, enabled AS enabled FROM sop_trigger_configs WHERE id = ?", cfg.ID,
	).Scan(&raw).Error)
	require.Equal(t, 0, raw.Prefer, "显式 false 必须落库为 false")
	require.Equal(t, 0, raw.Enabled)

	got, err := repo.GetConfig(ctx, 1, "p1")
	require.NoError(t, err)
	require.False(t, got.PreferItemTemplate)
	require.False(t, got.Enabled)
}

// TestUpsertConfigValidation 校验口径逐条走一遍：cron 间隔过密、终态状态、
// 未知 mode/interval_key 全部拒绝且不落库；合法的 cron 配置照常保存。
func TestUpsertConfigValidation(t *testing.T) {
	db := newSopTriggerTestDB(t)
	repo := NewSopTriggerRepo(db)
	ctx := context.Background()
	base := func() *types.SopTriggerConfig {
		return &types.SopTriggerConfig{
			TenantID: 1, ProjectID: "p1", Mode: types.SopTriggerModeInterval,
			IntervalKey: types.SopTriggerIntervalDaily9, Timezone: "Asia/Shanghai",
			Statuses: []string{types.SopItemStatusTodo}, PerRunLimit: 5,
		}
	}
	cases := map[string]func(*types.SopTriggerConfig){
		"每分钟 cron":     func(c *types.SopTriggerConfig) { c.Mode, c.Cron = types.SopTriggerModeCron, "* * * * *" },
		"每两分钟 cron":    func(c *types.SopTriggerConfig) { c.Mode, c.Cron = types.SopTriggerModeCron, "*/2 9 * * *" },
		"cron 为空":      func(c *types.SopTriggerConfig) { c.Mode, c.Cron = types.SopTriggerModeCron, "" },
		"未知 mode":      func(c *types.SopTriggerConfig) { c.Mode = "every-nanosecond" },
		"未知 interval":  func(c *types.SopTriggerConfig) { c.IntervalKey = "2m" },
		"终态 done":      func(c *types.SopTriggerConfig) { c.Statuses = []string{types.SopItemStatusDone} },
		"终态 closed":    func(c *types.SopTriggerConfig) { c.Statuses = []string{types.SopItemStatusClosed} },
		"未知状态":         func(c *types.SopTriggerConfig) { c.Statuses = []string{"archived"} },
		"未知来源":         func(c *types.SopTriggerConfig) { c.Sources = "all,unknown" },
		"未知时区":         func(c *types.SopTriggerConfig) { c.Timezone = "Mars/Olympus" },
		"cron 带 TZ 前缀": func(c *types.SopTriggerConfig) { c.Mode, c.Cron = types.SopTriggerModeCron, "CRON_TZ=UTC 0 9 * * *" },
		"cron 语法错误":    func(c *types.SopTriggerConfig) { c.Mode, c.Cron = types.SopTriggerModeCron, "not a cron" },
	}
	for name, mutate := range cases {
		cfg := base()
		mutate(cfg)
		require.ErrorIsf(t, repo.UpsertConfig(ctx, cfg), types.ErrInvalidSopTrigger, "用例 %s 必须被拒绝", name)
	}
	var count int64
	require.NoError(t, db.Model(&types.SopTriggerConfig{}).Count(&count).Error)
	require.Zero(t, count, "校验失败不得落库")

	ok := base()
	ok.Mode, ok.Cron = types.SopTriggerModeCron, "0 9 * * *"
	require.NoError(t, repo.UpsertConfig(ctx, ok), "每天 9 点的 cron 合法")
}

// TestListLogsDescending AddLog 写入即取 id/created_at；ListLogs 按 created_at 倒序分页。
func TestListLogsDescending(t *testing.T) {
	db := newSopTriggerTestDB(t)
	repo := NewSopTriggerRepo(db)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		log := &types.SopTriggerLog{
			TenantID: 1, ConfigID: "cfg1", Kind: types.SopTriggerLogAuto,
			Scanned: i + 1, Fired: i, Skipped: 1, Note: "n",
		}
		require.NoError(t, repo.AddLog(ctx, log))
		// 同毫秒写入时靠显式时间拉开顺序（SQLite CURRENT_TIMESTAMP 精度只到秒）。
		require.NoError(t, db.Model(&types.SopTriggerLog{}).
			Where("id = ?", log.ID).
			Update("created_at", time.Now().Add(time.Duration(i)*time.Second)).Error)
	}
	logs, err := repo.ListLogs(ctx, 1, "cfg1", 1, 2)
	require.NoError(t, err)
	require.Len(t, logs, 2)
	require.Equal(t, 3, logs[0].Scanned, "第一页第一条是最新一条")
	require.NotEmpty(t, logs[0].ID)

	logs, err = repo.ListLogs(ctx, 1, "cfg1", 2, 2)
	require.NoError(t, err)
	require.Len(t, logs, 1)
	require.Equal(t, 1, logs[0].Scanned)

	other, err := repo.ListLogs(ctx, 2, "cfg1", 1, 10)
	require.NoError(t, err)
	require.Empty(t, other, "跨租户不可见")
}
