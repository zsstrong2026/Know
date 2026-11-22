// Package executor 定义 SOP 执行器抽象：引擎只认 Provider，平台智能体 / 本地 CLI /
// 脚本各自实现同一条 Execute 契约，并通过统一流事件模型向 SSE 直播。
//
// 依赖方向固定为 engine → executor → types：本包除标准库与 internal/types 外不引入
// 任何仓库内依赖，也不反向感知 engine（引擎尚未存在）或 repository。
package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// ---- 常量：流事件类型（前端 SSE 直播按 type 分派渲染）----
const (
	StreamEventTextDelta  = "text_delta"  // 正文增量
	StreamEventToolCall   = "tool_call"   // 发起工具调用
	StreamEventToolResult = "tool_result" // 工具调用结果
	StreamEventResult     = "result"      // 节点最终结果
	StreamEventError      = "error"       // 执行错误
)

// ---- 常量：Provider 种类（Kind() 的返回值口径）----
const (
	// ProviderKindPlatformAgent 平台智能体执行器。
	ProviderKindPlatformAgent = types.SopExecutorPlatformAgent
	// ProviderKindScript 脚本节点执行器。
	ProviderKindScript = "script"
	// ProviderKindCLIPrefix CLI 执行器种类前缀：Kind() = "cli:" + node.Executor.CLIKind。
	ProviderKindCLIPrefix = "cli:"
)

// StreamEvent 统一流事件：执行器的实时输出，原样序列化后推送 SSE。
//
// 字段名即前端契约（Task 24/38），不得改名。
type StreamEvent struct {
	Type     string          `json:"type"` // text_delta | tool_call | tool_result | result | error
	Text     string          `json:"text,omitempty"`
	ToolName string          `json:"tool_name,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"`
	At       time.Time       `json:"at"`
}

