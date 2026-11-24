package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

// newChildItemsWorkspace 建一个带 out/ 目录的工作区。
func newChildItemsWorkspace(t *testing.T, files map[string]string) *Workspace {
	t.Helper()
	root := t.TempDir()
	ws := &Workspace{Root: root}
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(body), 0o644))
	}
	return ws
}

func childItemsJSON(t *testing.T, n int) string {
	t.Helper()
	kids := make([]map[string]string, 0, n)
	for i := 0; i < n; i++ {
		kids = append(kids, map[string]string{"title": fmt.Sprintf("子%d", i), "description": "d"})
	}
	b, err := json.Marshal(map[string]any{"children": kids})
	require.NoError(t, err)
	return string(b)
}

// 未声明 produces_child_items：不读文件、不建子项。
func TestChildItemsSkippedWhenNotDeclared(t *testing.T) {
	called := 0
	e := NewEngine(Deps{CreateItems: func(context.Context, *types.SopRun, []types.SopChildItemSpec) error {
		called++
		return nil
	}})
	ws := newChildItemsWorkspace(t, nil)
	node := &types.SopNode{ID: "n1"}
	require.NoError(t, e.maybeCreateChildItems(context.Background(), &types.SopRun{ItemID: "i1"}, node, ws))
	require.Zero(t, called)
}

// 声明但运行没有关联事项：跳过不报错（没有可挂载的父项）。
func TestChildItemsSkippedWithoutItem(t *testing.T) {
	called := 0
	e := NewEngine(Deps{CreateItems: func(context.Context, *types.SopRun, []types.SopChildItemSpec) error {
		called++
		return nil
	}})
	ws := newChildItemsWorkspace(t, map[string]string{childItemsFile: childItemsJSON(t, 2)})
	node := &types.SopNode{ID: "n1", ProducesChildItems: true}
	require.NoError(t, e.maybeCreateChildItems(context.Background(), &types.SopRun{}, node, ws))
	require.Zero(t, called)
}

// 正常清单：交给 CreateItems，空标题的条目被过滤。
func TestChildItemsCreatesSpecs(t *testing.T) {
	var got []types.SopChildItemSpec
	e := NewEngine(Deps{CreateItems: func(_ context.Context, _ *types.SopRun, specs []types.SopChildItemSpec) error {
		got = specs
		return nil
	}})
	ws := newChildItemsWorkspace(t, map[string]string{
		childItemsFile: `{"children":[{"title":"子一","description":"d1"},{"title":"  "},{"title":"子二","kind":"task"}]}`,
	})
	node := &types.SopNode{ID: "n1", ProducesChildItems: true}
	require.NoError(t, e.maybeCreateChildItems(context.Background(), &types.SopRun{ItemID: "i1"}, node, ws))
	require.Len(t, got, 2)
	require.Equal(t, "子一", got[0].Title)
	require.Equal(t, "task", got[1].Kind)
}

// 超 20 条：报白话错误且完全不创建（先生成别名再调 CreateItems 的前提是校验在前）。
func TestChildItemsOverLimitRejected(t *testing.T) {
	called := 0
	e := NewEngine(Deps{CreateItems: func(context.Context, *types.SopRun, []types.SopChildItemSpec) error {
		called++
		return nil
	}})
	ws := newChildItemsWorkspace(t, map[string]string{childItemsFile: childItemsJSON(t, 21)})
	node := &types.SopNode{ID: "n1", ProducesChildItems: true}
	err := e.maybeCreateChildItems(context.Background(), &types.SopRun{ItemID: "i1"}, node, ws)
	require.ErrorContains(t, err, "20")
	require.Zero(t, called, "超限不得部分创建")
}

// 缺文件 / 格式错 / 全空标题：都返回白话错误。
func TestChildItemsInvalidPayloads(t *testing.T) {
	e := NewEngine(Deps{CreateItems: func(context.Context, *types.SopRun, []types.SopChildItemSpec) error {
		return nil
	}})
	node := &types.SopNode{ID: "n1", ProducesChildItems: true}
	run := &types.SopRun{ItemID: "i1"}

	err := e.maybeCreateChildItems(context.Background(), run, node, newChildItemsWorkspace(t, nil))
	require.ErrorContains(t, err, childItemsFile)

	ws := newChildItemsWorkspace(t, map[string]string{childItemsFile: "{not json"})
	err = e.maybeCreateChildItems(context.Background(), run, node, ws)
	require.ErrorContains(t, err, "格式不对")

	ws = newChildItemsWorkspace(t, map[string]string{childItemsFile: `{"children":[{"title":""}]}`})
	err = e.maybeCreateChildItems(context.Background(), run, node, ws)
	require.ErrorContains(t, err, "为空")
}
