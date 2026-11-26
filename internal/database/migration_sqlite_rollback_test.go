package database

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	sqlite3migrate "github.com/golang-migrate/migrate/v4/database/sqlite3"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/stretchr/testify/require"
)

// TestSQLiteMigrationsRollbackProjectNotes 验证 M11 迁移的 down 脚本真的能回滚：
// 升到最新 → 回滚一步 → 断言 sop_project_notes 与 ix_sop_items_parent 被移除、
// sop_items 本身与更早的 sop_* 表仍在 → 再升回来断言重新出现。
//
// 只断言「down 文件存在且非空」（T55 的对等测试）挡不住「down 写错表名」这类问题。
func TestSQLiteMigrationsRollbackProjectNotes(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, repoRoot)

	dbPath := filepath.Join(t.TempDir(), "rollback.db")
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: dbPath}))

	db := openSQLiteDB(t, dbPath)
	version, dirty := sqliteMigrationState(t, db)
	require.Equal(t, expectedSQLiteMigrationVersion, version)
	require.False(t, dirty)
	require.True(t, sqliteTableExists(t, db, "sop_project_notes"))
	require.True(t, sqliteIndexExists(t, db, "ix_sop_items_parent"))
	require.NoError(t, db.Close())

	// Down 三步：必须回到 75（000076 之前）。上游 0.9.1 的 credential_vault /
	// im_session_mode_indexes 与本地 052/053 撞号后重编号为 000077/000078，
	// 最新版本不再是 000076，因此需要连续回滚三步才能越过 000076。
	downSteps(t, dbPath, expectedSQLiteMigrationVersion-75)
	require.Equal(t, 75, oneStepDownVersion(t, dbPath))

	db = openSQLiteDB(t, dbPath)
	require.False(t, sqliteTableExists(t, db, "sop_project_notes"), "down 必须删掉黑板表")
	require.False(t, sqliteIndexExists(t, db, "ix_sop_items_parent"), "down 必须删掉父项索引")
	require.True(t, sqliteTableExists(t, db, "sop_items"), "down 不得误删既有表")
	require.True(t, sqliteTableExists(t, db, "sop_runs"))
	require.True(t, sqliteTableExists(t, db, "sop_trigger_configs"))

	// 回滚不应破坏既有数据：sop_items 里还能插入/读回（列未被 down 改动）
	_, err := db.Exec(`INSERT INTO sop_projects (id, tenant_id, name, code) VALUES ('p-rb', 1, '回滚项目', 'RB')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO sop_items (id, tenant_id, project_id, no, title) VALUES ('i-rb', 1, 'p-rb', 'RB-1', '回滚事项')`)
	require.NoError(t, err)
	var title string
	require.NoError(t, db.QueryRow(`SELECT title FROM sop_items WHERE id = 'i-rb'`).Scan(&title))
	require.Equal(t, "回滚事项", title)
	require.NoError(t, db.Close())

	// 再升回来：表与索引重新出现（可重复上下线）
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: dbPath}))
	db = openSQLiteDB(t, dbPath)
	version, dirty = sqliteMigrationState(t, db)
	require.Equal(t, expectedSQLiteMigrationVersion, version)
	require.False(t, dirty)
	require.True(t, sqliteTableExists(t, db, "sop_project_notes"))
	require.True(t, sqliteIndexExists(t, db, "ix_sop_items_parent"))
}

// downSteps 用与生产相同的迁移目录与驱动回滚指定步数（测试里直接建 migrator，避免暴露回滚 API）。
func downSteps(t *testing.T, dbPath string, steps int) {
	t.Helper()
	sqlDB, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	driver, err := sqlite3migrate.WithInstance(sqlDB, &sqlite3migrate.Config{})
	require.NoError(t, err)
	// 与生产同一路径口径（相对 cwd 的 file://）——绝对 Windows 路径会被 url.Parse
	// 当成 host 而报 invalid port。
	m, err := migrate.NewWithDatabaseInstance("file://migrations/sqlite", "sqlite3", driver)
	require.NoError(t, err)
	require.NoError(t, m.Steps(-steps))
}

// oneStepDownVersion 读回 schema_migrations 的版本（回滚后）。
func oneStepDownVersion(t *testing.T, dbPath string) int {
	t.Helper()
	db := openSQLiteDB(t, dbPath)
	defer func() { _ = db.Close() }()
	version, dirty := sqliteMigrationState(t, db)
	require.False(t, dirty)
	return version
}
