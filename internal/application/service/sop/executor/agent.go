// agent.go 是平台智能体执行器：把一次节点执行翻译成一次 AgentQA 调用，
// 复用 Know 现有智能体运行时（工具/技能/知识库/MCP/HITL），不复制一份 agent 引擎。
//
// 设计口径：
//   - 会话 per-node 复用：同一节点的多次尝试落在同一个会话里（返工、挂起续跑都靠它），
//     会话描述带 types.SOPSessionMarker 前缀，首页对话列表据此隐藏；
//   - 结果不自评：门禁结论（pass/rework/fail/human）由 submit_node_result 双通道提交
//     （T17），本执行器只返回正文与用量；
//   - 挂起：AgentQA 结束时带 PromptSuspension 即返回 Suspended，引擎把节点置
//     pending_input 等人工答复后续跑。
//
// 依赖方向：executor 只认 types 与 event（QARequest 的事件总线签名需要 event），
// 不 import 任何 repository / service 包；会话与消息能力以窄接口注入，真实实现就是
// interfaces.SessionService / interfaces.MessageService。
package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
)

// submitToolName 是平台节点提交门禁结论的工具名（实现见 T17）。
//
// 本执行器只负责把它追加进本轮可用工具：AgentProvider 声明的执行通道必须与任务书
// 「结果提交」小节一致，否则模型会照着任务书去调一个不存在的工具。
const submitToolName = "submit_node_result"

// kbScopeNone 是不使用知识库的范围值（前端「不使用知识库」）。
const kbScopeNone = "none"

// AgentQACaller 是 AgentQA 的窄接口：真实实现 = interfaces.SessionService，
// 测试可以只给一个记录入参的桩。
type AgentQACaller interface {
	AgentQA(ctx context.Context, req *types.QARequest, bus *event.EventBus) error
}

// SessionCreator 是会话读写的窄接口（真实实现 = interfaces.SessionService）。
type SessionCreator interface {
	CreateSession(ctx context.Context, session *types.Session) (*types.Session, error)
	GetSessionByID(ctx context.Context, tenantID uint64, id string) (*types.Session, error)
}

// MessageWriter 是消息读写的窄接口（真实实现 = interfaces.MessageService）。
//
// 预建 user/assistant 两行是 AgentQA 的前提：它按 messageID 把流式结果写回
// 已存在的 assistant 行，不自己建行（与 automation_runner 的用法一致）。
type MessageWriter interface {
	CreateMessage(ctx context.Context, message *types.Message) (*types.Message, error)
	UpdateMessage(ctx context.Context, message *types.Message) error
}

// RoleGetter 按 ID 取角色（真实实现 = interfaces.CustomAgentRepository）。
//
// Prepare 阶段要知道角色有没有配重排模型；执行阶段角色由 Resolver 直接放进任务书，
// 这里只服务「未注入 Resolver 的直连调用」（如测试与将来的单节点重跑）。
type RoleGetter interface {
	GetAgentByID(ctx context.Context, id string, tenantID uint64) (*types.CustomAgent, error)
}

// AgentSuspension 一次挂起的最小信息：会话 + 运行快照 + 被答复的提示。
//
// types 里的挂起行按 assistant message 主键存（AgentRunSuspension），而节点只知道
// 会话 id；这里用「会话 → 最近一次挂起」的形状把两者隔开，适配层放在 T17 接线处。
type AgentSuspension struct {
	SessionID string
	Snapshot  []byte
	Prompt    *types.AgentPrompt
}

// SuspensionReader 按会话读挂起（真实实现 = hitl.Suspensions + hitl.Store 的适配，
// T17 注入；nil = 本进程不做挂起续跑）。
type SuspensionReader interface {
	GetBySession(ctx context.Context, sessionID string) (*AgentSuspension, error)
}

