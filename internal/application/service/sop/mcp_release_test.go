package sop

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

// 运行收口后按前缀清理该运行的端点行：端点表只增不减时，每跑一次就多留
// 「运行数 × 节点数」行，列表页会越来越长，过期 token 也没有回收路径。
func TestReleaseRunRemovesOnlyOwnEndpoints(t *testing.T) {
	p, db := newTestProvisioner(t)
	ctx := context.Background()

	_, _, err := p.EnsureNodeEndpoint(ctx, 1, "run-1", "n1", nil)
	require.NoError(t, err)
	_, _, err = p.EnsureNodeEndpoint(ctx, 1, "run-1", "n2", nil)
	require.NoError(t, err)
	_, _, err = p.EnsureNodeEndpoint(ctx, 1, "run-2", "n1", nil)
	require.NoError(t, err)
	// 用户自建端点（非 sop: 前缀）不得被误删。
	require.NoError(t, db.Create(&types.MCPEndpoint{
		TenantID: 1, Name: "我的端点", Enabled: true,
	}).Error)

	require.NoError(t, p.ReleaseRun(ctx, 1, "run-1"))

	var left []types.MCPEndpoint
	require.NoError(t, db.Find(&left).Error)
	names := make([]string, 0, len(left))
	for _, ep := range left {
		names = append(names, ep.Name)
	}
	require.ElementsMatch(t, []string{"sop:run-2:n1", "我的端点"}, names,
		"只删本运行的端点，其他运行与用户自建端点保留")
}

// 前缀必须按整段匹配：run-1 的清理不能顺手删掉 run-10 的端点。
func TestReleaseRunPrefixIsExact(t *testing.T) {
	p, db := newTestProvisioner(t)
	ctx := context.Background()

	_, _, err := p.EnsureNodeEndpoint(ctx, 1, "run-1", "n1", nil)
	require.NoError(t, err)
	_, _, err = p.EnsureNodeEndpoint(ctx, 1, "run-10", "n1", nil)
	require.NoError(t, err)

	require.NoError(t, p.ReleaseRun(ctx, 1, "run-1"))

	var left []types.MCPEndpoint
	require.NoError(t, db.Find(&left).Error)
	require.Len(t, left, 1)
	require.Equal(t, "sop:run-10:n1", left[0].Name, "run-10 不能被 run-1 的前缀误伤")
}

// 无端点时不报错（大多数运行没有 CLI 节点）。
func TestReleaseRunNoopWhenNothingToDelete(t *testing.T) {
	p, _ := newTestProvisioner(t)
	require.NoError(t, p.ReleaseRun(context.Background(), 1, "run-empty"))
}
