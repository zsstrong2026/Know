package sop

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

// stubModels 返回预置模型清单。
type stubModels struct{ models []*types.Model }

func (s stubModels) ListModels(_ context.Context) ([]*types.Model, error) {
	return s.models, nil
}

// 助手角色必须自带模型：合成的角色不属于 AgentQA 的内置回退名单，
// 模型为空会直接以「未配置模型」失败（SSE 500 的根因）。
func TestQARunnerAgentFillsModelAndDisablesTools(t *testing.T) {
	runner := NewQARunner(QADeps{
		Models: stubModels{models: []*types.Model{
			{ID: "m-other", Type: types.ModelTypeEmbedding, Status: types.ModelStatusActive},
			{ID: "m-chat", Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive},
		}},
	})
	agent := runner.agent(context.Background(), AIRequest{TenantID: 7, System: "只输出 JSON"})

	require.Equal(t, "m-chat", agent.Config.ModelID)
	require.Equal(t, uint64(7), agent.TenantID)
	require.False(t, agent.IsBuiltin, "非内置，AgentQA 才不会再走内置回退分支")
	require.Equal(t, "none", agent.Config.KBSelectionMode)
	require.Empty(t, agent.Config.AllowedTools)
	require.Equal(t, types.AssistantAccessReadOnly, agent.Config.AccessLevel)
	require.Contains(t, agent.Config.SystemPrompt, "只输出 JSON")
}

// 角色自带模型时保留原值，不被工作区默认覆盖。
func TestQARunnerAgentKeepsConfiguredModel(t *testing.T) {
	runner := NewQARunner(QADeps{
		Role:   &types.CustomAgent{Config: types.CustomAgentConfig{ModelID: "role-model"}},
		Models: stubModels{models: []*types.Model{{ID: "m-chat", Type: types.ModelTypeKnowledgeQA}}},
	})
	agent := runner.agent(context.Background(), AIRequest{TenantID: 1})
	require.Equal(t, "role-model", agent.Config.ModelID)
}

// 工作区没有可用聊天模型时保持为空：让 AgentQA 自己报「模型未配置」。
func TestQARunnerAgentLeavesModelEmptyWithoutCandidates(t *testing.T) {
	runner := NewQARunner(QADeps{Models: stubModels{}})
	agent := runner.agent(context.Background(), AIRequest{TenantID: 1})
	require.Empty(t, agent.Config.ModelID)
}
