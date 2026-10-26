package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ---- 测试替身：只实现项目服务真正依赖的能力 ----

var (
	_ interfaces.PMProjectRepository = (*fakePMProjectRepo)(nil)
	_ interfaces.PMKBDirService      = (*fakePMKBDir)(nil)
	_ pmKBService                    = (*fakePMKB)(nil)
	_ pmSessionService               = (*fakePMSession)(nil)
	_ pmModelService                 = (*fakePMModel)(nil)
)

type fakePMProjectRepo struct {
	items       []*types.PMProject
	createCalls int
	createErr   error
}

func (f *fakePMProjectRepo) Create(_ context.Context, p *types.PMProject) error {
	f.createCalls++
	if f.createErr != nil {
		return f.createErr
	}
	if p.ID == "" {
		p.ID = fmt.Sprintf("p-%d", len(f.items)+1)
	}
	f.items = append(f.items, p)
	return nil
}

func (f *fakePMProjectRepo) GetByID(_ context.Context, tenantID uint64, id string) (*types.PMProject, error) {
	for _, p := range f.items {
		if p.TenantID == tenantID && p.ID == id {
			return p, nil
		}
	}
	return nil, repository.ErrPMProjectNotFound
}

func (f *fakePMProjectRepo) GetByName(_ context.Context, tenantID uint64, name string) (*types.PMProject, error) {
	for _, p := range f.items {
		if p.TenantID == tenantID && p.Name == name {
			return p, nil
		}
	}
	return nil, repository.ErrPMProjectNotFound
}