// AgentDeps 平台智能体执行器的外部依赖。
type AgentDeps struct {
	Sessions    SessionCreator   // 建会话 / 复用会话
	Messages    MessageWriter    // 预建 user/assistant 消息行
	QA          AgentQACaller    // AgentQA（真实实现 = interfaces.SessionService）
	Suspensions SuspensionReader // 挂起快照（可 nil；T17 消费）
	Agents      RoleGetter       // 角色（可 nil：Prepare 的重排校验退化为「无角色即不合格」）
	// Results 是 submit_node_result 的进程内结果槽（nil 时用进程内默认槽；
	// 工具与执行器必须共享同一份，默认槽就是为此存在）。
	Results *ResultSlots
	// Submits 把「会话 → SOP 提交上下文」登记给工具（nil 时用进程内默认登记表）。
	Submits *SubmitBindings
}

// AgentProvider 平台智能体执行器（Kind = platform_agent）。
type AgentProvider struct {
	deps AgentDeps
	// submitEnabled 表示装配时显式给了提交通道（结果槽/登记表）。
	submitEnabled bool

	// mu/cancel 让 Cancel 能中断在途的 AgentQA：AgentQA 尊重 ctx，取消它即可。
	mu     sync.Mutex
	cancel context.CancelFunc
}

// NewAgentProvider 构造平台智能体执行器。
func NewAgentProvider(d AgentDeps) *AgentProvider {
	// 显式装配了结果通道才启用「结果优先 + 缺失追问」：测试桩/单节点预演没有
	// 提交通道，追问只会白烧一轮 token。
	enabled := d.Results != nil || d.Submits != nil
	if d.Results == nil {
		d.Results = defaultResultSlots
	}
	if d.Submits == nil {
		d.Submits = defaultSubmitBindings
	}
	return &AgentProvider{deps: d, submitEnabled: enabled}
}

// Kind 返回注册表键：platform_agent。
func (p *AgentProvider) Kind() string { return ProviderKindPlatformAgent }

// Prepare 校验执行前提：节点要用知识库时，角色必须能拿出重排模型。
//
// 判定字段与 AgentQA 一致（Config.RerankModelID；内建角色走工作区回退），
// 在这里拦下的原因是解释成本：跑起来才发现，用户只会看到一次莫名其妙的失败。
func (p *AgentProvider) Prepare(ctx context.Context, node *types.SopNode, _ string) error {
	if node == nil {
		return errors.New("节点为空：无法准备执行环境")
	}
	if !usesKnowledge(node.Executor.KBScope) {
		return nil
	}
	role, err := p.roleFor(ctx, node)
	if err != nil {
		return err
	}
	if role == nil {
		return errors.New("该角色未配置重排模型，无法使用知识库检索；请在角色设置中配置或把知识库范围改为不使用")
	}
	if strings.TrimSpace(role.Config.RerankModelID) == "" && !types.UsesWorkspaceModelFallback(role) {
		return errors.New("该角色未配置重排模型，无法使用知识库检索；请在角色设置中配置或把知识库范围改为不使用")
	}
	return nil
}

// roleFor 取节点角色：Prepare 时节点可能还没走 Resolver，按 executor.agent_id 回查。
func (p *AgentProvider) roleFor(ctx context.Context, node *types.SopNode) (*types.CustomAgent, error) {
	id := strings.TrimSpace(node.Executor.AgentID)
	if id == "" || p.deps.Agents == nil {
		return nil, nil
	}
	tenantID, _ := types.TenantIDFromContext(ctx)
	role, err := p.deps.Agents.GetAgentByID(ctx, id, tenantID)
	if err != nil {
		return nil, fmt.Errorf("读取执行角色失败：%w", err)
	}
	return role, nil
}

// usesKnowledge 判断节点是否真的会用到知识库（空=默认项目范围）。
func usesKnowledge(scope string) bool {
	return strings.TrimSpace(scope) != kbScopeNone
}

