package service

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// pmKBService 项目服务只依赖知识库的建/删两个能力，收窄后可注入轻量 fake
type pmKBService interface {
	CreateKnowledgeBase(ctx context.Context, kb *types.KnowledgeBase) (*types.KnowledgeBase, error)
	DeleteKnowledgeBase(ctx context.Context, id string) error
}

// pmSessionService 项目服务只依赖会话创建与删除
type pmSessionService interface {
	CreateSession(ctx context.Context, session *types.Session) (*types.Session, error)
	DeleteSession(ctx context.Context, id string) error
}

// pmModelService 项目服务只依赖模型列表（用于挑选默认 Embedding 模型）
type pmModelService interface {
	ListModels(ctx context.Context) ([]*types.Model, error)
}

type pmProjectService struct {
	projectRepo  interfaces.PMProjectRepository
	kbService    pmKBService
	sessionSvc   pmSessionService
	kbDirService interfaces.PMKBDirService
	modelSvc     pmModelService
}

// NewPMProjectService 宽接口（KnowledgeBaseService / SessionService / ModelService）可直接传入，
// 编译器按方法集自动满足窄接口
func NewPMProjectService(
	projectRepo interfaces.PMProjectRepository,
	kbService pmKBService,
	sessionSvc pmSessionService,
	kbDirService interfaces.PMKBDirService,
	modelSvc pmModelService,
) interfaces.PMProjectService {
	return &pmProjectService{
		projectRepo:  projectRepo,
		kbService:    kbService,
		sessionSvc:   sessionSvc,
		kbDirService: kbDirService,
		modelSvc:     modelSvc,
	}
}

// CreateProject 创建或关联项目。
// isNew=true：动态建知识库 + 常驻 PM 会话；同名项目已存在时直接复用，避免重复建库。
// isNew=false：仅关联同名项目，不存在则把 notFound 交给 handler 映射 404。
func (s *pmProjectService) CreateProject(
	ctx context.Context, tenantID uint64, userID string, name string, isNew bool,
) (*types.PMProject, error) {
	// 下游知识库/会话服务从 ctx 取租户，这里统一注入，避免调用方遗漏
	ctx = context.WithValue(ctx, types.TenantIDContextKey, tenantID)

	if !isNew {
		return s.projectRepo.GetByName(ctx, tenantID, name)
	}

	// 先按名预检：避免先建库再因重名失败，留下孤儿资源
	existing, err := s.projectRepo.GetByName(ctx, tenantID, name)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, repository.ErrPMProjectNotFound) {
		return nil, err
	}

	kb, err := s.kbService.CreateKnowledgeBase(ctx, &types.KnowledgeBase{
		Name:             name,
		EmbeddingModelID: s.defaultEmbeddingModelID(ctx),
	})
	if err != nil {
		return nil, err
	}

	// 建会话失败时回滚知识库，避免孤儿库
	session, err := s.sessionSvc.CreateSession(ctx, &types.Session{
		Title:    name + " - PM 工作站",
		TenantID: tenantID,
		UserID:   userID,
	})
	if err != nil {
		s.deleteKBQuietly(ctx, kb.ID)
		return nil, err
	}

	// 目录初始化失败仅告警，不阻断项目创建（目录可后续补建）
	if err := s.kbDirService.EnsureProjectDirs(ctx, tenantID, kb.ID); err != nil {
		logger.Warnf(ctx, "PM 项目目录初始化失败: project=%s kb=%s err=%v", name, kb.ID, err)
	}

	p := &types.PMProject{
		TenantID:  tenantID,
		Name:      name,
		KBID:      kb.ID,
		SessionID: session.ID,
		Status:    types.PMProjectStatusActive,
		OwnerID:   userID,
		CreatedBy: userID,
	}
	if err := s.projectRepo.Create(ctx, p); err != nil {
		s.deleteKBQuietly(ctx, kb.ID)
		return nil, err
	}
	return p, nil
}

// defaultEmbeddingModelID 取当前租户的默认 Embedding 模型。
// PM 自动建库必须写入该字段，否则知识库解析阶段会因模型 ID 为空直接失败；
// 未配置时返回空串并告警，让项目仍可创建（后续可在知识库设置中补配）。
func (s *pmProjectService) defaultEmbeddingModelID(ctx context.Context) string {
	if s.modelSvc == nil {
		return ""
	}
	models, err := s.modelSvc.ListModels(ctx)
	if err != nil {
		logger.Warnf(ctx, "PM 项目查询默认嵌入模型失败: err=%v", err)
		return ""
	}
	for _, m := range models {
		if m.IsDefault && m.Type == types.ModelTypeEmbedding {
			return m.ID
		}
	}
	logger.Warnf(ctx, "PM 项目未找到默认嵌入模型: tenant=%d", types.MustTenantIDFromContext(ctx))
	return ""
}

func (s *pmProjectService) GetProject(
	ctx context.Context, tenantID uint64, id string,
) (*types.PMProject, error) {
	return s.projectRepo.GetByID(ctx, tenantID, id)
}

func (s *pmProjectService) ListProjects(
	ctx context.Context, tenantID uint64, status string,
) ([]*types.PMProject, error) {
	return s.projectRepo.List(ctx, tenantID, status)
}

// UpdateProject 名称空串表示不修改，描述可清空
func (s *pmProjectService) UpdateProject(
	ctx context.Context, tenantID uint64, id string, name string, description string,
) (*types.PMProject, error) {
	p, err := s.projectRepo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if name != "" {
		p.Name = name
	}
	p.Description = description
	if err := s.projectRepo.Update(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

// ArchiveProject 归档 / 恢复：幂等操作，不删除任何知识库内容
func (s *pmProjectService) ArchiveProject(
	ctx context.Context, tenantID uint64, id string, archived bool,
) error {
	p, err := s.projectRepo.GetByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	status := types.PMProjectStatusActive
	if archived {
		status = types.PMProjectStatusArchived
	}
	if p.Status == status {
		return nil
	}
	p.Status = status
	return s.projectRepo.Update(ctx, p)
}

// deleteKBQuietly 清理知识库（创建回滚 / 项目删除），失败只告警
func (s *pmProjectService) deleteKBQuietly(ctx context.Context, kbID string) {
	if err := s.kbService.DeleteKnowledgeBase(ctx, kbID); err != nil {
		logger.Warnf(ctx, "PM 项目清理知识库失败: kb=%s err=%v", kbID, err)
	}
}

// DeleteProject 删除项目及其全部业务数据；项目记录删成功后再清理知识库与会话，
// 下游清理失败只告警，避免已删除的项目因外部资源回收失败而无法再次删除。
func (s *pmProjectService) DeleteProject(ctx context.Context, tenantID uint64, id string) error {
	// 下游知识库/会话服务从 ctx 取租户，先注入再调用
	ctx = context.WithValue(ctx, types.TenantIDContextKey, tenantID)

	p, err := s.projectRepo.GetByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if err := s.projectRepo.Delete(ctx, tenantID, id); err != nil {
		return err
	}
	if p.KBID != "" {
		s.deleteKBQuietly(ctx, p.KBID)
	}
	if p.SessionID != "" {
		if err := s.sessionSvc.DeleteSession(ctx, p.SessionID); err != nil {
			logger.Warnf(ctx, "PM 项目删除会话失败: session=%s err=%v", p.SessionID, err)
		}
	}
	return nil
}
