package repository

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSopMigrationParity 校验 SOP 的 PG 与 SQLite 迁移对成对出现：
// 建表集合一致、SQLite 侧做了类型降级（JSONB→JSON、TIMESTAMPTZ→DATETIME、BYTEA→BLOB）、
// down 脚本存在且非空。两边任何一侧新增 SOP 表都必须同时补另一侧。
func TestSopMigrationParity(t *testing.T) {
	pairs := [][2]string{
		{"000166_sop_projects", "000068_sop_projects"},
		{"000167_sop_items", "000069_sop_items"},
		{"000168_sop_item_extras", "000070_sop_item_extras"},
		{"000169_sop_templates", "000071_sop_templates"},
		{"000170_sop_runs", "000072_sop_runs"},
		{"000171_sop_cli_trigger", "000073_sop_cli_trigger"},
		{"000172_sop_indexes", "000074_sop_indexes"},
		{"000173_sop_builtin_templates", "000075_sop_builtin_templates"},
		// 计划原定 000174，执行时被 meeting_phase_events 占用，PG 侧顺延为 000175
		// （SQLite 000076 未占用，对应关系不变）。
		{"000175_sop_project_notes", "000076_sop_project_notes"},
	}
	tableRe := regexp.MustCompile(`CREATE TABLE IF NOT EXISTS (\w+)`)
	// SQLite 迁移头部注释会写「JSONB→JSON」这类映射说明，注释里的类型名不算违规
	commentRe := regexp.MustCompile(`(?m)^\s*--.*$`)
	root := filepath.Join("..", "..", "..", "migrations")

	for _, pair := range pairs {
		pgPath := filepath.Join(root, "versioned", pair[0]+".up.sql")
		litePath := filepath.Join(root, "sqlite", pair[1]+".up.sql")

		pg, err := os.ReadFile(pgPath)
		require.NoError(t, err, "缺少 PG 迁移 %s", pgPath)
		lite, err := os.ReadFile(litePath)
		require.NoError(t, err, "缺少 SQLite 迁移 %s", litePath)

		require.Equal(t,
			tableRe.FindAllStringSubmatch(string(pg), -1),
			tableRe.FindAllStringSubmatch(string(lite), -1),
			"表集合不一致: %s ↔ %s", pair[0], pair[1],
		)

		// SQLite 侧必须类型降级，否则 sqlite 驱动建表失败
		liteSQL := commentRe.ReplaceAllString(string(lite), "")
		require.NotContains(t, liteSQL, "JSONB", "%s: SQLite 侧必须 JSON 降级", pair[1])
		require.NotContains(t, liteSQL, "TIMESTAMPTZ", "%s: SQLite 侧必须 DATETIME", pair[1])
		require.NotContains(t, liteSQL, "BYTEA", "%s: SQLite 侧必须 BLOB", pair[1])

		// down 两侧都要有且非空（回滚能力是迁移的一部分）
		for _, path := range []string{
			filepath.Join(root, "versioned", pair[0]+".down.sql"),
			filepath.Join(root, "sqlite", pair[1]+".down.sql"),
		} {
			down, err := os.ReadFile(path)
			require.NoError(t, err, "缺少 down 脚本 %s", path)
			require.NotEmpty(t, down, "down 脚本为空: %s", path)
		}
	}
}
