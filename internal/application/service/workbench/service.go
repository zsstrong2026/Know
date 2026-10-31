// Package workbench 实现工作台的服务层：条目 CRUD、内容保存、版本历史、文件夹。
//
// 分层约定：
//   - handler 只做参数绑定与响应封装，不写业务规则；
//   - 本包的每个方法都从 ctx 取 (tenantID, ownerID)，不接受调用方传入，
//     因此“跨用户访问”在类型层面就无法表达；
//   - 仓储的 gorm.ErrRecordNotFound 统一由本包翻译成 2400（404），
//     绝不用 403，避免通过错误码探测他人资源是否存在。
package workbench

import (
	"context"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// SaveResult 是保存成功的返回体，复用 types 里已定义的响应结构，
// 避免 handler 与前端各认一套字段名。
type SaveResult = types.SaveWorkbenchContentResult

// archiveKBService 是归档写路径所需的最小知识库能力面：
// 只需要拿到（必要时懒创建）当前用户的私有工作知识库。
type archiveKBService interface {
	EnsureWorkKnowledgeBase(ctx context.Context, tenantID uint64, userID string) (*types.KnowledgeBase, error)
}

// archiveKnowledgeService 是归档写路径所需的最小知识能力面。
// 用窄接口而不是完整 interfaces.KnowledgeService，测试里可以只实现这几个方法。
type archiveKnowledgeService interface {
	CreateKnowledgeFromManualWithID(
		ctx context.Context, kbID, knowledgeID string,
		payload *types.ManualKnowledgePayload, channel string,
	) (*types.Knowledge, error)
	UpdateManualKnowledge(
		ctx context.Context, knowledgeID string, payload *types.ManualKnowledgePayload,
	) (*types.Knowledge, error)
	DeleteKnowledge(ctx context.Context, id string) error
	GetKnowledgeBatch(ctx context.Context, tenantID uint64, ids []string) ([]*types.Knowledge, error)
}

// docTaskLister 是离开预览所需的最小文档任务能力面。
// 只需统计“有多少章节文档”，不加载正文，也不触发任何写操作。
type docTaskLister interface {
	ListTasks(ctx context.Context) ([]*types.DocGenerationTask, error)
}

// Service 工作台服务。
type Service struct {
	repo interfaces.WorkbenchRepository

	// 归档写路径依赖；未注入时归档接口返回 503，其余端点不受影响。
	kbService archiveKBService
	knowledge archiveKnowledgeService
	// 章节文档计数（离开预览、bootstrap）；未注入时按 0 处理。
	docTasks docTaskLister
	// 发布门禁（§9.1）：只用于 bootstrap 下发，路由注册由 router 侧判定。
	features types.WorkbenchFeatureToggles
}

// NewService 构造服务。
func NewService(repo interfaces.WorkbenchRepository) *Service {
	return &Service{repo: repo}
}

// WithArchiveServices 注入归档所需的写依赖，返回自身便于链式装配。
// 单独开一个方法而不是改构造签名：条目/文件夹端点不需要知识库依赖，
// 既有测试与 wiring 也不必为了归档而改。
func (s *Service) WithArchiveServices(
	kbService archiveKBService, knowledge archiveKnowledgeService,
) *Service {
	s.kbService = kbService
	s.knowledge = knowledge
	return s
}

// WithDocTasks 注入章节文档列表（离开预览与 bootstrap 计数用），返回自身便于链式装配。
func (s *Service) WithDocTasks(tasks docTaskLister) *Service {
	s.docTasks = tasks
	return s
}

// WithFeatures 注入发布门禁状态（bootstrap 下发用），返回自身便于链式装配。
func (s *Service) WithFeatures(features types.WorkbenchFeatureToggles) *Service {
	s.features = features
	return s
}

// callerScope 从 ctx 取调用者的租户与用户。工作台数据严格按用户隔离，
// 两者缺一都按未认证处理（401），不做“退化成公共空间”的兜底。
func callerScope(ctx context.Context) (tenantID uint64, userID string, err error) {
	caller := types.CallerFromContext(ctx)
	if caller.TenantID == 0 {
		return 0, "", apperrors.NewUnauthorizedError("workspace context is required")
	}
	if caller.UserID == "" {
		return 0, "", apperrors.NewUnauthorizedError("user context is required")
	}
	if types.IsSyntheticUserID(caller.UserID) {
		// API Key 主体没有个人工作台；路由层已用 DenyAPIKeyPrincipal 拦截，
		// 这里再兜一层，防止未来有代码绕过中间件直接调服务。
		return 0, "", apperrors.NewUnauthorizedError("workbench requires a real user")
	}
	return caller.TenantID, caller.UserID, nil
}

// notFound 是“资源不存在或不属于当前用户”的唯一出口。
func notFound(message string) error {
	if message == "" {
		message = "workbench resource not found"
	}
	return apperrors.NewWorkbenchNotFoundError(message)
}
