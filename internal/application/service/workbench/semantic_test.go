package workbench

import (
	"context"
	"errors"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubSemanticKB 记录 UpdateKnowledgeBase 的入参，用于断言“只改模型与索引策略”。
type stubSemanticKB struct {
	kb      *types.KnowledgeBase
	updated *types.KnowledgeBaseConfig
	err     error
}

func (s *stubSemanticKB) UpdateKnowledgeBase(
	_ context.Context, id, name, description string,
	config *types.KnowledgeBaseConfig, _ *types.VLMConfig,
) (*types.KnowledgeBase, error) {
	if s.err != nil {
		return nil, s.err
	}
	s.updated = config
	if s.kb != nil {
		s.kb.ID = id
		s.kb.Name = name
		s.kb.Description = description
	}
	return s.kb, nil
}

func (s *stubSemanticKB) EnsureWorkKnowledgeBase(
	context.Context, uint64, string,
) (*types.KnowledgeBase, error) {
	if s.kb == nil {
		return nil, errors.New("work kb unavailable")
	}
	return s.kb, nil
}

// stubSemanticKnowledge 记录重建入队的知识 id，便于断言逐条 ReparseKnowledge。
type stubSemanticKnowledge struct {
	rows   []*types.Knowledge
	failed map[string]bool
	queued []string
}

func (s *stubSemanticKnowledge) ListKnowledgeByKnowledgeBaseID(
	context.Context, string,
) ([]*types.Knowledge, error) {
	return s.rows, nil
}

func (s *stubSemanticKnowledge) ReparseKnowledge(
	_ context.Context, knowledgeID string, _ *types.KnowledgeProcessOverrides,
) (*types.Knowledge, error) {
	if s.failed[knowledgeID] {
		return nil, errors.New("reparse failed")
	}
	s.queued = append(s.queued, knowledgeID)
	return &types.Knowledge{ID: knowledgeID}, nil
}

// newSemanticFixture 组装一个“工作库 + 两个知识 + 一个可用 embedding 模型”的环境。
func newSemanticFixture() (*Service, *stubSemanticKB, *stubSemanticKnowledge) {
	kb := &types.KnowledgeBase{
		ID: "kb-work", TenantID: 1, CreatorID: "user-a",
		Name: types.KBNameWork, IsSystem: true, SystemKey: types.KBSystemKeyWork,
		Visibility: types.KBVisibilityPrivate,
	}
	kb.IndexingStrategy = types.IndexingStrategy{KeywordEnabled: true, VectorEnabled: false}
	kbSvc := &stubSemanticKB{kb: kb}
	kgSvc := &stubSemanticKnowledge{
		rows: []*types.Knowledge{
			{ID: "k-1", KnowledgeBaseID: "kb-work"},
			{ID: "k-2", KnowledgeBaseID: "kb-work"},
		},
		failed: map[string]bool{},
	}
	svc := NewService(newMemArchiveRepo()).
		WithArchiveServices(kbSvc, newStubArchiveKnowledge()).
		WithSemanticIndex(kbSvc, kgSvc, activeEmbeddingModel())
	return svc, kbSvc, kgSvc
}

// 启用语义索引：绑定默认模型、打开向量索引，并把每条知识重新入队。
func TestEnableSemanticIndexBindsModelAndRequeues(t *testing.T) {
	svc, kbSvc, kgSvc := newSemanticFixture()

	result, err := svc.EnableSemanticIndex(newTestContext(), "")
	require.NoError(t, err)
	assert.Equal(t, 2, result.Queued)
	assert.Equal(t, "emb-1", result.ModelID)

	require.NotNil(t, kbSvc.updated)
	require.NotNil(t, kbSvc.updated.EmbeddingModelID)
	assert.Equal(t, "emb-1", *kbSvc.updated.EmbeddingModelID)
	require.NotNil(t, kbSvc.updated.IndexingStrategy)
	assert.True(t, kbSvc.updated.IndexingStrategy.VectorEnabled)
	assert.True(t, kbSvc.updated.IndexingStrategy.KeywordEnabled, "语义与关键词互补，不应被关掉")

	assert.Equal(t, []string{"k-1", "k-2"}, kgSvc.queued)
}

// 单个知识重建失败不影响其余知识：返回的 queued 只统计成功的。
func TestEnableSemanticIndexCountsPartialFailures(t *testing.T) {
	svc, _, kgSvc := newSemanticFixture()
	kgSvc.failed["k-1"] = true

	result, err := svc.EnableSemanticIndex(newTestContext(), "")
	require.NoError(t, err)
	assert.Equal(t, 1, result.Queued)
	assert.Equal(t, []string{"k-2"}, kgSvc.queued)
}

// 没有可用的 embedding 模型 → 2406，且不改 KB、不入队。
func TestEnableSemanticIndexWithoutModelFails(t *testing.T) {
	svc, kbSvc, kgSvc := newSemanticFixture()
	svc.WithSemanticIndex(kbSvc, kgSvc, embeddingModels{})

	_, err := svc.EnableSemanticIndex(newTestContext(), "")
	require.Error(t, err)
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok, "必须是 AppError，got %v", err)
	assert.Equal(t, int(apperrors.ErrWorkbenchNoEmbedding), int(appErr.Code))
	assert.Nil(t, kbSvc.updated, "没有模型时不应改 KB")
	assert.Empty(t, kgSvc.queued)
}

// 显式传的 model_id 不存在或类型不对 → 2406。
func TestEnableSemanticIndexRejectsWrongModel(t *testing.T) {
	svc, _, _ := newSemanticFixture()
	_, err := svc.EnableSemanticIndex(newTestContext(), "missing-model")
	require.Error(t, err)
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	assert.Equal(t, int(apperrors.ErrWorkbenchNoEmbedding), int(appErr.Code))
}

// 工作库已有向量能力时不需要提示。
func TestNeedsSemanticIndexFalseWhenVectorReady(t *testing.T) {
	svc, kbSvc, _ := newSemanticFixture()
	kbSvc.kb.IndexingStrategy = types.IndexingStrategy{KeywordEnabled: true, VectorEnabled: true}
	kbSvc.kb.EmbeddingModelID = "emb-1"

	assert.False(t, svc.NeedsSemanticIndex(newTestContext(), kbSvc.kb))
}
