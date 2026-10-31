package workbench

import (
	"context"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// NewAIServiceWithDeps 是给依赖注入用的装配入口。
//
// ai.go 里的窄接口让测试只需实现四个方法，但 dig 是按具体类型装配的；
// 这里把宽接口包成窄适配器再交给 NewAIService，业务逻辑与装配细节互不干扰。
//
// retriever / workKB 为 nil 时 /ai/ask 返回 503，内联车道不受影响。
func NewAIServiceWithDeps(
	repo interfaces.WorkbenchRepository,
	models interfaces.ModelService,
	agents interfaces.CustomAgentService,
	cfg *config.Config,
	retriever interfaces.SessionService,
	workKB interfaces.KnowledgeBaseService,
) *AIService {
	service := NewAIService(
		repo, aiModelAdapter{inner: models}, aiAgentAdapter{inner: agents}, aiPromptAdapter{inner: cfg},
	)
	if retriever != nil && workKB != nil {
		service.WithAskLane(retriever, workKB)
	}
	return service
}

// aiModelAdapter 把 interfaces.ModelService 收窄成内联车道需要的两个方法。
type aiModelAdapter struct {
	inner interfaces.ModelService
}

func (a aiModelAdapter) GetChatModel(ctx context.Context, modelID string) (chat.Chat, error) {
	return a.inner.GetChatModel(ctx, modelID)
}

func (a aiModelAdapter) ListModels(ctx context.Context) ([]*types.Model, error) {
	return a.inner.ListModels(ctx)
}

// aiAgentAdapter 只暴露按 ID 取智能体。
type aiAgentAdapter struct {
	inner interfaces.CustomAgentService
}

func (a aiAgentAdapter) GetAgentByID(ctx context.Context, id string) (*types.CustomAgent, error) {
	return a.inner.GetAgentByID(ctx, id)
}

// aiPromptAdapter 复用 config.Config 的 system_prompt_id 解析能力。
type aiPromptAdapter struct {
	inner *config.Config
}

func (a aiPromptAdapter) ResolveCustomAgentPrompts(agent *types.CustomAgent) (string, string) {
	if a.inner == nil {
		return "", ""
	}
	return a.inner.ResolveCustomAgentPrompts(agent)
}