func (f *fakePMProjectRepo) List(_ context.Context, tenantID uint64, status string) ([]*types.PMProject, error) {
	var out []*types.PMProject
	for _, p := range f.items {
		if p.TenantID == tenantID && (status == "" || p.Status == status) {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *fakePMProjectRepo) Update(_ context.Context, p *types.PMProject) error {
	for i, old := range f.items {
		if old.ID == p.ID && old.TenantID == p.TenantID {
			f.items[i] = p
			return nil
		}
	}
	return repository.ErrPMProjectNotFound
}

func (f *fakePMProjectRepo) Delete(_ context.Context, tenantID uint64, id string) error {
	for i, p := range f.items {
		if p.ID == id && p.TenantID == tenantID {
			f.items = append(f.items[:i], f.items[i+1:]...)
			return nil
		}
	}
	return repository.ErrPMProjectNotFound
}

type fakePMKB struct {
	created   []*types.KnowledgeBase
	deleted   []string
	createErr error
	tenantID  uint64
	tenantOK  bool
}

func (f *fakePMKB) CreateKnowledgeBase(ctx context.Context, kb *types.KnowledgeBase) (*types.KnowledgeBase, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	// 记录 ctx 中的租户，验证服务调用下游前已注入
	f.tenantID, f.tenantOK = types.TenantIDFromContext(ctx)
	kb.ID = "kb-1"
	kb.TenantID = f.tenantID
	f.created = append(f.created, kb)
	return kb, nil
}

func (f *fakePMKB) DeleteKnowledgeBase(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	return nil
}

type fakePMSession struct {
	created   []*types.Session
	deleted   []string
	createErr error
	deleteErr error
}

func (f *fakePMSession) CreateSession(_ context.Context, s *types.Session) (*types.Session, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	s.ID = "sess-1"
	f.created = append(f.created, s)
	return s, nil
}

func (f *fakePMSession) DeleteSession(_ context.Context, id string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, id)
	return nil
}

type fakePMKBDir struct {
	ensured []string
}

// fakePMModel 只返回预置模型列表，用于验证建库时回填默认嵌入模型
type fakePMModel struct {
	models []*types.Model
	err    error
}

func (f *fakePMModel) ListModels(_ context.Context) ([]*types.Model, error) {
	return f.models, f.err
}

func (f *fakePMKBDir) EnsureProjectDirs(_ context.Context, _ uint64, kbID string) error {
	f.ensured = append(f.ensured, kbID)
	return nil
}

func (f *fakePMKBDir) Resolve(dir types.PMDir, name string) string {
	return string(dir) + name
}

// ---- 编排规则 ----

// 1) is_new=true：建库 → 建会话 → 建项目，kb_id/session_id 回填
func TestCreateProjectNewCreatesKBAndSession(t *testing.T) {
	repo := &fakePMProjectRepo{}
	kb := &fakePMKB{}
	sess := &fakePMSession{}
	dir := &fakePMKBDir{}
	// 默认模型列表：只有默认 Embedding 模型会被写入知识库
	models := &fakePMModel{models: []*types.Model{
		{ID: "chat-1", Type: types.ModelTypeKnowledgeQA, IsDefault: true},
		{ID: "embed-1", Type: types.ModelTypeEmbedding, IsDefault: true},
	}}
	svc := NewPMProjectService(repo, kb, sess, dir, models)

	p, err := svc.CreateProject(context.Background(), 7, "user-1", "登录优化", true)
	require.NoError(t, err)
	require.NotNil(t, p)

	assert.Equal(t, "kb-1", p.KBID)
	assert.Equal(t, "sess-1", p.SessionID)
	assert.Equal(t, types.PMProjectStatusActive, p.Status)
	assert.Equal(t, "user-1", p.OwnerID)
	assert.Equal(t, "user-1", p.CreatedBy)
	assert.Equal(t, uint64(7), p.TenantID)

	require.Len(t, kb.created, 1)
	assert.Equal(t, "登录优化", kb.created[0].Name)
	// 建库必须带上租户默认 Embedding 模型，否则后续解析会失败
	assert.Equal(t, "embed-1", kb.created[0].EmbeddingModelID)
	// 下游知识库服务从 ctx 取租户，服务必须在调用前注入
	assert.True(t, kb.tenantOK)
	assert.Equal(t, uint64(7), kb.tenantID)

	require.Len(t, sess.created, 1)
	assert.Equal(t, "登录优化 - PM 工作站", sess.created[0].Title)
	assert.Equal(t, uint64(7), sess.created[0].TenantID)
	assert.Equal(t, "user-1", sess.created[0].UserID)

	assert.Equal(t, []string{"kb-1"}, dir.ensured)
	assert.Equal(t, 1, repo.createCalls)
}

// 2) is_new=false 且同名项目已存在：直接返回已有项目，不得重复建库
func TestCreateProjectExistingNameReturnsExisting(t *testing.T) {
	existing := &types.PMProject{
		ID: "p-old", TenantID: 7, Name: "登录优化",
		Status: types.PMProjectStatusActive, KBID: "kb-old",
	}
	repo := &fakePMProjectRepo{items: []*types.PMProject{existing}}
	kb := &fakePMKB{}
	svc := NewPMProjectService(repo, kb, &fakePMSession{}, &fakePMKBDir{}, &fakePMModel{})

	got, err := svc.CreateProject(context.Background(), 7, "user-1", "登录优化", false)
	require.NoError(t, err)
	assert.Same(t, existing, got)
	assert.Empty(t, kb.created, "同名项目已存在时不得重复建库")
	assert.Equal(t, 0, repo.createCalls)
}

// is_new=true 但同名项目已存在：同样复用，避免产生重复知识库
func TestCreateProjectNewReusesSameNameProject(t *testing.T) {
	existing := &types.PMProject{ID: "p-old", TenantID: 7, Name: "登录优化", KBID: "kb-old"}
	repo := &fakePMProjectRepo{items: []*types.PMProject{existing}}
	kb := &fakePMKB{}
	svc := NewPMProjectService(repo, kb, &fakePMSession{}, &fakePMKBDir{}, &fakePMModel{})

	got, err := svc.CreateProject(context.Background(), 7, "user-1", "登录优化", true)
	require.NoError(t, err)
	assert.Same(t, existing, got)
	assert.Empty(t, kb.created)
}

// 3) 建库成功但建会话失败：必须回滚（删除已建知识库），避免孤儿库
func TestCreateProjectSessionFailureRollsBackKB(t *testing.T) {
	repo := &fakePMProjectRepo{}
	kb := &fakePMKB{}
	dir := &fakePMKBDir{}
	svc := NewPMProjectService(repo, kb, &fakePMSession{createErr: errors.New("session down")}, dir, &fakePMModel{})

	p, err := svc.CreateProject(context.Background(), 7, "user-1", "登录优化", true)
	require.Error(t, err)
	assert.Nil(t, p)
	assert.Equal(t, []string{"kb-1"}, kb.deleted, "建会话失败必须删除已建知识库")
	assert.Empty(t, repo.items)
	assert.Empty(t, dir.ensured, "建会话失败不应初始化目录")
}

// 项目落库失败同样回滚知识库
func TestCreateProjectRepoFailureRollsBackKB(t *testing.T) {
	repo := &fakePMProjectRepo{createErr: errors.New("db down")}
	kb := &fakePMKB{}
	svc := NewPMProjectService(repo, kb, &fakePMSession{}, &fakePMKBDir{}, &fakePMModel{})

	p, err := svc.CreateProject(context.Background(), 7, "user-1", "登录优化", true)
	require.Error(t, err)
	assert.Nil(t, p)
	assert.Equal(t, []string{"kb-1"}, kb.deleted)
}

// 删除项目：先删项目数据，再清理关联知识库与会话
func TestDeleteProjectRemovesDataKBAndSession(t *testing.T) {
	repo := &fakePMProjectRepo{items: []*types.PMProject{{
		ID: "p-1", TenantID: 7, Name: "登录优化", KBID: "kb-1", SessionID: "sess-1",
	}}}
	kb := &fakePMKB{}
	sess := &fakePMSession{}
	svc := NewPMProjectService(repo, kb, sess, &fakePMKBDir{}, &fakePMModel{})

	require.NoError(t, svc.DeleteProject(context.Background(), 7, "p-1"))
	assert.Empty(t, repo.items, "项目记录应被删除")
	assert.Equal(t, []string{"kb-1"}, kb.deleted)
	assert.Equal(t, []string{"sess-1"}, sess.deleted)
}

// 项目不存在：直接返回 not found，不得清理任何外部资源
func TestDeleteProjectNotFound(t *testing.T) {
	kb := &fakePMKB{}
	sess := &fakePMSession{}
	svc := NewPMProjectService(&fakePMProjectRepo{}, kb, sess, &fakePMKBDir{}, &fakePMModel{})

	err := svc.DeleteProject(context.Background(), 7, "missing")
	assert.ErrorIs(t, err, repository.ErrPMProjectNotFound)
	assert.Empty(t, kb.deleted)
	assert.Empty(t, sess.deleted)
}

// 会话清理失败不影响删除结果（项目数据已删除，不应因外部资源失败而回滚）
func TestDeleteProjectIgnoresSessionCleanupError(t *testing.T) {
	repo := &fakePMProjectRepo{items: []*types.PMProject{{
		ID: "p-1", TenantID: 7, Name: "登录优化", KBID: "kb-1", SessionID: "sess-1",
	}}}
	svc := NewPMProjectService(repo, &fakePMKB{}, &fakePMSession{deleteErr: errors.New("down")},
		&fakePMKBDir{}, &fakePMModel{})

	require.NoError(t, svc.DeleteProject(context.Background(), 7, "p-1"))
	assert.Empty(t, repo.items)
}
