// context.go 是 engine.NodeResolver 的真实实现：把「节点配置了哪个角色 / 用哪个模型 /
// 能查哪些知识库 / 挂哪些 MCP / 用哪些技能 / 带哪份 CLI 凭据」翻译成执行上下文。
//
// 为什么要单独一层：节点里存的是用户的选择（agent_id、kb_scope、mcp 名称、技能名），
// 执行器要的是真实配置（角色对象、KB id、MCP 服务配置、凭据明文）。这层转换只发生在
// 执行前一刻，节点与模板保持「引用」语义，角色改了配置下一次运行自动生效。
//
// 依赖方向：本包只 import engine / executor / types，仓储以窄接口注入（真实实现都是
// repository 包的具体类型，结构化满足即可），不 import repository 也不 import service。
package sop

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/service/sop/engine"
	"github.com/Tencent/WeKnora/internal/application/service/sop/executor"
	"github.com/Tencent/WeKnora/internal/types"
)

// ---- 窄依赖接口：真实实现全是 repository 包的具体类型 ----

// RoleRepository 按 ID 取角色（实现：repository.CustomAgentRepository）。
type RoleRepository interface {
	GetAgentByID(ctx context.Context, id string, tenantID uint64) (*types.CustomAgent, error)
}

// ProjectGetter 按 ID 取项目（实现：repository.SopProjectRepo）。
type ProjectGetter interface {
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.SopProject, error)
}

// ItemGetter 按 ID 取事项（实现：repository.SopItemRepo）。
//
// 事项级 KB 覆盖要用它：事项绑定的知识库比项目级更具体，优先于项目 KB。
type ItemGetter interface {
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.SopItem, error)
}

// MCPServiceLister 列租户全部 MCP 服务（实现：repository.MCPServiceRepo）。
//
// 节点里存的是 MCP 服务名（面向用户的白话选择），这里按名翻回真实配置。
type MCPServiceLister interface {
	List(ctx context.Context, tenantID uint64) ([]*types.MCPService, error)
}

// KnowledgeBaseLister 列租户全部知识库（实现：repository.KnowledgeBaseRepository）。
//
// kb_scope=all 时用它取白名单；范围越小越省事，只在 all 才查。
type KnowledgeBaseLister interface {
	ListKnowledgeBasesByTenantID(ctx context.Context, tenantID uint64) ([]*types.KnowledgeBase, error)
}

// CredentialLister 列某用户的 CLI 凭据（实现：repository.SopCredentialRepo）。
//
// 凭据唯一口径是 (owner, cli_kind)，节点里存的是凭据 id；拿到列表后按 id 精确匹配，
// 这样既不需要新增 GetByID，也不会把别人的凭据串给本运行。
type CredentialLister interface {
	ListByOwner(ctx context.Context, tenantID uint64, ownerUserID string) ([]types.SopCLICredential, error)
}

// ResolverDeps 是解析器的外部依赖；未注入的依赖按「空解析」处理（不报错）。
type ResolverDeps struct {
	Agents      RoleRepository      // 角色
	Projects    ProjectGetter       // 项目（project 范围的 KB）
	Items       ItemGetter          // 事项（事项级 KB 覆盖）
	MCPs        MCPServiceLister    // MCP 服务名 → 真实配置
	KBs         KnowledgeBaseLister // 租户全部 KB（kb_scope=all）
	Credentials CredentialLister    // CLI 凭据
}

// ContextResolver 实现 engine.NodeResolver：纯查表 + 纯计算，不写任何状态。
type ContextResolver struct {
	deps ResolverDeps
}

// 编译期断言：解析器必须满足引擎的解析契约。
var _ engine.NodeResolver = (*ContextResolver)(nil)

// NewContextResolver 构造节点上下文解析器。
func NewContextResolver(d ResolverDeps) *ContextResolver { return &ContextResolver{deps: d} }

