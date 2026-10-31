package types

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm/schema"
)

// TestWorkbenchTableNames 锁定模型到表名的映射，必须与 000136/000138 迁移一致。
//
// 为什么需要这条测试：GORM 的默认复数化对 `WorkbenchAsset` 推出的是
// `workbench_assets`，而迁移建的表叫 `workbench_item_assets`。这类偏差在
// 编译期完全看不出来，只会在 P1-2 第一次查询时变成 "no such table"。
func TestWorkbenchTableNames(t *testing.T) {
	cases := []struct {
		model any
		want  string
	}{
		{&WorkbenchItem{}, "workbench_items"},
		{&WorkbenchItemVersion{}, "workbench_item_versions"},
		{&WorkbenchFolder{}, "workbench_folders"},
		{&WorkbenchItemLink{}, "workbench_item_links"},
		{&WorkbenchArchive{}, "workbench_archives"},
		{&WorkbenchOrphan{}, "workbench_orphans"},
		{&WorkbenchNotice{}, "workbench_notices"},
		{&WorkbenchAsset{}, "workbench_item_assets"},
		{&WorkbenchTemplate{}, "workbench_templates"},
	}
	store := &sync.Map{}
	naming := schema.NamingStrategy{}
	for _, c := range cases {
		parsed, err := schema.Parse(c.model, store, naming)
		require.NoErrorf(t, err, "parse %T", c.model)
		require.Equalf(t, c.want, parsed.Table, "%T 的表名必须与迁移一致", c.model)
	}
}

// TestWorkbenchItemVersionSkipsComputedSize 确认 size_bytes 是计算列：
// 它不落库（000136 的 workbench_item_versions 没有该列），因此 GORM 既不能
// SELECT 也不能 INSERT 它，只能是 `gorm:"-"`。
// 若保留 `->`（只读），`First(&WorkbenchItemVersion{})` 会 SELECT size_bytes 而报错。
func TestWorkbenchItemVersionSkipsComputedSize(t *testing.T) {
	store := &sync.Map{}
	parsed, err := schema.Parse(&WorkbenchItemVersion{}, store, schema.NamingStrategy{})
	require.NoError(t, err)
	for _, field := range parsed.Fields {
		require.NotEqualf(t, "size_bytes", field.DBName,
			"WorkbenchItemVersion.%s 不能映射到 size_bytes 列（迁移里不存在该列）", field.Name)
	}
}
