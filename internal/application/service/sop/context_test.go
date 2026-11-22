package sop

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// ---- stub 仓储 ----

// stubRoles 按 id 返回预置角色；SelectedSkills=[a b] 供技能子集断言。
type stubRoles struct{ role *types.CustomAgent }

func (s stubRoles) GetAgentByID(_ context.Context, id string, _ uint64) (*types.CustomAgent, error) {
	if s.role == nil || s.role.ID != id {
		return nil, errors.New("agent not found")
	}
	return s.role, nil
}

// stubProjects 返回项目 KB=kb-project。
type stubProjects struct {
	kbID string
	err  error
}

func (s stubProjects) GetByID(_ context.Context, _ uint64, id string) (*types.SopProject, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &types.SopProject{ID: id, KBID: s.kbID}, nil
}

// stubItems 返回事项 KB（空=不覆盖项目 KB）。
type stubItems struct {
	kbID string
}

func (s stubItems) GetByID(_ context.Context, _ uint64, id string) (*types.SopItem, error) {
	return &types.SopItem{ID: id, KBID: s.kbID}, nil
}

// stubMCPs 提供 know-kb 一个服务。
type stubMCPs struct{ services []*types.MCPService }

func (s stubMCPs) List(_ context.Context, _ uint64) ([]*types.MCPService, error) {
	return s.services, nil
}

// stubKBs 返回租户全部 KB。
type stubKBs struct{ ids []string }

func (s stubKBs) ListKnowledgeBasesByTenantID(_ context.Context, _ uint64) ([]*types.KnowledgeBase, error) {
	out := make([]*types.KnowledgeBase, 0, len(s.ids))
	for _, id := range s.ids {
		out = append(out, &types.KnowledgeBase{ID: id})
	}
	return out, nil
}

// stubCreds 返回预置凭据（APIKey 已由模型解密，这里直接给明文）。
type stubCreds struct {
	creds []types.SopCLICredential
	err   error
}

func (s stubCreds) ListByOwner(_ context.Context, _ uint64, _ string) ([]types.SopCLICredential, error) {
	return s.creds, s.err
}

// newTestResolver 组装一份标准依赖：角色（技能 a/b）、know-kb、项目 KB=kb-project。
func newTestResolver() *ContextResolver {
	return NewContextResolver(ResolverDeps{
		Agents: stubRoles{role: &types.CustomAgent{
			ID:     "a1",
			Config: types.CustomAgentConfig{SelectedSkills: []string{"a", "b"}, ModelID: "role-model"},
		}},
		Projects: stubProjects{kbID: "kb-project"},
		Items:    stubItems{},
		MCPs:     stubMCPs{services: []*types.MCPService{{ID: "mcp-1", Name: "know-kb"}}},
		KBs:      stubKBs{ids: []string{"kb-1", "kb-2"}},
	})
}

func TestContextResolverSkillsAndMCPSubset(t *testing.T) {
	r := newTestResolver()
	run := &types.SopRun{TenantID: 1, ProjectID: "p1"}
	node := &types.SopNode{
		Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a1", KBScope: "project"},
		Skills:   []string{"a"}, MCP: []string{"know-kb"},
	}
	got, err := r.Resolve(context.Background(), run, node)
	require.NoError(t, err)
	require.Equal(t, []string{"a"}, got.Skills)
	require.Len(t, got.MCPServers, 1)
	require.Equal(t, "mcp-1", got.MCPServers[0].ID)
	require.Equal(t, []string{"kb-project"}, got.KBIDs)
	require.Equal(t, "a1", got.Role.ID)
	require.Equal(t, "role-model", got.Model)
}

func TestContextResolverExplicitEmptySkillsAndMCP(t *testing.T) {
	r := newTestResolver()
	run := &types.SopRun{TenantID: 1, ProjectID: "p1"}
	node := &types.SopNode{
		Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a1", KBScope: "project"},
		Skills:   []string{}, MCP: []string{},
	}
	got, err := r.Resolve(context.Background(), run, node)
	require.NoError(t, err)
	require.Empty(t, got.Skills)      // 显式一个都不要
	require.NotNil(t, got.MCPServers) // 显式空数组：不是 nil（nil=角色全部）
	require.Empty(t, got.MCPServers)
}

func TestContextResolverKBScopeProjectItemOverridesProject(t *testing.T) {
	r := NewContextResolver(ResolverDeps{
		Projects: stubProjects{kbID: "kb-project"},
		Items:    stubItems{kbID: "kb-item"},
	})
	run := &types.SopRun{TenantID: 1, ProjectID: "p1", ItemID: "i1"}
	node := &types.SopNode{Executor: types.SopNodeExecutor{KBScope: "project"}}
	got, err := r.Resolve(context.Background(), run, node)
	require.NoError(t, err)
	require.Equal(t, []string{"kb-item"}, got.KBIDs)
}

func TestContextResolverKBScopeFallbacks(t *testing.T) {
	r := NewContextResolver(ResolverDeps{
		Projects: stubProjects{kbID: "kb-project"},
		Items:    stubItems{}, // 事项没绑 KB → 不覆盖
		KBs:      stubKBs{ids: []string{"kb-1", "kb-2"}},
	})
	run := &types.SopRun{TenantID: 1, ProjectID: "p1", ItemID: "i1"}

	// project：事项 KB 为空 → 项目 KB
	got, err := r.Resolve(context.Background(), run,
		&types.SopNode{Executor: types.SopNodeExecutor{KBScope: "project"}})
	require.NoError(t, err)
	require.Equal(t, []string{"kb-project"}, got.KBIDs)

	// custom：用节点勾选
	got, err = r.Resolve(context.Background(), run,
		&types.SopNode{Executor: types.SopNodeExecutor{KBScope: "custom", KBIDs: []string{" kb-9 ", "kb-9"}}})
	require.NoError(t, err)
	require.Equal(t, []string{"kb-9"}, got.KBIDs)

	// all：租户全部
	got, err = r.Resolve(context.Background(), run,
		&types.SopNode{Executor: types.SopNodeExecutor{KBScope: "all"}})
	require.NoError(t, err)
	require.Equal(t, []string{"kb-1", "kb-2"}, got.KBIDs)

	// none / 未知值：不检索
	for _, scope := range []string{"none", "unknown"} {
		got, err = r.Resolve(context.Background(), run,
			&types.SopNode{Executor: types.SopNodeExecutor{KBScope: scope}})
		require.NoError(t, err)
		require.Empty(t, got.KBIDs)
	}
}