// ArtifactFile 节点产物文件：Path 为工作区相对路径，SHA256 供下游判重与入 KB。
type ArtifactFile struct {
	Name      string `json:"name"`
	Path      string `json:"path"` // 工作区相对路径
	MediaType string `json:"media_type"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
}

// TaskBook 一次节点执行的全部输入：引擎组装好后交给执行器，执行器不再回查仓储。
type TaskBook struct {
	RunID           string
	NodeRunID       string
	NodeID          string
	NodeName        string
	Prompt          string // 引擎组装好的完整任务书（大白话+上下文块）
	SystemPrompt    string // agent-system.md 内容（CLI）/系统提示追加（平台）
	WorkspaceRoot   string
	InputsDir       string
	OutDir          string
	Feedback        string // 返工意见（非空=返工轮）
	Attempt         int
	ResumeSessionID string // CLI 续跑 / 平台续会话
	Timeout         time.Duration
	Env             map[string]string // 受控叠加环境变量
	Script          string            // script 节点脚本正文（其余类型为空）
	Model           string            // 空=默认
	Credential      *types.SopCLICredential
	Role            *types.CustomAgent // 解析后的角色（平台智能体执行器消费；可 nil）
	MCPServers      []types.MCPService // 节点 mcp 子集解析后的真实配置
	Skills          []string           // 解析后的技能子集（nil=全部）
	// KBIDs 是 Resolver 按节点知识库范围解析出的 KB id 列表（nil=不限制/未解析，
	// 显式 [] 表示范围内没有可用知识库）。平台智能体执行器据此收敛检索范围。
	KBIDs   []string
	KnowMCP *KnowMCPEndpoint // CLI 节点专用：Know MCP scoped endpoint（T37 供给；平台节点 nil）
}

// KnowMCPEndpoint 是 T37 MCPProvisioner 为 CLI 节点签发的受限检索端点。
// 类型定义在 executor 包（engine 与 cli 都要引用，cli 侧 T33 用类型别名）。
type KnowMCPEndpoint struct {
	URL     string   // 含 kb_scope query，如 http://127.0.0.1:8080/mcp?kb_scope=project
	Token   string   // Bearer token（HMAC 派生，不落盘）
	KBScope string   // project | custom | all
	KBIDs   []string // 服务端强制的 KB 白名单（all=租户全部）
}

// ExecOptions 单次执行的运行时选项。
type ExecOptions struct {
	Emit      func(StreamEvent) // SSE 直播 sink（nil=丢弃）
	SessionID string            // 平台智能体 per-node 会话（复用/新建由执行器决定）
}

// ExecResult 执行结果：摘要正文 + 门禁自评 + 产物 + 用量 + 会话续跑信息。
type ExecResult struct {
	Output          string         // 进下游 {{upstream}} 的摘要正文
	GateResult      string         // pass|rework|fail|human；空=缺失（交门禁裁定）
	GateReason      string         // 门禁自评理由
	Artifacts       []ArtifactFile // 本轮产出文件
	Usage           types.SopUsage // 本轮模型用量
	SessionID       string         // 本轮会话 id
	ResumeSessionID string         // 下次续跑用的会话 id
	Suspended       bool           // true=运行挂起等待用户输入（HITL），引擎置节点 pending_input
}

// Provider 是节点执行器的统一契约：同一份任务书，三路执行器各自落地。
type Provider interface {
	Kind() string // platform_agent | cli:<kind> | script
	Prepare(ctx context.Context, node *types.SopNode, wsRoot string) error
	Execute(ctx context.Context, tb TaskBook, opts ExecOptions) (ExecResult, error)
	Cancel(ctx context.Context) error
}

// Registry 按 Kind() 索引的执行器注册表；Register / Resolve 并发安全。
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

// NewRegistry 建空注册表；启动期由装配代码逐类注册。
func NewRegistry() *Registry {
	return &Registry{providers: map[string]Provider{}}
}

// Register 以 Provider.Kind() 为键登记；同键后注册者覆盖先注册者。
func (r *Registry) Register(p Provider) {
	if p == nil || p.Kind() == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[p.Kind()] = p
}

// Resolve 按节点推导执行器 key 并取回 Provider；未注册的 key 报白话错误。
//
// 映射：agent → platform_agent；script → script；
// executor.kind=cli → "cli:"+node.Executor.CLIKind。
// sop 子流程与 approval 审批节点由引擎内部处理，不进注册表。
func (r *Registry) Resolve(node *types.SopNode) (Provider, error) {
	key, err := resolveKind(node)
	if err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[key]
	if !ok {
		return nil, fmt.Errorf("执行器 %s 未就绪：请先在平台完成该执行器配置", key)
	}
	return p, nil
}

// resolveKind 把节点推导成注册表 key。
func resolveKind(node *types.SopNode) (string, error) {
	if node == nil {
		return "", fmt.Errorf("节点为空：无法解析执行器")
	}
	switch node.Type {
	case types.SopNodeTypeScript:
		return ProviderKindScript, nil
	case types.SopNodeTypeSOP:
		return "", fmt.Errorf("节点 %s 是子流程节点：由引擎内部编排，不走执行器注册表", node.ID)
	case types.SopNodeTypeApproval:
		return "", fmt.Errorf("节点 %s 是审批节点：由引擎等待人工审批，不走执行器注册表", node.ID)
	case types.SopNodeTypeAgent:
		// agent 继续按 executor.kind 细分。
	default:
		return "", fmt.Errorf("节点 %s 类型 %q 无法识别", node.ID, node.Type)
	}

	switch node.Executor.Kind {
	case "", types.SopExecutorPlatformAgent:
		return ProviderKindPlatformAgent, nil
	case types.SopExecutorCLI:
		cliKind := strings.TrimSpace(node.Executor.CLIKind)
		if cliKind == "" {
			return "", fmt.Errorf("节点 %s 选了本地 CLI 执行器但未指定 cli_kind", node.ID)
		}
		return ProviderKindCLIPrefix + cliKind, nil
	default:
		return "", fmt.Errorf("节点 %s 的执行器类型 %q 无法识别", node.ID, node.Executor.Kind)
	}
}
