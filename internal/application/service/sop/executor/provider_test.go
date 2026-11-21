package executor

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// stubProvider 是最小 Provider 实现：只关心 Kind，其余方法直接成功返回。
type stubProvider struct {
	kind string
}

func (s *stubProvider) Kind() string { return s.kind }

func (s *stubProvider) Prepare(_ context.Context, _ *types.SopNode, _ string) error { return nil }

func (s *stubProvider) Execute(_ context.Context, _ TaskBook, _ ExecOptions) (ExecResult, error) {
	return ExecResult{}, nil
}

func (s *stubProvider) Cancel(_ context.Context) error { return nil }

func TestRegistryResolve(t *testing.T) {
	registry := NewRegistry()
	registry.Register(&stubProvider{kind: "cli:zcode"})
	registry.Register(&stubProvider{kind: ProviderKindPlatformAgent})
	registry.Register(&stubProvider{kind: ProviderKindScript})

	tests := []struct {
		name     string
		node     *types.SopNode
		wantKind string
		wantErr  string
	}{
		{
			name: "cli 节点解析为 cli:<cli_kind>",
			node: &types.SopNode{
				ID:       "n1",
				Type:     types.SopNodeTypeAgent,
				Executor: types.SopNodeExecutor{Kind: types.SopExecutorCLI, CLIKind: types.SopCLIKindZCode},
			},
			wantKind: "cli:zcode",
		},
		{
			name: "agent 节点未写 executor.kind 兜底平台智能体",
			node: &types.SopNode{
				ID:       "n2",
				Type:     types.SopNodeTypeAgent,
				Executor: types.SopNodeExecutor{},
			},
			wantKind: ProviderKindPlatformAgent,
		},
		{
			name: "agent 节点显式 platform_agent",
			node: &types.SopNode{
				ID:       "n3",
				Type:     types.SopNodeTypeAgent,
				Executor: types.SopNodeExecutor{Kind: types.SopExecutorPlatformAgent},
			},
			wantKind: ProviderKindPlatformAgent,
		},
		{
			name:     "script 节点解析为 script",
			node:     &types.SopNode{ID: "n4", Type: types.SopNodeTypeScript},
			wantKind: ProviderKindScript,
		},
		{
			name: "未注册的 cli 种类报未就绪",
			node: &types.SopNode{
				ID:       "n5",
				Type:     types.SopNodeTypeAgent,
				Executor: types.SopNodeExecutor{Kind: types.SopExecutorCLI, CLIKind: types.SopCLIKindCursor},
			},
			wantErr: "执行器 cli:cursor-agent 未就绪",
		},
		{
			name: "cli 节点缺 cli_kind 报错",
			node: &types.SopNode{
				ID:       "n6",
				Type:     types.SopNodeTypeAgent,
				Executor: types.SopNodeExecutor{Kind: types.SopExecutorCLI},
			},
			wantErr: "cli_kind",
		},
		{
			name:    "sop 子流程节点不走注册表",
			node:    &types.SopNode{ID: "n7", Type: types.SopNodeTypeSOP, ChildTemplateID: "tpl"},
			wantErr: "子流程",
		},
		{
			name:    "approval 节点不走注册表",
			node:    &types.SopNode{ID: "n8", Type: types.SopNodeTypeApproval},
			wantErr: "审批",
		},
		{
			name:    "nil 节点报错",
			node:    nil,
			wantErr: "节点为空",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := registry.Resolve(tt.node)
			if tt.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErr)
				require.Nil(t, got)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, got)
			require.Equal(t, tt.wantKind, got.Kind())
		})
	}
}

func TestRegistryRegisterOverridesSameKind(t *testing.T) {
	registry := NewRegistry()
	first := &stubProvider{kind: "script"}
	second := &stubProvider{kind: "script"}
	registry.Register(first)
	registry.Register(second)

	got, err := registry.Resolve(&types.SopNode{ID: "n1", Type: types.SopNodeTypeScript})
	require.NoError(t, err)
	require.Same(t, second, got)
}

func TestRegistryConcurrentRegisterAndResolve(t *testing.T) {
	registry := NewRegistry()
	node := &types.SopNode{
		ID:       "n1",
		Type:     types.SopNodeTypeAgent,
		Executor: types.SopNodeExecutor{Kind: types.SopExecutorCLI, CLIKind: types.SopCLIKindZCode},
	}

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			registry.Register(&stubProvider{kind: "cli:zcode"})
		}()
		go func() {
			defer wg.Done()
			_, _ = registry.Resolve(node)
		}()
	}
	wg.Wait()

	got, err := registry.Resolve(node)
	require.NoError(t, err)
	require.Equal(t, "cli:zcode", got.Kind())
}

// TestStreamEventJSONTags 锁定 SSE 线格式：前端（Task 24/38）按此 JSON 消费。
func TestStreamEventJSONTags(t *testing.T) {
	event := StreamEvent{
		Type:     StreamEventTextDelta,
		Text:     "正在分析",
		ToolName: "kb_search",
		Payload:  json.RawMessage(`{"q":"sop"}`),
	}

	raw, err := json.Marshal(event)
	require.NoError(t, err)

	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &fields))
	require.Contains(t, fields, "type")
	require.Contains(t, fields, "text")
	require.Contains(t, fields, "tool_name")
	require.Contains(t, fields, "payload")
	require.Contains(t, fields, "at")

	empty, err := json.Marshal(StreamEvent{Type: StreamEventResult})
	require.NoError(t, err)
	var emptyFields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(empty, &emptyFields))
	require.NotContains(t, emptyFields, "text", "空文本必须省略")
	require.NotContains(t, emptyFields, "tool_name", "空工具名必须省略")
	require.NotContains(t, emptyFields, "payload", "空载荷必须省略")
}
