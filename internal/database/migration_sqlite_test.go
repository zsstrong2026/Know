package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSQLiteMigrationsIncludeAutoTagConfig(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	previousDir, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(repoRoot))
	t.Cleanup(func() { _ = os.Chdir(previousDir) })

	dbPath := filepath.Join(t.TempDir(), "migration.db")
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: dbPath}))

	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	rows, err := db.Query("PRAGMA table_info(knowledge_bases)")
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	found := false
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		require.NoError(t, rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey))
		if name == "auto_tag_config" {
			found = true
		}
	}
	require.NoError(t, rows.Err())
	require.True(t, found, "SQLite migrations must create knowledge_bases.auto_tag_config")
}

// TestWorkbenchTablesSQLite 覆盖 000042/000043/000044：
// 工作台 7 张表 + 附件表、知识库系统/私有三列、文档生成任务归档四列，以及本次新建的索引。
func TestWorkbenchTablesSQLite(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, repoRoot)

	dbPath := filepath.Join(t.TempDir(), "workbench.db")
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: dbPath}))
	db := openSQLiteDB(t, dbPath)

	for _, table := range []string{
		"workbench_items", "workbench_item_versions", "workbench_folders",
		"workbench_item_links", "workbench_archives", "workbench_orphans",
		"workbench_notices", "workbench_item_assets",
	} {
		require.Truef(t, sqliteTableExists(t, db, table), "SQLite migrations must create table %s", table)
	}

	for _, column := range []string{"is_system", "system_key", "visibility"} {
		require.Truef(
			t,
			sqliteColumnExists(t, db, "knowledge_bases", column),
			"SQLite migrations must add column knowledge_bases.%s",
			column,
		)
	}
	for _, column := range []string{"output_kb_id", "auto_archive", "archive_status", "archive_error"} {
		require.Truef(
			t,
			sqliteColumnExists(t, db, "doc_generation_tasks", column),
			"SQLite migrations must add column doc_generation_tasks.%s",
			column,
		)
	}

	for _, index := range []string{
		"uq_kb_user_system", "idx_kb_visibility",
		"idx_wi_owner_updated", "idx_wi_owner_kind_updated", "idx_wi_folder", "idx_wi_favorite",
		"uq_wiv_item_version", "idx_wiv_item_created",
		"idx_wf_owner_parent",
		"uq_wa_live_part", "idx_wa_source", "idx_wa_knowledge",
		"idx_doc_generation_tasks_output_kb",
		"idx_wia_item", "uq_wia_item_hash",
	} {
		require.Truef(t, sqliteIndexExists(t, db, index), "SQLite migrations must create index %s", index)
	}
}
