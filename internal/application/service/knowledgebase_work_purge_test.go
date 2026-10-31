package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// workKBPurgeRepo 是 PurgeWorkKnowledgeBase 的测试替身：
// 只实现它用到的两个方法（按 id 读 KB、软删 KB），其余靠内嵌接口占位。
type workKBPurgeRepo struct {
	interfaces.KnowledgeBaseRepository

	kb     *types.KnowledgeBase
	getErr error
	// deleted 记录被删除的 id；为空表示没有触发删除。
	deleted string
}

func (r *workKBPurgeRepo) GetKnowledgeBaseByID(
	_ context.Context, id string,
) (*types.KnowledgeBase, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	if r.kb == nil || r.kb.ID != id {
		return nil, repository.ErrKnowledgeBaseNotFound
	}
	return r.kb, nil
}

func (r *workKBPurgeRepo) DeleteKnowledgeBase(_ context.Context, id string) error {
	r.deleted = id
	return nil
}

// PurgeWorkKnowledgeBase 只接受系统工作知识库；普通 KB 一律拒绝。
func TestPurgeWorkKnowledgeBaseRejectsNonSystemKB(t *testing.T) {
	cases := []struct {
		name string
		kb   *types.KnowledgeBase
	}{
		{
			name: "普通工作区 KB",
			kb:   &types.KnowledgeBase{ID: "kb-1", TenantID: 1, Visibility: types.KBVisibilityWorkspace},
		},
		{
			name: "系统 KB 但 system_key 不是 work",
			kb: &types.KnowledgeBase{
				ID: "kb-2", TenantID: 1, IsSystem: true, SystemKey: "other",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &workKBPurgeRepo{kb: tc.kb}
			svc := &knowledgeBaseService{repo: repo}

			err := svc.PurgeWorkKnowledgeBase(context.Background(), tc.kb.ID)
			appErr, ok := apperrors.IsAppError(err)
			require.True(t, ok, "必须是 AppError，got %v", err)
			assert.Equal(t, apperrors.ErrWorkbenchSystemKB, appErr.Code)
			assert.Empty(t, repo.deleted, "非工作知识库不得触发删除")
		})
	}
}

// 工作知识库 ID 不存在时按 404 语义返回，不静默成功。
func TestPurgeWorkKnowledgeBaseMissingIsNotFound(t *testing.T) {
	repo := &workKBPurgeRepo{}
	svc := &knowledgeBaseService{repo: repo}

	err := svc.PurgeWorkKnowledgeBase(context.Background(), "missing")
	require.Error(t, err)
	assert.True(t, errors.Is(err, repository.ErrKnowledgeBaseNotFound))
	assert.Empty(t, repo.deleted)
}

// 合法工作知识库必须走统一的删除链路，并把 tenant 上下文补齐。
// 这里只断言“删除动作发生”，不装配 asynq/audit 等可选依赖。
func TestPurgeWorkKnowledgeBaseDeletesThroughSharedPath(t *testing.T) {
	repo := &workKBPurgeRepo{kb: &types.KnowledgeBase{
		ID: "work-1", TenantID: 7, IsSystem: true, SystemKey: types.KBSystemKeyWork,
		Visibility: types.KBVisibilityPrivate, CreatorID: "user-a",
	}}
	svc := &knowledgeBaseService{repo: repo}

	err := svc.PurgeWorkKnowledgeBase(context.Background(), "work-1")
	// 缺少 asynqClient 时入队会失败并返回错误，但软删必须已经落库。
	assert.Equal(t, "work-1", repo.deleted, "删除动作必须已执行")
	if err != nil {
		t.Logf("缺少可选依赖导致入队失败（可接受）：%v", err)
	}
}

// 空 id 直接 400，不做任何查询。
func TestPurgeWorkKnowledgeBaseRequiresID(t *testing.T) {
	svc := &knowledgeBaseService{}
	err := svc.PurgeWorkKnowledgeBase(context.Background(), "")
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	assert.Equal(t, apperrors.ErrBadRequest, appErr.Code)
}
