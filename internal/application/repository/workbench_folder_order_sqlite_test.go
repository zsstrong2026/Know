package repository

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ReorderFolders 是拖拽排序落库的那一步：整层按给定顺序重写 sort_order，
// 并且只影响「目标父级 + 当前所有者」的行。
func TestWorkbenchReorderFolders(t *testing.T) {
	db := setupWorkbenchFolderTestDB(t)
	repo := &workbenchRepository{db: db}
	const tenant uint64 = 7

	first := newTestFolder(t, repo, tenant, "owner-a", nil)
	second := newTestFolder(t, repo, tenant, "owner-a", nil)
	third := newTestFolder(t, repo, tenant, "owner-a", nil)
	// 造一条别人的文件夹：重排不得动它
	foreign := newTestFolder(t, repo, tenant, "owner-b", nil)

	for index, folder := range []*string{&first.ID, &second.ID, &third.ID} {
		_, err := repo.UpdateFolder(t.Context(), tenant, "owner-a", *folder, map[string]any{"sort_order": index})
		require.NoError(t, err)
	}

	// 只看根层级：子文件夹不参与这一层的顺序断言
	order := func() []string {
		folders, err := repo.ListFolders(t.Context(), tenant, "owner-a")
		require.NoError(t, err)
		ids := make([]string, 0, len(folders))
		for _, folder := range folders {
			if folder.ParentID != nil {
				continue
			}
			ids = append(ids, folder.ID)
		}
		return ids
	}

	require.Equal(t, []string{first.ID, second.ID, third.ID}, order())

	// 把 third 拖到最前；顺带把别人的 id 塞进去，必须被忽略
	require.NoError(t, repo.ReorderFolders(t.Context(), tenant, "owner-a", nil,
		[]string{third.ID, foreign.ID, first.ID, second.ID}))
	assert.Equal(t, []string{third.ID, first.ID, second.ID}, order())

	// 子层不受影响：重排根层级不会改子文件夹的 sort_order
	child := newTestFolder(t, repo, tenant, "owner-a", &first.ID)
	_, err := repo.UpdateFolder(t.Context(), tenant, "owner-a", child.ID, map[string]any{"sort_order": 5})
	require.NoError(t, err)
	require.NoError(t, repo.ReorderFolders(t.Context(), tenant, "owner-a", nil,
		[]string{first.ID, second.ID, third.ID}))

	got, err := repo.GetFolder(t.Context(), tenant, "owner-a", child.ID)
	require.NoError(t, err)
	assert.Equal(t, 5, got.SortOrder, "重排根层级不应改动子文件夹的顺序")
	assert.Equal(t, []string{first.ID, second.ID, third.ID}, order())
}

// 空列表是空操作：不报错也不改任何行的顺序。
func TestWorkbenchReorderFolders_EmptyIsNoop(t *testing.T) {
	db := setupWorkbenchFolderTestDB(t)
	repo := &workbenchRepository{db: db}
	folder := newTestFolder(t, repo, 7, "owner-a", nil)

	require.NoError(t, repo.ReorderFolders(t.Context(), 7, "owner-a", nil, nil))

	got, err := repo.GetFolder(t.Context(), 7, "owner-a", folder.ID)
	require.NoError(t, err)
	assert.Equal(t, folder.ID, got.ID)
}