// Execute 跑一次节点：复用/新建会话 → 预建消息 → 组装 QARequest → 同步阻塞到 AgentQA 返回。
func (p *AgentProvider) Execute(ctx context.Context, tb TaskBook, opts ExecOptions) (ExecResult, error) {
	if p.deps.QA == nil {
		return ExecResult{}, errors.New("平台智能体执行器未就绪：AgentQA 未注入")
	}
	if tb.Role == nil {
		return ExecResult{}, errors.New("该步骤没有解析到执行角色：请先在节点上选择角色")
	}

	runCtx, cancel := context.WithCancel(ctx)
	p.setCancel(cancel)
	defer func() {
		p.clearCancel(cancel)
		cancel()
	}()

	session, err := p.session(runCtx, tb, opts)
	if err != nil {
		return ExecResult{}, err
	}
	res := ExecResult{SessionID: session.ID, ResumeSessionID: session.ID}

	userMsg, answerMsg, err := p.createMessages(runCtx, tb, session)
	if err != nil {
		return res, err
	}

	submitKey, unbind := p.bindSubmit(tb, session)
	if unbind != nil {
		defer unbind()
	}
	// 工具注册处（agent_service）按 ctx 里的登记表判断本会话是不是 SOP 会话，
	// 命中才挂出 submit_node_result。
	runCtx = WithSubmitBindings(runCtx, p.deps.Submits)

	req, err := p.buildRequest(runCtx, tb, session, userMsg, answerMsg)
	if err != nil {
		return res, err
	}

	collected := p.run(runCtx, req, opts)
	res.Output = collected.output
	res.Usage = collected.usage
	res.Suspended = collected.suspended
	// 结果以 submit_node_result 为准；缺失时同会话追问一次（只问一次，仍缺失就
	// 留空交门禁三段裁定，绝不臆测模型意图）。结果槽未装配（单节点预演/测试桩）
	// 时不追问：没有通道能收回结论，追问只会白烧一轮 token。
	if !collected.suspended && collected.err == nil && p.submitEnabled {
		submitted, ok := p.takeSubmit(submitKey)
		if !ok {
			if again, askErr := p.askAgain(runCtx, req, opts); askErr == nil {
				collected.usage.InputTokens += again.usage.InputTokens
				collected.usage.OutputTokens += again.usage.OutputTokens
				res.Usage = collected.usage
				submitted, ok = p.takeSubmit(submitKey)
			}
		}
		if ok {
			res.GateResult, res.GateReason = submitted.GateResult, submitted.GateReason
			if strings.TrimSpace(submitted.Output) != "" {
				res.Output = submitted.Output
			}
		}
	}
	if answerMsg != nil && p.deps.Messages != nil {
		answerMsg.Content = collected.output
		answerMsg.Usage = collected.tokenUsage
		answerMsg.IsCompleted = !collected.suspended && collected.err == nil
		if collected.suspended {
			answerMsg.RunState = types.MessageRunStateAwaitingInput
		} else {
			answerMsg.RunState = ""
		}
		answerMsg.UpdatedAt = time.Now()
		if saveErr := p.deps.Messages.UpdateMessage(context.WithoutCancel(runCtx), answerMsg); saveErr != nil && collected.err == nil {
			collected.err = fmt.Errorf("保存本轮回复失败：%w", saveErr)
		}
	}
	if collected.err != nil {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		return res, collected.err
	}
	return res, nil
}

// submitKeyFor 是结果槽键：同一运行同一节点跨尝试共用一个键（每次执行前被取空）。
func submitKeyFor(tb TaskBook) string { return tb.RunID + "/" + tb.NodeID }

// defaultResultSlots 是进程内默认结果槽（与默认登记表配套：工具注册处不依赖
// 执行器实例，只能靠进程内单例对齐）。
var defaultResultSlots = NewResultSlots()

// DefaultResultSlots 返回进程内默认结果槽（工具注册处与测试可显式复用）。
func DefaultResultSlots() *ResultSlots { return defaultResultSlots }

