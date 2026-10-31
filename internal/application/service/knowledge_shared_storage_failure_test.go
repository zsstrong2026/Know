package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type failingSharedKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	err error
}

func (r *failingSharedKnowledgeRepo) GetKnowledgeBatch(context.Context, uint64, []string) ([]*types.Knowledge, error) {
	return []*types.Knowledge{{ID: "own", TenantID: 1, KnowledgeBaseID: "own-kb"}}, nil
}

func (r *failingSharedKnowledgeRepo) GetKnowledgeByIDOnly(context.Context, string) (*types.Knowledge, error) {
	return nil, r.err
}

// GetKnowledgeBatchByIDOnly mirrors a batch query: a missing row is simply
// absent, while a storage failure is an error.
func (r *failingSharedKnowledgeRepo) GetKnowledgeBatchByIDOnly(context.Context, []string) ([]*types.Knowledge, error) {
	if errors.Is(r.err, repository.ErrKnowledgeNotFound) {
		return nil, nil
	}
	return nil, r.err
}

// visibilityKBRepo 让 knowledgeBaseService 自己也能充当可见性 lookup：
// 它内部走 GetKnowledgeBaseByID，这里按 kbTenants 返回租户，缺省为 tenant 1。
// 全部按 workspace 可见，让用例只关注仓储失败的传播。
type visibilityKBRepo struct {
	interfaces.KnowledgeBaseRepository
	kbTenants map[string]uint64
}

func (r visibilityKBRepo) GetKnowledgeBaseByID(
	_ context.Context, id string,
) (*types.KnowledgeBase, error) {
	tenantID := uint64(1)
	if id != "" {
		if t, ok := r.kbTenants[id]; ok {
			tenantID = t
		}
	}
	return &types.KnowledgeBase{ID: id, TenantID: tenantID, Visibility: types.KBVisibilityWorkspace}, nil
}

func TestSharedDocumentReadsPropagateStorageFailure(t *testing.T) {
	for _, failure := range []error{errors.New("database unavailable"), repository.ErrKnowledgeNotFound} {
		repo := &failingSharedKnowledgeRepo{err: failure}
		// 可见性判定要按 id 加载 KB：own-kb 属于 tenant 1，
		// 让本用例只关注仓储失败的传播，而不是权限分支。
		kbs := staticKBService{lookup: staticKBLookup{
			"own-kb": {ID: "own-kb", TenantID: 1, Visibility: types.KBVisibilityWorkspace},
		}}
		svc := &knowledgeService{repo: repo, kbService: kbs}
		search := &knowledgeBaseService{kgRepo: repo, repo: visibilityKBRepo{}}
		rows, err := svc.GetKnowledgeBatchWithSharedAccess(newSharedAccessContext(), 1, []string{"own", "shared"})
		found, searchErr := search.fetchKnowledgeDataWithShared(newSharedAccessContext(), 1, []string{"own", "shared"})
		if errors.Is(failure, repository.ErrKnowledgeNotFound) {
			require.NoError(t, err)
			require.NoError(t, searchErr)
			require.Len(t, rows, 1)
			require.Len(t, found, 1)
		} else {
			require.ErrorIs(t, err, failure)
			require.ErrorIs(t, searchErr, failure)
			require.Nil(t, rows)
			require.Nil(t, found)
		}
	}
}
