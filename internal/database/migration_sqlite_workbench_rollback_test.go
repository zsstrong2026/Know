package database

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	sqlite3migrate "github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/stretchr/testify/require"
)

// workbenchMigrationSteps 是工作台相关 SQLite 迁移数量：
// P1-1 的 000042/000043/000044 + P1-9 的 000045 + P1-12 的 000046。
const workbenchMigrationSteps = 5

// TestWorkbenchMigrationsRollback 验证工作台迁移的退出标准「回滚可用」：
// up 全量迁移 → 用 golang-migrate 回滚 5 步 → 表/列/索引全部消失 → 再 up 仍能成功。
//
// 为什么必须走 `migrate.Steps(-5)` 而不是手工 Exec down 脚本：
// 手工执行只验证 SQL 语法，不会更新 schema_migrations；而真实回滚必须同时
// 回退版本水位，否则回滚后无法重新升级。这里用与生产完全相同的 migrate 实例。
func TestWorkbenchMigrationsRollback(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, repoRoot)

	dbPath := filepath.Join(t.TempDir(), "workbench-rollback.db")
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: dbPath}))
	db := openSQLiteDB(t, dbPath)

	// 1) 回滚前确认对象存在，避免「down 之后找不到」是因为本来就没建。
	require.True(t, sqliteTableExists(t, db, "workbench_items"))
	require.True(t, sqliteColumnExists(t, db, "knowledge_bases", "is_system"))
	// 000045 加的两列与部分索引必须在回滚前存在。
	require.True(t, sqliteColumnExists(t, db, "workbench_items", "journal_date"))
	require.True(t, sqliteColumnExists(t, db, "workbench_items", "current_mode"))
	require.True(t, sqliteIndexExists(t, db, "idx_wi_journal"))
	version, dirty := sqliteMigrationState(t, db)
	require.False(t, dirty, "迁移后不应处于 dirty 状态")
	require.GreaterOrEqual(t, version, workbenchMigrationSteps)

	m := newSQLiteMigrator(t, dbPath)

	// 2) 回滚工作台的 4 个迁移。
	require.NoError(t, m.Steps(-workbenchMigrationSteps), "down 脚本必须可执行")
	rolledBack, rolledBackDirty := sqliteMigrationState(t, db)
	require.False(t, rolledBackDirty, "回滚后不应处于 dirty 状态")
	require.Equal(t, version-workbenchMigrationSteps, rolledBack, "版本水位必须同步回退")

	// 3) 表必须全部消失；000045 只加列，workbench_items 由 000042 的 down 删除。
	for _, table := range []string{
		"workbench_items", "workbench_item_versions", "workbench_folders",
		"workbench_item_links", "workbench_archives", "workbench_orphans",
		"workbench_notices", "workbench_item_assets", "workbench_templates",
	} {
		require.Falsef(t, sqliteTableExists(t, db, table), "回滚后不应残留表 %s", table)
	}
	require.False(t, sqliteIndexExists(t, db, "idx_wi_journal"), "回滚后不应残留索引 idx_wi_journal")

	// 4) knowledge_bases 新增的三列必须消失，原有列不受影响。
	for _, column := range []string{"is_system", "system_key", "visibility"} {
		require.Falsef(t, sqliteColumnExists(t, db, "knowledge_bases", column),
			"回滚后不应残留列 knowledge_bases.%s", column)
	}
	require.True(t, sqliteColumnExists(t, db, "knowledge_bases", "id"), "回滚不能误删原有列")

	// 5) doc_generation_tasks 的归档四列必须消失。
	for _, column := range []string{"output_kb_id", "auto_archive", "archive_status", "archive_error"} {
		require.Falsef(t, sqliteColumnExists(t, db, "doc_generation_tasks", column),
			"回滚后不应残留列 doc_generation_tasks.%s", column)
	}

	// 6) knowledge_bases 上的索引必须消失（其余索引随表一起删除）。
	for _, index := range []string{"uq_kb_user_system", "idx_kb_visibility", "idx_doc_generation_tasks_output_kb"} {
		require.Falsef(t, sqliteIndexExists(t, db, index), "回滚后不应残留索引 %s", index)
	}

	// 7) 再次 up：回滚必须可逆，否则线上回滚后无法重新升级。
	require.NoError(t, m.Steps(workbenchMigrationSteps), "回滚后必须能重新升级")
	require.True(t, sqliteTableExists(t, db, "workbench_items"), "重新迁移后表应重新出现")
	require.True(t, sqliteColumnExists(t, db, "knowledge_bases", "is_system"), "重新迁移后列应重新出现")
	require.True(t, sqliteIndexExists(t, db, "uq_kb_user_system"), "重新迁移后索引应重新出现")
	require.True(t, sqliteColumnExists(t, db, "workbench_items", "journal_date"), "重新迁移后 journal_date 应重新出现")
	require.True(t, sqliteIndexExists(t, db, "idx_wi_journal"), "重新迁移后 idx_wi_journal 应重新出现")
}

// newSQLiteMigrator 构造与 RunMigrationsWithOptions 完全一致的 migrate 实例，
// 用于测试里执行 down/up 步骤。
func newSQLiteMigrator(t *testing.T, dbPath string) *migrate.Migrate {
	t.Helper()
	sqlDB, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	driver, err := sqlite3migrate.WithInstance(sqlDB, &sqlite3migrate.Config{})
	require.NoError(t, err)
	m, err := migrate.NewWithDatabaseInstance("file://migrations/sqlite", "sqlite3", driver)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = m.Close() })
	return m
}