// bindSubmit 登记本会话的提交上下文并返回结果槽键；nil 解绑函数表示未登记。
func (p *AgentProvider) bindSubmit(tb TaskBook, session *types.Session) (string, func()) {
	slots := p.deps.Results
	if slots == nil && p.deps.Submits == nil {
		return "", nil
	}
	key := submitKeyFor(tb)
	sc := &SubmitContext{
		SessionID:          session.ID,
		SessionDescription: session.Description,
		Slots:              slots,
		SlotKey:            key,
		WorkspaceRoot:      tb.WorkspaceRoot,
		NodeID:             tb.NodeID,
		Attempt:            tb.Attempt,
	}
	if p.deps.Submits != nil {
		p.deps.Submits.Bind(session.ID, sc)
		return key, func() { p.deps.Submits.Unbind(session.ID, sc) }
	}
	return key, func() {}
}

// takeSubmit 取走结果槽里的提交（无槽时视为未提交）。
func (p *AgentProvider) takeSubmit(key string) (SubmitResult, bool) {
	if p.deps.Results == nil || key == "" {
		return SubmitResult{}, false
	}
	return p.deps.Results.Take(key)
}

// askAgain 在同会话里追问一次「请提交本步骤结果」，返回本轮收集结果。
//
// 追问复用原请求的会话与角色配置，只换 Query、去掉 Resume、不再可挂起：
// 本轮已正常结束，续问就是新的一轮，不能因为模型又去提问而把节点挂起第二次。
func (p *AgentProvider) askAgain(
	ctx context.Context, first *types.QARequest, opts ExecOptions,
) (agentRun, error) {
	if p.deps.QA == nil || first == nil || first.Session == nil {
		return agentRun{}, errors.New("无法追问：执行器未就绪")
	}
	req := *first
	req.Query = submitReminder
	// 同一个 assistant 行续写：追问的产出仍归本轮，事件流也不会另起一条消息。
	req.Resume = nil
	req.Suspendable = false
	req.UserMessageID = ""
	req.RunNote = strings.TrimSpace(first.RunNote) + "\n本轮是结果提交追问。"
	again := p.run(ctx, &req, opts)
	if again.suspended {
		again.suspended = false
	}
	return again, again.err
}

// submitReminder 是缺失结果时的追问指令（一次，语气明确但不动摇已完成的工作）。
const submitReminder = "你还没有提交本步骤结果。请立即调用 submit_node_result 汇报结果（pass/rework/fail/human），" +
	"rework/fail/human 必须写 reason；如果已完成就提交 pass，不要重复做一遍工作。"

