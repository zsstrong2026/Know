package repository

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Tencent/WeKnora/internal/types"
)

// 并发 Upsert 撞唯一索引时必须降级为更新，而不是把驱动错误抛给调用方：
// 「先查后写」的窗口在真实并发下必然出现（两个管理员同时保存触发配置）。
//
// 复现方式：Create 前回调里用**另一条连接**抢先提交同项目的配置行——预检阶段
// 查不到、落库瞬间撞 ux_sop_trigger_project，正是线上时序。回调里的写入必须
// 独立提交（不能落在即将回滚的事务里），所以本用例用文件库 + 多连接。
func TestUpsertConfigFallsBackToUpdateOnUniqueViolation(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "sop_trigger.db") + "?_journal_mode=WAL&_busy_timeout=5000"
	db := openSopTriggerDBAt(t, dsn)
	// 第二个连接：回调里的「抢先插入」必须独立提交，不能挂在将被回滚的事务上。
	other := openSopTriggerDBAt(t, dsn)
	repo := NewSopTriggerRepo(db)
	ctx := context.Background()

	var raced bool
	require.NoError(t, db.Callback().Create().Before("gorm:create").
		Register("test:race_insert_trigger_config", func(tx *gorm.DB) {
			if raced {
				return
			}
			if _, ok := tx.Statement.Dest.(*types.SopTriggerConfig); !ok {
				return
			}
			raced = true
			winner := &types.SopTriggerConfig{
				TenantID: 1, ProjectID: "p1", Mode: types.SopTriggerModeInterval,
				IntervalKey: types.SopTriggerIntervalHourly, Timezone: "Asia/Shanghai",
				Sources: "all", Statuses: []string{types.SopItemStatusTodo},
				PerRunLimit: 3, Enabled: true,
			}
			require.NoError(t, other.Create(winner).Error, "抢先行必须独立提交")
		}))

	incoming := &types.SopTriggerConfig{
		TenantID: 1, ProjectID: "p1", Mode: types.SopTriggerModeCron, Cron: "0 9 * * *",
		Timezone: "Asia/Shanghai", Sources: "customer",
		Statuses: []string{types.SopItemStatusTodo}, PerRunLimit: 9, Enabled: false,
	}
	require.NoError(t, repo.UpsertConfig(ctx, incoming), "撞唯一索引应降级为更新而不是报错")
	require.True(t, raced, "回调必须真的制造过一次并发插入")

	var count int64
	require.NoError(t, db.Model(&types.SopTriggerConfig{}).Count(&count).Error)
	require.EqualValues(t, 1, count, "仍只能有一行配置")

	got, err := repo.GetConfig(ctx, 1, "p1")
	require.NoError(t, err)
	require.Equal(t, types.SopTriggerModeCron, got.Mode, "后写者的内容生效")
	require.Equal(t, 9, got.PerRunLimit)
	require.False(t, got.Enabled)
}

// openSopTriggerDBAt 在给定 DSN 上建连接池并补齐表与唯一索引（同一个文件可开多条）。
func openSopTriggerDBAt(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(
		&types.SopTriggerConfig{}, &types.SopTriggerLog{}, &types.SopDispatchReservation{},
	))
	require.NoError(t, db.Exec(
		"CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_trigger_project "+
			"ON sop_trigger_configs(project_id) WHERE deleted_at IS NULL").Error)
	return db
}
