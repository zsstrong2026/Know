package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	sqlite3migrate "github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/stretchr/testify/require"
)

// workbenchFirstMigration 是工作台相关 SQLite 迁移的起点（P1-1 的 000042）。
const workbenchFirstMigration = 42

// workbenchMigrationSteps 统计从工作台起点到最新的迁移数量。
//
// 回滚步数必须覆盖 000042 之后的全部迁移；写死常量会在后续追加迁移后失效
// （曾经固定为 5，等目录涨到 62 版时 Steps(-5) 只回退到 57，工作台表自然还在）。
func workbenchMigrationSteps(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(sqliteRepoRoot(t), "migrations", "sqlite"))
	require.NoError(t, err)
	steps := 0
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		version, convErr := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		require.NoError(t, convErr, name)
		if version >= workbenchFirstMigration {
			steps++
		}
	}
	require.Positive(t, steps, "工作台迁移必须存在")
	return steps
}

// TestWorkbenchMigrationsRollback 验证工作台迁移的退出标准「回滚可用」：
// up 全量迁移 → 用 golang-migrate 回滚全部工作台迁移 → 表/列/索引全部消失 → 再 up 仍能成功。
//
// 为什么必须走 `migrate.Steps(-N)` 而不是手工 Exec down 脚本：
// 手工执行只验证 SQL 语法，不会更新 schema_migrations；而真实回滚必须同时
// 回退版本水位，否则回滚后无法重新升级。这里用与生产完全相同的 migrate 实例。
func TestWorkbenchMigrationsRollback(t *testing.T) {
	// 先按测试包目录解析仓库根并统计步数，再切到仓库根，避免相对路径二次拼接。
	repoRoot := sqliteRepoRoot(t)
	steps := workbenchMigrationSteps(t)
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
	// 000062 的标签表同样属于工作台，必须一起回滚。
	require.True(t, sqliteTableExists(t, db, "workbench_tags"))
	version, dirty := sqliteMigrationState(t, db)
	require.False(t, dirty, "迁移后不应处于 dirty 状态")
	require.GreaterOrEqual(t, version, workbenchFirstMigration, "最新版本应包含工作台迁移")

	m := newSQLiteMigrator(t, dbPath)

	// 2) 回滚从 000042 起的全部迁移。
	require.NoError(t, m.Steps(-steps), "down 脚本必须可执行")
	rolledBack, rolledBackDirty := sqliteMigrationState(t, db)
	require.False(t, rolledBackDirty, "回滚后不应处于 dirty 状态")
	require.Equal(t, version-steps, rolledBack, "版本水位必须同步回退")
	require.Less(t, rolledBack, workbenchFirstMigration, "回滚后应停在工作台迁移之前")

	// 3) 表必须全部消失；000045 只加列，workbench_items 由 000042 的 down 删除。
	for _, table := range []string{
		"workbench_items", "workbench_item_versions", "workbench_folders",
		"workbench_item_links", "workbench_archives", "workbench_orphans",
		"workbench_notices", "workbench_item_assets", "workbench_templates",
		"workbench_tags", "workbench_item_tags",
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
	require.NoError(t, m.Steps(steps), "回滚后必须能重新升级")
	require.True(t, sqliteTableExists(t, db, "workbench_items"), "重新迁移后表应重新出现")
	require.True(t, sqliteColumnExists(t, db, "knowledge_bases", "is_system"), "重新迁移后列应重新出现")
	require.True(t, sqliteIndexExists(t, db, "uq_kb_user_system"), "重新迁移后索引应重新出现")
	require.True(t, sqliteColumnExists(t, db, "workbench_items", "journal_date"), "重新迁移后 journal_date 应重新出现")
	require.True(t, sqliteIndexExists(t, db, "idx_wi_journal"), "重新迁移后 idx_wi_journal 应重新出现")
	require.True(t, sqliteTableExists(t, db, "workbench_tags"), "重新迁移后标签表应重新出现")
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