// Cancel 中断在途执行：AgentQA 尊重 ctx，取消它即可（无在途执行时是 no-op）。
func (p *AgentProvider) Cancel(_ context.Context) error {
	p.mu.Lock()
	cancel := p.cancel
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// setCancel 记录本轮取消函数；clearCancel 只在仍是本轮那一份时清空。
func (p *AgentProvider) setCancel(cancel context.CancelFunc) {
	p.mu.Lock()
	p.cancel = cancel
	p.mu.Unlock()
}

func (p *AgentProvider) clearCancel(cancel context.CancelFunc) {
	p.mu.Lock()
	if p.cancel != nil {
		p.cancel = nil
	}
	p.mu.Unlock()
}

// session 取本次执行用的会话：有 SessionID 就复用（返工/续跑同一节点），否则新建。
func (p *AgentProvider) session(ctx context.Context, tb TaskBook, opts ExecOptions) (*types.Session, error) {
	if p.deps.Sessions == nil {
		return nil, errors.New("平台智能体执行器未就绪：会话服务未注入")
	}
	if id := strings.TrimSpace(opts.SessionID); id != "" {
		session, err := p.deps.Sessions.GetSessionByID(ctx, runTenantID(ctx, tb), id)
		if err != nil || session == nil {
			return nil, fmt.Errorf("复用节点会话失败：%w", err)
		}
		return session, nil
	}
	session := &types.Session{
		Title:       clipTitle(tb.NodeName + " - SOP"),
		Description: types.SOPSessionMarker + "run=" + tb.RunID + " node=" + tb.NodeID,
		TenantID:    runTenantID(ctx, tb),
	}
	created, err := p.deps.Sessions.CreateSession(ctx, session)
	if err != nil {
		return nil, fmt.Errorf("创建节点会话失败：%w", err)
	}
	if created == nil {
		return nil, errors.New("创建节点会话失败：会话服务没有返回会话")
	}
	return created, nil
}

// runTenantID 取执行租户：ctx 优先，其次角色自带（直连调用时 ctx 可能没有租户）。
func runTenantID(ctx context.Context, tb TaskBook) uint64 {
	if id, ok := types.TenantIDFromContext(ctx); ok && id != 0 {
		return id
	}
	if tb.Role != nil {
		return tb.Role.TenantID
	}
	return 0
}

// clipTitle 限制会话标题长度（sessions.title 列口径 255，这里留足余量）。
func clipTitle(title string) string {
	title = strings.TrimSpace(title)
	rs := []rune(title)
	if len(rs) <= 200 {
		return title
	}
	return string(rs[:200])
}

// createMessages 预建 user/assistant 两行：AgentQA 把流式结果写回已存在的 assistant 行。
func (p *AgentProvider) createMessages(
	ctx context.Context, tb TaskBook, session *types.Session,
) (*types.Message, *types.Message, error) {
	if p.deps.Messages == nil {
		return nil, nil, errors.New("平台智能体执行器未就绪：消息服务未注入")
	}
	now := time.Now()
	userMsg, err := p.deps.Messages.CreateMessage(ctx, &types.Message{
		SessionID: session.ID, RequestID: tb.NodeRunID, Role: "user", Content: tb.Prompt,
		IsCompleted: true, CreatedAt: now, Channel: types.ChannelWeb,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("记录节点输入失败：%w", err)
	}
	answerMsg, err := p.deps.Messages.CreateMessage(ctx, &types.Message{
		SessionID: session.ID, RequestID: tb.NodeRunID, Role: "assistant",
		CreatedAt: now, Channel: types.ChannelWeb,
	})
	if err != nil {
		return userMsg, nil, fmt.Errorf("记录节点输出失败：%w", err)
	}
	return userMsg, answerMsg, nil
}

// buildRequest 组装一次 AgentQA 请求：角色配置按节点覆盖，不改动共享的角色对象。
func (p *AgentProvider) buildRequest(
	ctx context.Context, tb TaskBook, session *types.Session, userMsg, answerMsg *types.Message,
) (*types.QARequest, error) {
	req := &types.QARequest{
		Session:            session,
		Query:              tb.Prompt,
		AssistantMessageID: answerMsg.ID,
		UserMessageID:      userMsg.ID,
		CustomAgent:        agentForNode(tb),
		SummaryModelID:     strings.TrimSpace(tb.Model),
		KnowledgeBaseIDs:   append([]string(nil), tb.KBIDs...),
		Mode:               types.RunModeDeferred, // 无人值守可挂起：答复后续跑
		Suspendable:        true,
		RunNote: fmt.Sprintf(
			"SOP 运行 %s 节点 %s 第 %d 次尝试，无人值守；需要用户输入时挂起等待，不要臆测。",
			tb.RunID, tb.NodeID, tb.Attempt,
		),
	}
	resume, err := p.resumeFor(ctx, tb)
	if err != nil {
		return nil, err
	}
	req.Resume = resume
	return req, nil
}

// resumeFor 取续跑快照：只有带挂起的会话才走 Resume（否则就是新一轮执行）。
func (p *AgentProvider) resumeFor(ctx context.Context, tb TaskBook) (*types.AgentResume, error) {
	sessionID := strings.TrimSpace(tb.ResumeSessionID)
	if sessionID == "" || p.deps.Suspensions == nil {
		return nil, nil
	}
	suspension, err := p.deps.Suspensions.GetBySession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("读取挂起状态失败：%w", err)
	}
	if suspension == nil || len(suspension.Snapshot) == 0 || suspension.Prompt == nil {
		return nil, nil
	}
	return &types.AgentResume{Snapshot: suspension.Snapshot, Prompt: suspension.Prompt}, nil
}

// agentForNode 深拷贝角色后按节点覆盖：模型、技能子集、MCP 子集、知识库子集与工具。
//
// 共享的角色对象绝不能就地改：同一角色可能被并行的兄弟节点同时使用。
func agentForNode(tb TaskBook) *types.CustomAgent {
	clone := *tb.Role
	cfg := tb.Role.Config
	if model := strings.TrimSpace(tb.Model); model != "" {
		cfg.ModelID = model
	}
	// tb.Skills 已由 Resolver 收敛成节点实际可用的子集；nil 表示「Resolver 未介入」，
	// 此时保留角色自己的选择方式，避免把技能静默关掉。
	if tb.Skills != nil {
		cfg.SkillsSelectionMode = "selected"
		cfg.SelectedSkills = append([]string{}, tb.Skills...)
	}
	if tb.MCPServers != nil {
		ids := mcpServiceIDs(tb.MCPServers)
		if len(ids) == 0 {
			cfg.MCPSelectionMode = kbScopeNone
			cfg.MCPServices = nil
		} else {
			cfg.MCPSelectionMode = "selected"
			cfg.MCPServices = ids
		}
	}
	if tb.KBIDs != nil {
		cfg.KBSelectionMode = "selected"
		cfg.KnowledgeBases = append([]string{}, tb.KBIDs...)
	}
	// 迭代上限按节点收敛：SOP 步骤是无人值守的有限任务，跑满角色默认上限（可能
	// 是无限）会把「卡住的步骤」变成「永远在跑的运行」。节点未设置时保持角色口径。
	if tb.LoopMaxIterations > 0 {
		cfg.MaxIterations = tb.LoopMaxIterations
	}
	cfg.AllowedTools = withSubmitTool(cfg.AllowedTools)
	clone.Config = cfg
	return &clone
}

// mcpServiceIDs 把解析后的 MCP 服务配置摊平成 ID 列表。
func mcpServiceIDs(servers []types.MCPService) []string {
	if servers == nil {
		return nil
	}
	ids := make([]string, 0, len(servers))
	for _, server := range servers {
		if id := strings.TrimSpace(server.ID); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// withSubmitTool 把 submit_node_result 追加进显式声明的可用工具。
//
// AllowedTools 为 nil 表示「从未设置」（平台默认工具集），此时不擅自展开也不追加：
// 展开默认集要把 internal/agent/tools 整棵依赖拖进本包，而 T17 会把
// submit_node_result 注册进工具表、让它对默认集同样可用。explicit 列表（含 []）是
// 用户把工具挑明白了，此时必须补上提交通道，否则任务书让它调的工具根本没注册。
func withSubmitTool(allowed []string) []string {
	if allowed == nil {
		return nil
	}
	for _, name := range allowed {
		if name == submitToolName {
			return allowed
		}
	}
	out := make([]string, 0, len(allowed)+1)
	out = append(out, allowed...)
	return append(out, submitToolName)
}

// agentRun 是一次 AgentQA 调用的收集结果（事件流里拼出来的）。
type agentRun struct {
	output     string
	usage      types.SopUsage
	tokenUsage *types.TokenUsage
	suspended  bool
	err        error
}

// run 订阅事件总线 → 映射成统一流事件 → 同步阻塞等 AgentQA 返回。
//
// 事件映射（前端 SSE 契约）：最终答案正文 → text_delta；工具调用/结果 →
// tool_call/tool_result；结束 → result；错误 → error。
func (p *AgentProvider) run(ctx context.Context, req *types.QARequest, opts ExecOptions) agentRun {
	bus := event.NewEventBus()
	var (
		mu      sync.Mutex
		answer  strings.Builder
		run     agentRun
		hadErr  bool
		emitErr error
	)
	emit := func(ev StreamEvent) {
		if opts.Emit == nil {
			return
		}
		if ev.At.IsZero() {
			ev.At = time.Now()
		}
		opts.Emit(ev)
	}
	bus.On(event.EventAgentFinalAnswer, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentFinalAnswerData)
		if !ok || data.Content == "" {
			return nil
		}
		mu.Lock()
		answer.WriteString(data.Content)
		mu.Unlock()
		emit(StreamEvent{Type: StreamEventTextDelta, Text: data.Content})
		return nil
	})
	bus.On(event.EventAgentToolCall, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentToolCallData)
		if !ok {
			return nil
		}
		var payload json.RawMessage
		if len(data.Arguments) > 0 {
			if raw, err := json.Marshal(data.Arguments); err == nil {
				payload = raw
			}
		}
		emit(StreamEvent{Type: StreamEventToolCall, ToolName: data.ToolName, Payload: payload})
		return nil
	})
	bus.On(event.EventAgentToolResult, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentToolResultData)
		if !ok {
			return nil
		}
		text := data.Output
		if text == "" && data.Error != "" {
			text = data.Error
		}
		emit(StreamEvent{Type: StreamEventToolResult, ToolName: data.ToolName, Text: text})
		return nil
	})
	bus.On(event.EventAgentComplete, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentCompleteData)
		if !ok {
			return nil
		}
		mu.Lock()
		defer mu.Unlock()
		if usage, ok := data.Usage.(*types.TokenUsage); ok && usage != nil {
			run.tokenUsage = usage
			run.usage = types.SopUsage{
				InputTokens:  int64(usage.PromptTokens),
				OutputTokens: int64(usage.CompletionTokens),
			}
		}
		if suspension, ok := data.Suspended.(*types.PromptSuspension); ok && suspension != nil {
			run.suspended = true
		}
		if answer.Len() == 0 && data.FinalAnswer != "" {
			answer.WriteString(data.FinalAnswer)
		}
		final := answer.String()
		emit(StreamEvent{Type: StreamEventResult, Text: final})
		return nil
	})
	bus.On(event.EventError, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.ErrorData)
		if !ok {
			return nil
		}
		mu.Lock()
		if !hadErr {
			hadErr = true
			emitErr = errors.New(data.Error)
		}
		mu.Unlock()
		emit(StreamEvent{Type: StreamEventError, Text: data.Error})
		return nil
	})

	qaErr := p.deps.QA.AgentQA(ctx, req, bus)

	mu.Lock()
	defer mu.Unlock()
	run.output = strings.TrimSpace(answer.String())
	if qaErr == nil && hadErr {
		qaErr = emitErr
	}
	run.err = qaErr
	return run
}

