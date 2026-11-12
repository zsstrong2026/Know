package workbench

import (
	"context"
	"strings"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// semanticKBService 是语义索引所需的「改绑模型」能力面（§5.4）。
//
// 只暴露 UpdateKnowledgeBase 一个方法：工作知识库换 embedding 模型是唯一
// 需要写 KB 行的动作，别的字段一律不碰。
type semanticKBService interface {
	UpdateKnowledgeBase(
		ctx context.Context, id, name, description string,
		config *types.KnowledgeBaseConfig, vlmConfig *types.VLMConfig,
	) (*types.KnowledgeBase, error)
}

// semanticKnowledgeService 是语义索引所需的知识能力面。
//
// 重建走既有的 ReparseKnowledge：它会清旧索引、按当前 KB 的 embedding 配置
// 重新 chunk + 向量化，与“启用语义检索”语义一致，不新造一套索引逻辑。
type semanticKnowledgeService interface {
	ListKnowledgeByKnowledgeBaseID(ctx context.Context, kbID string) ([]*types.Knowledge, error)
	ReparseKnowledge(
		ctx context.Context, knowledgeID string, processOverrides *types.KnowledgeProcessOverrides,
	) (*types.Knowledge, error)
}

// semanticModelProvider 是选模型所需的最小能力面。
type semanticModelProvider interface {
	ListModels(ctx context.Context) ([]*types.Model, error)
}

// WithSemanticIndex 注入语义索引依赖，返回自身便于链式装配。
func (s *Service) WithSemanticIndex(
	kb semanticKBService, knowledge semanticKnowledgeService, models semanticModelProvider,
) *Service {
	s.semanticKB = kb
	s.semanticKnowledge = knowledge
	s.semanticModels = models
	return s
}

// EnableSemanticIndex 为当前用户的工作知识库绑定 embedding 模型并重建索引（§7.8）。
//
// 只操作「当前用户自己的」工作 KB：接口不接受 kb_id，避免变成任意知识库重建入口。
// 没有可用 Embedding 模型时返回 2406，前端提示“先去配置模型”。
func (s *Service) EnableSemanticIndex(
	ctx context.Context, modelID string,
) (*types.WorkbenchReindexResult, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	if s.kbService == nil || s.semanticKB == nil || s.semanticKnowledge == nil {
		return nil, apperrors.NewServiceUnavailableError(
			"workbench semantic index is not configured")
	}
	kb, err := s.kbService.EnsureWorkKnowledgeBase(ctx, tenantID, ownerID)
	if err != nil {
		return nil, err
	}

	resolved, err := s.resolveEmbeddingModel(ctx, strings.TrimSpace(modelID))
	if err != nil {
		return nil, err
	}

	// 改绑模型 + 打开向量索引：只改这两项，其余配置原样保留。
	// 语义检索不应该单独关掉关键词：两者互补，且老数据可能只有关键词索引。
	strategy := kb.IndexingStrategy
	strategy.VectorEnabled = true
	strategy.KeywordEnabled = true
	embeddingModelID := resolved.ID
	if _, err := s.semanticKB.UpdateKnowledgeBase(ctx, kb.ID, kb.Name, kb.Description,
		&types.KnowledgeBaseConfig{
			IndexingStrategy: &strategy,
			EmbeddingModelID: &embeddingModelID,
		}, nil); err != nil {
		return nil, err
	}

	rows, err := s.semanticKnowledge.ListKnowledgeByKnowledgeBaseID(ctx, kb.ID)
	if err != nil {
		return nil, err
	}
	queued := 0
	failed := 0
	for _, row := range rows {
		if row == nil || strings.TrimSpace(row.ID) == "" {
			continue
		}
		if _, err := s.semanticKnowledge.ReparseKnowledge(ctx, row.ID, nil); err != nil {
			failed++
			logger.Warnf(ctx, "workbench: semantic index reparse failed for knowledge %s: %v", row.ID, err)
			continue
		}
		queued++
	}
	logger.Infof(ctx, "workbench: semantic index enabled for kb %s, queued=%d failed=%d model=%s",
		kb.ID, queued, failed, resolved.ID)
	return &types.WorkbenchReindexResult{Queued: queued, ModelID: resolved.ID}, nil
}

// resolveEmbeddingModel 解析目标 embedding 模型：显式 model_id 必须存在且类型正确，
// 为空时取默认（无默认则取第一个 active 的 embedding 模型）。都没有返回 2406。
func (s *Service) resolveEmbeddingModel(ctx context.Context, modelID string) (*types.Model, error) {
	if s.semanticModels == nil {
		return nil, apperrors.NewWorkbenchNoEmbedding()
	}
	models, err := s.semanticModels.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	if modelID != "" {
		for _, m := range models {
			if m == nil || m.ID != modelID {
				continue
			}
			if m.Type != types.ModelTypeEmbedding || m.Status != types.ModelStatusActive {
				return nil, apperrors.NewWorkbenchNoEmbedding()
			}
			return m, nil
		}
		return nil, apperrors.NewWorkbenchNoEmbedding()
	}
	picked := types.PickDefaultModel(models, types.ModelTypeEmbedding)
	if picked == nil {
		return nil, apperrors.NewWorkbenchNoEmbedding()
	}
	return picked, nil
}

// NeedsSemanticIndex 计算是否需要提示“启用语义检索”（§7.3）。
//
// 判定：工作库没有可用向量能力，且空间里至少有一个 active 的 embedding 模型。
// 没有模型时返回 false —— 此时按钮点了也必然失败，应该引导去配模型而不是重建。
func (s *Service) NeedsSemanticIndex(ctx context.Context, kb *types.KnowledgeBase) bool {
	if kb == nil {
		return false
	}
	if kb.IndexingStrategy.VectorEnabled && kb.EmbeddingModelID != "" {
		return false
	}
	if s.semanticModels == nil {
		return false
	}
	models, err := s.semanticModels.ListModels(ctx)
	if err != nil {
		logger.Warnf(ctx, "workbench: failed to list models for semantic index hint: %v", err)
		return false
	}
	return types.PickDefaultModel(models, types.ModelTypeEmbedding) != nil
}
