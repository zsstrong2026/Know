package hitl

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

// GORM 只在 CreatedAt 为零值时自动补当前时间；调用方显式给的时间戳原样落库。
// 内存实现必须同语义，否则两条链路的排序/分页结果不一致。
func TestMemoryStoreCreateKeepsCallerCreatedAt(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	past := time.Now().Add(-2 * time.Hour).Truncate(time.Second)

	require.NoError(t, store.Create(ctx, &types.AgentPrompt{
		ID: "p-old", TenantID: 1, SessionID: "s", Status: types.AgentPromptDone, CreatedAt: past,
	}))
	got, err := store.Get(ctx, "p-old")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, past, got.CreatedAt, "调用方给的时间戳必须保留")

	// 零值仍由存储补，避免落成 0001-01-01。
	require.NoError(t, store.Create(ctx, &types.AgentPrompt{
		ID: "p-now", TenantID: 1, SessionID: "s", Status: types.AgentPromptDone,
	}))
	got, err = store.Get(ctx, "p-now")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.False(t, got.CreatedAt.IsZero(), "零值时间戳仍要补当前时间")
}

// Windows 的时间精度约 1–15ms：连续创建的多行可能拿到同一个 CreatedAt。
// 这时按 created_at 排序必须仍然稳定（插入顺序），否则「最近一条」的语义
// 会在同一时刻的行之间随机漂移。
func TestMemoryStoreListBySessionOrderStableOnEqualTimestamps(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	// 同一个时间戳，模拟 Windows 粗时钟下连续创建的结果。
	same := time.Now().Truncate(time.Second)
	ids := []string{"p1", "p2", "p3", "p4"}
	for _, id := range ids {
		require.NoError(t, store.Create(ctx, &types.AgentPrompt{
			ID: id, TenantID: 1, SessionID: "s", Status: types.AgentPromptDone, CreatedAt: same,
		}))
	}

	for round := 0; round < 20; round++ {
		rows, err := store.ListBySession(ctx, "s", []string{types.AgentPromptDone})
		require.NoError(t, err)
		require.Len(t, rows, len(ids))
		for i, row := range rows {
			require.Equal(t, ids[i], row.ID,
				"同一时刻的行必须按创建顺序稳定排序（第 %d 轮第 %d 项）", round, i)
		}
	}
}