// ---- 技能子集 ----

// ResolveSkills 计算节点实际可用的技能子集（引擎与解析器共用同一份口径）：
//   - nodeSkills 为 nil（JSON null / 用户全选）→ 角色的 SelectedSkills 全部；
//   - nodeSkills 非 nil（含空切片）→ 与角色技能的交集，显式 [] 表示一个都不要；
//   - 角色为空 → 原样使用 nodeSkills（没有角色就没有可收敛的白名单）。
//
// 之所以放在 executor 而不是 engine：engine → executor 的依赖方向允许，
// 反过来会成环（engine 的 Resolver 实现由 service/sop 提供）。
func ResolveSkills(role *types.CustomAgent, nodeSkills []string) []string {
	if role == nil {
		if nodeSkills == nil {
			return nil
		}
		return dedupeSkillNames(nodeSkills)
	}
	owned := role.Config.SelectedSkills
	if nodeSkills == nil {
		return dedupeSkillNames(owned)
	}
	var out []string
	seen := make(map[string]bool, len(nodeSkills))
	for _, name := range owned {
		if !containsSkillName(nodeSkills, name) || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	if out == nil {
		out = []string{}
	}
	return out
}

// containsSkillName 判断切片是否含有某个技能名。
func containsSkillName(list []string, name string) bool {
	for _, s := range list {
		if s == name {
			return true
		}
	}
	return false
}

// dedupeSkillNames 去重并保持首次出现的顺序；nil 输入返回 nil。
func dedupeSkillNames(list []string) []string {
	if list == nil {
		return nil
	}
	seen := make(map[string]bool, len(list))
	out := make([]string, 0, len(list))
	for _, s := range list {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
