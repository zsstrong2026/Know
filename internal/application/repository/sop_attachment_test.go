package repository

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
)

// 附件写入必须自己校验事项归属：仓储是唯一落地入口，信任调用方传的 ItemID
// 等于只要猜到 id 就能往别的租户事项上挂附件。
func TestAddAttachmentRejectsForeignItem(t *testing.T) {
	db := newSopItemTestDB(t)
	repo := NewSopItemRepo(db)
	ctx := context.Background()
	own := seedSopItem(t, repo, 1, "p1", "TP-1", "本租户")
	foreign := seedSopItem(t, repo, 2, "p2", "TP-9", "别的租户")

	err := repo.AddAttachment(ctx, &types.SopItemAttachment{
		TenantID: 1, ItemID: foreign.ID, Name: "a.md", Size: 1, Data: []byte("x"),
	})
	require.ErrorIs(t, err, gorm.ErrRecordNotFound, "跨租户事项不得挂附件")

	err = repo.AddAttachment(ctx, &types.SopItemAttachment{
		TenantID: 1, ItemID: "ghost", Name: "a.md", Size: 1, Data: []byte("x"),
	})
	require.ErrorIs(t, err, gorm.ErrRecordNotFound, "不存在的事项同样拒绝")

	require.NoError(t, repo.AddAttachment(ctx, &types.SopItemAttachment{
		TenantID: 1, ItemID: own.ID, Name: "a.md", Size: 1, Data: []byte("x"),
	}))
	list, err := repo.ListAttachments(ctx, 1, own.ID)
	require.NoError(t, err)
	require.Len(t, list, 1)
}

// 同一租户 ID 但事项属于另一个租户的「拼接」请求：TenantID 由参数给出时
// 必须以事项真实归属为准，不能只看传进来的 tenant。
func TestAddAttachmentTenantMustMatchItem(t *testing.T) {
	db := newSopItemTestDB(t)
	repo := NewSopItemRepo(db)
	ctx := context.Background()
	own := seedSopItem(t, repo, 1, "p1", "TP-1", "本租户")

	// 事项属租户 1，却以租户 2 的身份写：必须拒绝。
	err := repo.AddAttachment(ctx, &types.SopItemAttachment{
		TenantID: 2, ItemID: own.ID, Name: "a.md", Size: 1, Data: []byte("x"),
	})
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}