func TestContextResolverProjectWithoutKBIsEmpty(t *testing.T) {
	r := NewContextResolver(ResolverDeps{Projects: stubProjects{}})
	run := &types.SopRun{TenantID: 1, ProjectID: "p1"}
	got, err := r.Resolve(context.Background(), run, &types.SopNode{Executor: types.SopNodeExecutor{KBScope: "project"}})
	require.NoError(t, err)
	require.Empty(t, got.KBIDs)
}

func TestContextResolverMCPNilMeansRoleServices(t *testing.T) {
	r := NewContextResolver(ResolverDeps{
		Agents: stubRoles{role: &types.CustomAgent{
			ID:     "a1",
			Config: types.CustomAgentConfig{MCPServices: []string{"mcp-2"}},
		}},
		MCPs: stubMCPs{services: []*types.MCPService{
			{ID: "mcp-1", Name: "know-kb"},
			{ID: "mcp-2", Name: "github"},
		}},
	})
	run := &types.SopRun{TenantID: 1}
	node := &types.SopNode{Executor: types.SopNodeExecutor{AgentID: "a1"}} // MCP 未设置（nil）
	got, err := r.Resolve(context.Background(), run, node)
	require.NoError(t, err)
	require.Len(t, got.MCPServers, 1)
	require.Equal(t, "mcp-2", got.MCPServers[0].ID)
}

func TestContextResolverUnknownMCPNameDropped(t *testing.T) {
	r := newTestResolver()
	run := &types.SopRun{TenantID: 1}
	node := &types.SopNode{MCP: []string{"know-kb", "gone"}}
	got, err := r.Resolve(context.Background(), run, node)
	require.NoError(t, err)
	require.Len(t, got.MCPServers, 1)
}

func TestContextResolverModelOverrideWins(t *testing.T) {
	r := newTestResolver()
	run := &types.SopRun{TenantID: 1}
	node := &types.SopNode{Executor: types.SopNodeExecutor{AgentID: "a1", Model: "node-model"}}
	got, err := r.Resolve(context.Background(), run, node)
	require.NoError(t, err)
	require.Equal(t, "node-model", got.Model)
}

func TestContextResolverCredentialByID(t *testing.T) {
	r := NewContextResolver(ResolverDeps{Credentials: stubCreds{creds: []types.SopCLICredential{
		{ID: "c1", CLIKind: types.SopCLIKindZCode, APIKey: "sk-1"},
		{ID: "c2", CLIKind: types.SopCLIKindClaude, APIKey: "sk-2"},
	}}})
	run := &types.SopRun{TenantID: 1, CreatedBy: "u1"}
	node := &types.SopNode{Executor: types.SopNodeExecutor{Kind: types.SopExecutorCLI, CredentialID: "c2"}}
	got, err := r.Resolve(context.Background(), run, node)
	require.NoError(t, err)
	require.NotNil(t, got.Credential)
	require.Equal(t, "sk-2", got.Credential.APIKey)
}

func TestContextResolverMissingCredentialPlainError(t *testing.T) {
	r := NewContextResolver(ResolverDeps{Credentials: stubCreds{}})
	run := &types.SopRun{TenantID: 1}
	node := &types.SopNode{Executor: types.SopNodeExecutor{Kind: types.SopExecutorCLI, CredentialID: "c9"}}
	_, err := r.Resolve(context.Background(), run, node)
	require.Error(t, err)
	require.Contains(t, err.Error(), "凭据")
}

func TestContextResolverCLIRoleIDUsedForTaskBook(t *testing.T) {
	roleID := "a1"
	r := NewContextResolver(ResolverDeps{Agents: stubRoles{role: &types.CustomAgent{
		ID: "a1", Config: types.CustomAgentConfig{SelectedSkills: []string{"a"}},
	}}})
	run := &types.SopRun{TenantID: 1}
	node := &types.SopNode{
		Type:     types.SopNodeTypeAgent,
		Executor: types.SopNodeExecutor{Kind: types.SopExecutorCLI, CLIKind: types.SopCLIKindZCode, RoleID: &roleID},
		Skills:   nil, // nil=角色全部
	}
	got, err := r.Resolve(context.Background(), run, node)
	require.NoError(t, err)
	require.NotNil(t, got.Role)
	require.Equal(t, []string{"a"}, got.Skills)
}

func TestContextResolverNilNode(t *testing.T) {
	_, err := newTestResolver().Resolve(context.Background(), &types.SopRun{TenantID: 1}, nil)
	require.Error(t, err)
}

func TestContextResolverRoleReadErrorSurfaces(t *testing.T) {
	r := NewContextResolver(ResolverDeps{Agents: stubRoles{}}) // 角色不存在
	run := &types.SopRun{TenantID: 1}
	node := &types.SopNode{Executor: types.SopNodeExecutor{AgentID: "missing"}}
	_, err := r.Resolve(context.Background(), run, node)
	require.Error(t, err)
	require.Contains(t, err.Error(), "读取执行角色失败")
}
