package engine

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

// 未知的 node.type / check.type 必须在发布与发起运行前被拒绝：跑到一半才炸
// 时错误发生在执行器里，用户看到的是「平台内部错误」，且已消耗一轮 token。
func TestValidateDefinitionRejectsUnknownNodeType(t *testing.T) {
	def := types.SopDefinition{Nodes: []types.SopNode{
		{ID: "n1", Name: "分析", Type: "agent", Executor: types.SopNodeExecutor{Kind: types.SopExecutorPlatformAgent, AgentID: "a"}},
		{ID: "n2", Name: "怪节点", Type: "python"},
	}, Edges: []types.SopEdge{{ID: "e1", Source: "n1", Target: "n2"}}}

	errs := ValidateDefinitionWithSelf(&def, "", true)
	require.NotEmpty(t, errs)
	require.True(t, containsSubstr(errs, "python"), "错误要指出不认识的值：%v", errs)
}

// 未知的产物校验方式同样前置拒绝。
func TestValidateDefinitionRejectsUnknownCheckType(t *testing.T) {
	def := types.SopDefinition{Nodes: []types.SopNode{{
		ID: "n1", Name: "实现", Type: types.SopNodeTypeAgent,
		Executor: types.SopNodeExecutor{Kind: types.SopExecutorPlatformAgent, AgentID: "a"},
		ArtifactOutputs: []types.SopArtifactOutput{{
			Name: "a.md", Path: "out/a.md",
			Check: types.SopArtifactCheck{Type: "regex"},
		}},
	}}}

	errs := ValidateDefinitionWithSelf(&def, "", true)
	require.NotEmpty(t, errs)
	require.True(t, containsSubstr(errs, "regex"), "错误要指出不认识的值：%v", errs)
}

// 四种合法节点类型与三种合法校验方式不得误伤。
func TestValidateDefinitionAcceptsKnownTypes(t *testing.T) {
	def := types.SopDefinition{Nodes: []types.SopNode{
		{ID: "n1", Name: "实现", Type: types.SopNodeTypeAgent,
			Executor: types.SopNodeExecutor{Kind: types.SopExecutorPlatformAgent, AgentID: "a"},
			ArtifactOutputs: []types.SopArtifactOutput{
				{Name: "a", Path: "out/a.md", Check: types.SopArtifactCheck{Type: types.SopArtifactCheckExists}},
				{Name: "b", Path: "out/b.md", Check: types.SopArtifactCheck{Type: types.SopArtifactCheckContains, Keyword: "k"}},
				{Name: "c", Path: "out/c.md", Check: types.SopArtifactCheck{Type: types.SopArtifactCheckCommand, Command: "true"}},
			}},
		{ID: "n2", Name: "脚本", Type: types.SopNodeTypeScript, Script: "echo hi",
			ArtifactOutputs: []types.SopArtifactOutput{{Name: "d", Path: "out/d.txt"}}},
		{ID: "n3", Name: "验收", Type: types.SopNodeTypeApproval},
	}, Edges: []types.SopEdge{
		{ID: "e1", Source: "n1", Target: "n2"}, {ID: "e2", Source: "n2", Target: "n3"},
	}}
	require.Empty(t, ValidateDefinitionWithSelf(&def, "", true))
}

// 大小写/空白不放过：落库口径是精确值，'Agent' 与 ' agent ' 都要被拒。
func TestValidateDefinitionRejectsCaseAndSpaceVariants(t *testing.T) {
	for _, typ := range []string{"Agent", " agent "} {
		def := types.SopDefinition{Nodes: []types.SopNode{
			{ID: "n1", Name: "实现", Type: typ,
				Executor: types.SopNodeExecutor{Kind: types.SopExecutorPlatformAgent, AgentID: "a"}},
		}}
		require.NotEmpty(t, ValidateDefinitionWithSelf(&def, "", true), "type=%q 应被拒绝", typ)
	}
}

func containsSubstr(list []string, want string) bool {
	for _, s := range list {
		if strings.Contains(s, want) {
			return true
		}
	}
	return false
}