// Resolve 解析节点的执行上下文。
//
// 失败口径：只有「读不到就必须停」的情况才报错（角色读失败、凭据读失败、KB 列表读失败）；
// 读不到但可以退化的（项目不存在、事项不存在、MCP 名字对不上）按空处理——运行带病
// 继续比直接失败更符合「用户改完配置重跑一次」的实际操作习惯。
func (r *ContextResolver) Resolve(
	ctx context.Context, run *types.SopRun, node *types.SopNode,
) (engine.ResolvedNode, error) {
	out := engine.ResolvedNode{}
	if node == nil {
		return out, errors.New("节点为空：无法解析执行上下文")
	}

	role, err := r.role(ctx, run, node)
	if err != nil {
		return out, err
	}
	out.Role = role

	out.Model = r.model(node, role)
	out.KBIDs = r.knowledgeBaseIDs(ctx, run, node)
	out.Skills = executor.ResolveSkills(role, node.Skills)

	servers, err := r.mcpServers(ctx, run, node, role)
	if err != nil {
		return out, err
	}
	out.MCPServers = servers

	credential, err := r.credential(ctx, run, node)
	if err != nil {
		return out, err
	}
	out.Credential = credential
	return out, nil
}

// role 取节点角色：平台节点用 executor.agent_id；CLI 节点用可选的 executor.role_id
// （CLI 节点只要角色给任务书渲染与技能子集，不需要角色去跑）。
func (r *ContextResolver) role(
	ctx context.Context, run *types.SopRun, node *types.SopNode,
) (*types.CustomAgent, error) {
	id := strings.TrimSpace(node.Executor.AgentID)
	if node.Executor.Kind == types.SopExecutorCLI && node.Executor.RoleID != nil {
		id = strings.TrimSpace(*node.Executor.RoleID)
	}
	if id == "" || r.deps.Agents == nil {
		return nil, nil
	}
	tenantID := runTenant(run)
	role, err := r.deps.Agents.GetAgentByID(ctx, id, tenantID)
	if err != nil {
		return nil, fmt.Errorf("读取执行角色失败：%w", err)
	}
	return role, nil
}

// model 取节点模型：节点显式指定优先，其次是角色默认（空=AgentQA 自己兜底）。
func (r *ContextResolver) model(node *types.SopNode, role *types.CustomAgent) string {
	if model := strings.TrimSpace(node.Executor.Model); model != "" {
		return model
	}
	if role != nil {
		return strings.TrimSpace(role.Config.ModelID)
	}
	return ""
}

// knowledgeBaseIDs 按 kb_scope 解析检索范围：
//
//	project → 事项级 KB 覆盖优先，其次项目 KB，都没有就是空（不检索）；
//	custom  → 节点勾选的 KB；
//	all     → 租户全部 KB；
//	其他（none / 空以外未知值）→ 空。
func (r *ContextResolver) knowledgeBaseIDs(
	ctx context.Context, run *types.SopRun, node *types.SopNode,
) []string {
	switch strings.TrimSpace(node.Executor.KBScope) {
	case "", "project":
		return r.projectKnowledgeBases(ctx, run)
	case "custom":
		return dedupeIDs(node.Executor.KBIDs)
	case "all":
		return r.allKnowledgeBases(ctx, run)
	default:
		return nil
	}
}

// projectKnowledgeBases project 范围：事项级覆盖优先，其次项目 KB。
func (r *ContextResolver) projectKnowledgeBases(ctx context.Context, run *types.SopRun) []string {
	tenantID := runTenant(run)
	if run != nil && strings.TrimSpace(run.ItemID) != "" && r.deps.Items != nil {
		// 事项读不到不算错误：项目 KB 仍然是有效范围。
		if item, err := r.deps.Items.GetByID(ctx, tenantID, run.ItemID); err == nil && item != nil {
			if kb := strings.TrimSpace(item.KBID); kb != "" {
				return []string{kb}
			}
		}
	}
	if run != nil && strings.TrimSpace(run.ProjectID) != "" && r.deps.Projects != nil {
		if proj, err := r.deps.Projects.GetByID(ctx, tenantID, run.ProjectID); err == nil && proj != nil {
			if kb := strings.TrimSpace(proj.KBID); kb != "" {
				return []string{kb}
			}
		}
	}
	return nil
}

// allKnowledgeBases all 范围：租户全部 KB（列表读失败返回空而不是报错——
// 检索范围为空时 AgentQA 退化为「不检索」，比整条运行失败更容易理解）。
func (r *ContextResolver) allKnowledgeBases(ctx context.Context, run *types.SopRun) []string {
	if r.deps.KBs == nil {
		return nil
	}
	kbs, err := r.deps.KBs.ListKnowledgeBasesByTenantID(ctx, runTenant(run))
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(kbs))
	for _, kb := range kbs {
		if kb == nil {
			continue
		}
		if id := strings.TrimSpace(kb.ID); id != "" {
			ids = append(ids, id)
		}
	}
	return dedupeIDs(ids)
}

