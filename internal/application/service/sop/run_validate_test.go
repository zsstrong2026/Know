package sop

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
)

// 已发布 revision 里带了未知节点类型（历史数据/手改库/旧版本写入的行）时，
// 发起运行必须当场拒绝：跑到一半才在执行器里炸，用户看到的是内部错误。
func TestCreateRunRejectsUnknownNodeType(t *testing.T) {
	svc, db := newRunTestService(t)
	ctx := context.Background()
	tpl := seedRawPublishedTemplate(t, db, types.SopDefinition{Nodes: []types.SopNode{
		{ID: "n1", Name: "怪节点", Type: "python"},
	}})

	_, err := svc.Create(ctx, 1, "u1", CreateRunInput{TemplateID: tpl.ID})
	require.ErrorIs(t, err, ErrSopRunDefinitionInvalid)
	require.Contains(t, err.Error(), "python")

	var count int64
	require.NoError(t, db.Model(&types.SopRun{}).Count(&count).Error)
	require.Zero(t, count, "定义非法时不得建出运行")
}

// 未知产物校验方式同样在创建期拒绝。
func TestCreateRunRejectsUnknownCheckType(t *testing.T) {
	svc, db := newRunTestService(t)
	ctx := context.Background()
	tpl := seedRawPublishedTemplate(t, db, types.SopDefinition{Nodes: []types.SopNode{{
		ID: "n1", Name: "实现", Type: types.SopNodeTypeAgent,
		Executor: types.SopNodeExecutor{Kind: types.SopExecutorPlatformAgent, AgentID: "a"},
		ArtifactOutputs: []types.SopArtifactOutput{{
			Name: "a", Path: "out/a.md", Check: types.SopArtifactCheck{Type: "regex"},
		}},
	}}})

	_, err := svc.Create(ctx, 1, "u1", CreateRunInput{TemplateID: tpl.ID})
	require.ErrorIs(t, err, ErrSopRunDefinitionInvalid)
	require.Contains(t, err.Error(), "regex")
}

// 合法定义照常建运行（不得误伤）。
func TestCreateRunAcceptsValidDefinition(t *testing.T) {
	svc, db := newRunTestService(t)
	ctx := context.Background()
	tpl := seedPublishedTemplate(t, db, nil)

	run, err := svc.Create(ctx, 1, "u1", CreateRunInput{TemplateID: tpl.ID})
	require.NoError(t, err)
	require.Equal(t, types.SopRunQueued, run.Status)
}

// seedRawPublishedTemplate 直接落库草稿并发布（绕过 TemplateService 的发布校验，
// 模拟「历史数据里已经有非法定义」）。
func seedRawPublishedTemplate(t *testing.T, db *gorm.DB, def types.SopDefinition) *types.SopTemplate {
	t.Helper()
	ctx := context.Background()
	repo := repository.NewSopTemplateRepo(db)
	tpl := &types.SopTemplate{TenantID: 1, Name: t.Name(), Definition: def}
	require.NoError(t, repo.Create(ctx, tpl))
	_, err := repo.Publish(ctx, 1, tpl.ID, "u1")
	require.NoError(t, err)
	got, err := repo.GetByID(ctx, 1, tpl.ID)
	require.NoError(t, err)
	return got
}
