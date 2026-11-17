package repository

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 缩略图与正文分开写：保存封面不应把条目顶到“最近更新”，版本只能前进。
func TestWorkbenchItemThumbnail_SaveAndRead(t *testing.T) {
	db := setupWorkbenchTestDB(t)
	repo := &workbenchRepository{db: db}
	item := newTestItem(t, db, 1, "user-a")
	ctx := t.Context()

	_, _, err := repo.UpdateItemContent(ctx, 1, "user-a", item.ID, 0, false, []byte("doc"), "正文", 1)
	require.NoError(t, err)
	before, err := repo.GetItemMeta(ctx, 1, "user-a", item.ID)
	require.NoError(t, err)

	applied, err := repo.SaveItemThumbnail(ctx, 1, "user-a", item.ID, []byte("jpeg-v1"), "image/jpeg", 1)
	require.NoError(t, err)
	assert.True(t, applied)

	image, contentType, err := repo.GetItemThumbnail(ctx, 1, "user-a", item.ID)
	require.NoError(t, err)
	assert.Equal(t, []byte("jpeg-v1"), image)
	assert.Equal(t, "image/jpeg", contentType)

	// 缩略图不参与“最近更新”排序。
	after, err := repo.GetItemMeta(ctx, 1, "user-a", item.ID)
	require.NoError(t, err)
	assert.Equal(t, before.UpdatedAt, after.UpdatedAt, "保存封面不得改 updated_at")
	assert.Equal(t, 1, after.ThumbnailVersion)

	// 旧版本不覆盖新封面。
	applied, err = repo.SaveItemThumbnail(ctx, 1, "user-a", item.ID, []byte("jpeg-v0"), "image/jpeg", 0)
	require.NoError(t, err)
	assert.False(t, applied, "旧版本封面应被忽略")
	image, _, err = repo.GetItemThumbnail(ctx, 1, "user-a", item.ID)
	require.NoError(t, err)
	assert.Equal(t, []byte("jpeg-v1"), image)

	// 同版本允许重拍（切换文档/画布形态后覆盖同一版本封面）。
	applied, err = repo.SaveItemThumbnail(ctx, 1, "user-a", item.ID, []byte("jpeg-v1b"), "image/jpeg", 1)
	require.NoError(t, err)
	assert.True(t, applied)
	image, _, err = repo.GetItemThumbnail(ctx, 1, "user-a", item.ID)
	require.NoError(t, err)
	assert.Equal(t, []byte("jpeg-v1b"), image)

	// owner 隔离：别人读不到。
	_, _, err = repo.GetItemThumbnail(ctx, 1, "user-b", item.ID)
	require.Error(t, err)
}
