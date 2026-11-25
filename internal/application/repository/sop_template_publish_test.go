package repository

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

// 重复发布「没改过的草稿」不该每次都堆一条 revision：模板版本历史里全是
// 内容一模一样的快照，回滚与对比都失去意义。内容相同（规范化 JSON 的 sha256
// 一致）时复用已发布的那条 revision。
func TestPublishReusesRevisionForIdenticalDefinition(t *testing.T) {
	db := newSopTplTestDB(t)
	repo := NewSopTemplateRepo(db)
	ctx := context.Background()
	tpl := &types.SopTemplate{TenantID: 1, Name: "t", Definition: types.SopDefinition{
		Nodes: []types.SopNode{{ID: "n1", Name: "v1", Type: "agent"}},
	}}
	require.NoError(t, repo.Create(ctx, tpl))

	first, err := repo.Publish(ctx, 1, tpl.ID, "u1")
	require.NoError(t, err)
	require.Equal(t, 1, first.Revision)

	second, err := repo.Publish(ctx, 1, tpl.ID, "u1")
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID, "内容未变必须复用同一条 revision")
	require.Equal(t, 1, second.Revision)

	revs, err := repo.ListRevisions(ctx, 1, tpl.ID)
	require.NoError(t, err)
	require.Len(t, revs, 1, "不得堆出内容相同的 revision")

	// 改过内容再发布：必须产生新 revision。
	tpl.Definition.Nodes[0].Name = "v2"
	require.NoError(t, repo.Update(ctx, tpl))
	third, err := repo.Publish(ctx, 1, tpl.ID, "u1")
	require.NoError(t, err)
	require.NotEqual(t, first.ID, third.ID)
	require.Equal(t, 2, third.Revision)
}

// 键序不同但语义相同的定义也应视为同一份内容（走规范化 JSON 而非原始字节）。
func TestPublishReuseIgnoresJSONKeyOrder(t *testing.T) {
	db := newSopTplTestDB(t)
	repo := NewSopTemplateRepo(db)
	ctx := context.Background()
	tpl := &types.SopTemplate{TenantID: 1, Name: "t", Definition: types.SopDefinition{
		Nodes: []types.SopNode{{ID: "n1", Type: "agent",
			CredentialEnv: map[string]string{"A": "1", "B": "2"}}},
	}}
	require.NoError(t, repo.Create(ctx, tpl))
	first, err := repo.Publish(ctx, 1, tpl.ID, "u1")
	require.NoError(t, err)

	// 同一个 map 只是插入顺序换了：序列化前排序后应得到同一个指纹。
	tpl.Definition.Nodes[0].CredentialEnv = map[string]string{"B": "2", "A": "1"}
	require.NoError(t, repo.Update(ctx, tpl))
	second, err := repo.Publish(ctx, 1, tpl.ID, "u1")
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
}

// 复用时不改 published_by / created_at：被复用的是原来那次发布的事实。
func TestPublishReuseKeepsOriginalPublisher(t *testing.T) {
	db := newSopTplTestDB(t)
	repo := NewSopTemplateRepo(db)
	ctx := context.Background()
	tpl := &types.SopTemplate{TenantID: 1, Name: "t", Definition: types.SopDefinition{
		Nodes: []types.SopNode{{ID: "n1", Type: "agent"}},
	}}
	require.NoError(t, repo.Create(ctx, tpl))
	first, err := repo.Publish(ctx, 1, tpl.ID, "alice")
	require.NoError(t, err)

	second, err := repo.Publish(ctx, 1, tpl.ID, "bob")
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
	require.Equal(t, "alice", second.PublishedBy, "复用不覆盖原发布人")
	require.True(t, first.CreatedAt.Equal(second.CreatedAt), "复用不改创建时间")
}