// mcpServers 解析节点 MCP：node.MCP 为 nil（未设置）→ 角色配置的全部 MCP；
// 显式空数组 → 一个都不要；非空 → 按名称（兼容按 id）翻成真实配置。
//
// 名字对不上的条目静默丢弃：MCP 服务被删/改名的情形下，硬失败会让用户只看到
// 一句「MCP 不存在」，而实际执行不需要它也能完成大多数步骤。
func (r *ContextResolver) mcpServers(
	ctx context.Context, run *types.SopRun, node *types.SopNode, role *types.CustomAgent,
) ([]types.MCPService, error) {
	if node.MCP == nil {
		return r.roleMCPServers(ctx, run, role)
	}
	names := dedupeIDs(node.MCP)
	if len(names) == 0 {
		return []types.MCPService{}, nil
	}
	if r.deps.MCPs == nil {
		return nil, nil
	}
	services, err := r.deps.MCPs.List(ctx, runTenant(run))
	if err != nil {
		return nil, fmt.Errorf("读取可用 MCP 服务失败：%w", err)
	}
	out := make([]types.MCPService, 0, len(names))
	for _, name := range names {
		if svc := findMCPService(services, name); svc != nil {
			out = append(out, *svc)
		}
	}
	return out, nil
}

// roleMCPServers 展开角色自己的 MCP 选择（nil=未设置，交给 AgentQA 的默认语义）。
func (r *ContextResolver) roleMCPServers(
	ctx context.Context, run *types.SopRun, role *types.CustomAgent,
) ([]types.MCPService, error) {
	if role == nil || len(role.Config.MCPServices) == 0 || r.deps.MCPs == nil {
		return nil, nil
	}
	services, err := r.deps.MCPs.List(ctx, runTenant(run))
	if err != nil {
		return nil, fmt.Errorf("读取可用 MCP 服务失败：%w", err)
	}
	out := make([]types.MCPService, 0, len(role.Config.MCPServices))
	for _, id := range dedupeIDs(role.Config.MCPServices) {
		if svc := findMCPServiceByID(services, id); svc != nil {
			out = append(out, *svc)
		}
	}
	return out, nil
}

// findMCPService 按名称找服务（忽略大小写与首尾空白），找不到再按 id 兜底。
func findMCPService(services []*types.MCPService, name string) *types.MCPService {
	for _, svc := range services {
		if svc == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(svc.Name), name) {
			return svc
		}
	}
	return findMCPServiceByID(services, name)
}

// findMCPServiceByID 按 id 找 MCP 服务。
func findMCPServiceByID(services []*types.MCPService, id string) *types.MCPService {
	for _, svc := range services {
		if svc != nil && svc.ID == id {
			return svc
		}
	}
	return nil
}

// credential 取 CLI 凭据：节点选了凭据就按 id 从运行创建者的凭据里精确匹配
// （凭据是「用户 × CLI」私有的，绝不跨用户取）。未选则不注入。
func (r *ContextResolver) credential(
	ctx context.Context, run *types.SopRun, node *types.SopNode,
) (*types.SopCLICredential, error) {
	id := strings.TrimSpace(node.Executor.CredentialID)
	if id == "" {
		return nil, nil
	}
	if r.deps.Credentials == nil {
		return nil, nil
	}
	tenantID := runTenant(run)
	owner := ""
	if run != nil {
		owner = strings.TrimSpace(run.CreatedBy)
	}
	creds, err := r.deps.Credentials.ListByOwner(ctx, tenantID, owner)
	if err != nil {
		return nil, fmt.Errorf("读取 CLI 凭据失败：%w", err)
	}
	for i := range creds {
		if creds[i].ID == id {
			found := creds[i]
			return &found, nil
		}
	}
	return nil, fmt.Errorf("节点选择的 CLI 凭据已不存在：请重新选择凭据")
}

// runTenant 取运行租户（运行一定带租户；为 nil 时退化为 0，查询自然查不到）。
func runTenant(run *types.SopRun) uint64 {
	if run == nil {
		return 0
	}
	return run.TenantID
}

// dedupeIDs 去空白、去重并保持顺序；nil 输入返回 nil（nil 与 [] 语义不同：
// nil=未设置，[]=显式一个都不要）。
func dedupeIDs(ids []string) []string {
	if ids == nil {
		return nil
	}
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
