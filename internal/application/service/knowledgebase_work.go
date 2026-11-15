package service

import (
	"context"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
)

// activeMembershipLookup 是工作库懒创建所需的成员校验最小接口，
// 便于在测试中注入替身而不必装配整套依赖。
type activeMembershipLookup interface {
	Get(ctx context.Context, userID string, tenantID uint64) (*types.TenantMember, error)
}

// EnsureWorkKnowledgeBase 返回用户的私有工作知识库，首次调用时懒创建。
//
// 可见性与生命周期边界：
//   - 租户 API Key 主体（含合成用户 system-<tenantID>）没有个人工作库；
//   - 用户必须属于该租户且成员状态为 active；
//   - (tenant_id, creator_id, system_key='work') 由部分唯一索引保证唯一，
//     并发调用时先写入者胜出，其余调用回查后返回同一行。
func (s *knowledgeBaseService) EnsureWorkKnowledgeBase(
	ctx context.Context, tenantID uint64, userID string,
) (*types.KnowledgeBase, error) {
	// API Key 请求携带的是服务身份，不能代表某个真实用户创建私有工作库。
	if _, ok := types.TenantAPIKeyScopeFromContext(ctx); ok {
		return nil, apperrors.NewForbiddenError("work knowledge base requires a real user")
	}
	if tenantID == 0 || userID == "" || types.IsSyntheticUserID(userID) {
		return nil, apperrors.NewBadRequestError("invalid user or workspace")
	}
	if err := s.requireActiveMember(ctx, tenantID, userID); err != nil {
		return nil, err
	}

	existing, err := s.repo.GetWorkKnowledgeBase(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}

	createCtx, err := s.workKBCreateContext(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}
	created, createErr := s.CreateKnowledgeBase(createCtx, s.buildWorkKnowledgeBase(createCtx, tenantID, userID))
	if createErr == nil {
		return created, nil
	}

	// 并发创建时唯一索引会拒绝重复写入，此时回查并返回先到的那一行；
	// 仍查不到才返回原始错误，避免把唯一冲突暴露给调用方。
	if existing, lookupErr := s.repo.GetWorkKnowledgeBase(ctx, tenantID, userID); lookupErr == nil && existing != nil {
		return existing, nil
	}
	logger.ErrorWithFields(ctx, createErr, map[string]interface{}{
		"tenant_id":  tenantID,
		"system_key": types.KBSystemKeyWork,
	})
	return nil, createErr
}

// PurgeWorkKnowledgeBase 硬删用户的工作知识库及其内容。
//
// 与 DeleteKnowledgeBase 的区别：后者走软删 + 异步清理，且面向普通 KB；
// 这里用于 workspace 解散与账号清理，必须：
//   - 只接受 is_system=true 且 system_key='work' 的 KB（防止被当成任意删除入口）；
//   - 复用同一套异步清理链路（知识/chunk/向量/文件），避免两套删除逻辑分叉。
func (s *knowledgeBaseService) PurgeWorkKnowledgeBase(ctx context.Context, kbID string) error {
	if kbID == "" {
		return apperrors.NewBadRequestError("knowledge base id is required")
	}
	kb, err := s.repo.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return err
	}
	if kb == nil || !kb.IsSystem || kb.SystemKey != types.KBSystemKeyWork {
		// 不属于工作知识库时按“不允许”处理，不透露该 id 是否存在。
		return apperrors.NewWorkbenchSystemKBError("not a work knowledge base")
	}
	// 清理流程需要 tenant 上下文；调用方（清理任务/解散流程）可能只带了 KB。
	purgeCtx := context.WithValue(ctx, types.TenantIDContextKey, kb.TenantID)
	return s.DeleteKnowledgeBase(purgeCtx, kbID)
}

// requireActiveMember 校验用户在该租户下的成员状态；未注入仓储时跳过（测试场景）。
func (s *knowledgeBaseService) requireActiveMember(ctx context.Context, tenantID uint64, userID string) error {
	if s.members == nil {
		return nil
	}
	member, err := s.members.Get(ctx, userID, tenantID)
	if err != nil {
		return err
	}
	if member == nil || member.Status != types.TenantMemberStatusActive {
		return apperrors.NewForbiddenError("user is not an active member of this workspace")
	}
	return nil
}

// workKBCreateContext 构造「以该用户身份、在该租户内执行」的创建上下文，
// 使 CreateKnowledgeBase 记录正确的租户、创建者与存储默认值。
func (s *knowledgeBaseService) workKBCreateContext(
	ctx context.Context, tenantID uint64, userID string,
) (context.Context, error) {
	role := types.CallerFromContext(ctx).Role
	createCtx := context.WithValue(ctx, types.UserIDContextKey, userID)
	createCtx = types.WithCaller(createCtx, types.Caller{TenantID: tenantID, UserID: userID, Role: role})
	createCtx = types.WithExecutionTenant(createCtx, tenantID)
	if s.tenantRepo == nil {
		return createCtx, nil
	}
	tenant, err := s.tenantRepo.GetTenantByID(createCtx, tenantID)
	if err != nil {
		return nil, err
	}
	if tenant != nil {
		createCtx = context.WithValue(createCtx, types.TenantInfoContextKey, tenant)
	}
	return createCtx, nil
}

// buildWorkKnowledgeBase 组装工作知识库的初始配置：系统标记 + 私有可见性，
// 并在有可用向量模型时绑定默认 embedding 模型。
func (s *knowledgeBaseService) buildWorkKnowledgeBase(
	ctx context.Context, tenantID uint64, userID string,
) *types.KnowledgeBase {
	kb := &types.KnowledgeBase{
		ID:          uuid.New().String(),
		TenantID:    tenantID,
		CreatorID:   userID,
		Name:        types.KBNameWork,
		Type:        types.KnowledgeBaseTypeDocument,
		Description: "我的墨匣文档与生成资料的私有归档",
		IsSystem:    true,
		SystemKey:   types.KBSystemKeyWork,
		Visibility:  types.KBVisibilityPrivate,
	}
	kb.EnsureDefaults()
	if modelID := s.pickEmbeddingModelID(ctx); modelID != "" {
		kb.EmbeddingModelID = modelID
		return kb
	}
	// 没有可用向量模型时只开关键词索引，避免创建工作库因缺模型而失败。
	kb.EmbeddingModelID = ""
	kb.IndexingStrategy = types.IndexingStrategy{KeywordEnabled: true, VectorEnabled: false}
	return kb
}

// pickEmbeddingModelID 选择默认 embedding 模型：优先 IsDefault，其次最早创建。
func (s *knowledgeBaseService) pickEmbeddingModelID(ctx context.Context) string {
	if s.modelService == nil {
		return ""
	}
	models, err := s.modelService.ListModels(ctx)
	if err != nil {
		logger.Warnf(ctx, "failed to list embedding models for work KB: %v", err)
		return ""
	}
	var picked *types.Model
	for _, model := range models {
		if model == nil || model.Type != types.ModelTypeEmbedding || model.Status != types.ModelStatusActive {
			continue
		}
		if picked == nil || (model.IsDefault && !picked.IsDefault) ||
			(model.IsDefault == picked.IsDefault && model.CreatedAt.Before(picked.CreatedAt)) {
			picked = model
		}
	}
	if picked == nil {
		return ""
	}
	return picked.ID
}
