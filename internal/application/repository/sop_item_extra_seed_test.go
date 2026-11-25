package repository

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
)

// 空 qas 也必须先校验事项归属：调用方传了别的租户的 itemID 时不能静默成功，
// 否则上层会以为「seeding 完成」，后续按该 item 查询却什么都拿不到。
func TestSeedAnsweredValidatesItemEvenWhenEmpty(t *testing.T) {
	db := newSopExtraTestDB(t)
	repo := NewSopItemExtraRepo(db)
	ctx := context.Background()

	require.ErrorIs(t, repo.SeedAnswered(ctx, 2, "it1", nil), gorm.ErrRecordNotFound,
		"跨租户的 itemID 即使 qas 为空也要报不存在")
	require.NoError(t, repo.SeedAnswered(ctx, 1, "it1", nil), "本租户空 qas 是合法 no-op")
}

// 调用方带进来的 ID/CreatedAt 必须丢掉：这些字段由模型补，复用别人的 id 会
// 撞主键，带旧 CreatedAt 会让「本轮新的问答」排到历史前面。
func TestSeedAnsweredDropsCallerIDAndCreatedAt(t *testing.T) {
	db := newSopExtraTestDB(t)
	repo := NewSopItemExtraRepo(db)
	ctx := context.Background()
	past := time.Now().Add(-48 * time.Hour)

	require.NoError(t, repo.SeedAnswered(ctx, 1, "it1", []types.SopItemClarification{
		{ID: "caller-supplied", CreatedAt: past, Question: "Q1", Answer: "A1"},
		{Question: "Q2", Answer: "A2"},
	}))

	list, err := repo.ListClarifications(ctx, 1, "it1")
	require.NoError(t, err)
	require.Len(t, list, 2)
	for _, c := range list {
		require.NotEqual(t, "caller-supplied", c.ID, "调用方 id 不得落库")
		require.True(t, c.CreatedAt.After(past.Add(time.Hour)), "创建时间由模型补，不沿用调用方旧值")
		require.Equal(t, types.SopClarificationStatusAnswered, c.Status)
		require.NotNil(t, c.AnsweredAt)
	}
}
