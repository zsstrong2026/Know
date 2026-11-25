// engine.go 是 SOP 工作台引擎的生命周期与装配：Deps 汇总引擎的全部外部依赖，
// Engine 持有进程内单实例调度状态，并负责「认领 queued 运行 → 建工作区与节点记录 →
// 终态收口（事项联动/通知/审计）」这三件确定性的事。
//
// 边界约定（不可越界）：
//   - 本包不 import internal/application/repository：仓储以 interfaces 包的窄接口注入
//     （接口定义在 types/interfaces，实现留在仓储层），依赖方向固定为
//     engine → executor → types；
//   - 事项状态的唯一写入口是 ItemStatusUpdater（item service 实现，T22 接线）：
//     引擎永不直接写 sop_items.status，保证 8 态状态机只在一处裁决；
//   - 节点推进（Advance/executeNode）属于 T13 的 node.go，本文件只提供调度骨架。
package engine

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service/sop/executor"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ItemStatusUpdater 窄接口：事项状态唯一写入口（item service 实现，T22 接线；nil=跳过联动）。
//
// 引擎只声明它需要的那一个方法，不引入整个事项服务：这样 script/测试可以只给一个
// 记录调用的桩，也不会因为事项服务加方法而让引擎重新编译失败。
type ItemStatusUpdater interface {
	UpdateStatus(ctx context.Context, tenantID uint64, id, to string) error
}

// ResolvedNode 节点执行上下文解析结果（真实实现=T16 service/sop/context.go；
// nil Resolver=空解析，供脚本/测试）。
//
// Skills 已按节点声明过滤：Resolver 负责「角色全部技能 ∩ 节点选择的子集」，
// 引擎不再二次裁剪，避免两处口径漂移。
type ResolvedNode struct {
	Role       *types.CustomAgent
	Skills     []string // 已按节点子集过滤
	MCPServers []types.MCPService
	Model      string
	Credential *types.SopCLICredential
	KBIDs      []string
}

// NodeResolver 把一次运行的节点解析成执行上下文（角色/技能/MCP/模型/凭据/知识库）。
type NodeResolver interface {
	Resolve(ctx context.Context, run *types.SopRun, node *types.SopNode) (ResolvedNode, error)
}

// Deps 是引擎的全部外部依赖；零值安全（nil 的 Resolver/ItemStatus/Notify 一律跳过）。
type Deps struct {
	Runs              interfaces.SopRunRepository
	Templates         interfaces.SopTemplateRepository
	Projects          interfaces.SopProjectRepository
	Items             interfaces.SopItemRepository
	Extras            interfaces.SopItemExtraRepository
	Registry          *executor.Registry
	Resolver          NodeResolver                                                                // 可 nil
	ItemStatus        ItemStatusUpdater                                                           // 可 nil
	WorkspaceRoot     string                                                                      // 如 data/sop-workspaces
	MaxParallel       int                                                                         // 默认 2
	DisableRunLoop    bool                                                                        // true=startQueued 不启动 runLoop，测试同步驱动 Advance（E2E 用 false）
	Notify            func(ctx context.Context, tenantID uint64, kind, title, body, refID string) // 可 nil；kind=sop_approval|sop_run_finished|sop_run_failed
	Prompts           PromptAnswerer                                                              // 可 nil；答复写入挂起提示（hitl Broker 适配，T21 接线）
	SuspensionPrompts PromptSuspensionLocator                                                     // 可 nil；按会话定位待答复的挂起提示
	// EventSink 把节点流事件与状态变更推给 SSE 订阅方（可 nil；T24 接线）。
	EventSink func(runID string, ev executor.StreamEvent)
	// AutoIngest 门禁通过后自动归档产物到绑定 KB（可 nil；T36 接线）。
	// 错误只记日志，不影响运行结论——沉淀不是门禁。
	AutoIngest func(ctx context.Context, run *types.SopRun, node *types.SopNode, arts []*types.SopArtifact)
	// MCP 为 CLI 节点签发受限 Know MCP 端点（可 nil；T37 接线）。
	MCP MCPEndpointProvisioner
	// ReleaseMCP 在运行收口后回收该运行签发的 MCP 端点（可 nil）。与 OnRunFinal 分开：
	// 取消路径不走 OnRunFinal（黑板不为取消写摘要），但端点同样要回收。
	ReleaseMCP func(ctx context.Context, tenantID uint64, runID string) error
	// OnRunFinal 在运行收口（终态 + FinishedAt 已落库）后调用一次（可 nil；T61 接线）。
	// 用于项目黑板自动摘要：失败只记日志，绝不影响运行结论。
	OnRunFinal func(ctx context.Context, run *types.SopRun)
	// CreateItems 在节点声明 produces_child_items 且门禁通过后建子事项（可 nil；T63 接线）。
	// 返回错误即节点失败（清单格式错/超限不静默忽略）。
	CreateItems func(ctx context.Context, run *types.SopRun, specs []types.SopChildItemSpec) error
	// BriefProvider 提供项目简报正文（可 nil；T64 接线）。失败只降级为空上下文。
	BriefProvider BriefProvider
	// Notes 供「项目现状」块读取黑板决策（可 nil）。
	Notes interfaces.SopProjectNoteRepository
}

// MCPEndpointProvisioner 签发「运行 × 节点」的受限 MCP 端点。
type MCPEndpointProvisioner interface {
	EnsureNodeEndpoint(
		ctx context.Context, tenantID uint64, runID, nodeID string, kbIDs []string,
	) (url, token string, err error)
}

// Engine 是进程内单实例调度器：一个 ticker 循环认领到期运行，每个运行一个推进协程。
//
// active 是「本进程正在跑的 runID → 取消函数」簿记：它同时充当并行上限的计数
// （MaxParallel）与 CancelRun 的入口。跨进程的互斥不靠它，靠 DB 租约（ClaimDue/
// Heartbeat/ExpireLeases）——多实例同时上线也只有一个能抢到同一条 queued 运行。
type Engine struct {
	deps   Deps
	mu     sync.Mutex
	active map[string]context.CancelFunc // runID→取消函数
	// runLocks 是「同一运行同一时刻只有一个推进者」的进程内互斥表（并发调 Advance
	// 时用，见 lockRun）；它的值受 mu 保护，锁本身在锁表之外持有。
	runLocks map[string]*sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	// started 标记调度循环是否已启动：Start 对已启动的引擎是 no-op（绝不取消旧根
	// ctx——那是所有在途 runLoop 的父 ctx，取消它等于把在跑运行打成 cancelled）。
	started bool
	// advanceHook 是推进步骤的测试挂点（生产恒为 nil，走 Advance 的占位实现）；
	// 用它可以让 runLoop 在「推进途中被取消」这类时序确定地复现。
	advanceHook func(ctx context.Context, runID string) (bool, error)
}

// NewEngine 构造引擎：补默认并行上限并初始化 active 簿记。
func NewEngine(d Deps) *Engine {
	if d.MaxParallel <= 0 {
		d.MaxParallel = defaultMaxParallel
	}
	return &Engine{
		deps:     d,
		active:   map[string]context.CancelFunc{},
		runLocks: map[string]*sync.Mutex{},
	}
}

// Deps 返回引擎依赖集（只读；测试与诊断需要按 id 直读运行/模板）。
func (e *Engine) Deps() Deps { return e.deps }

// defaultMaxParallel 是未配置时的并行运行上限。
const defaultMaxParallel = 2

// startQueued 把一个刚被认领（status=running）的运行初始化到可推进状态：
// 建工作区 → 回写 WorkspaceDir → 克隆项目仓库 → 为快照每个节点建 pending 记录
// → 事项联动（todo→in_progress）→ （非测试模式）启动推进循环。
//
// 失败即返回错误：调用方（TickOnce）负责把运行置 failed 并写原因，同一条运行
// 可被再次认领重试（租约过期语义见 sop_run.go）。
func (e *Engine) startQueued(ctx context.Context, run *types.SopRun) error {
	if run == nil {
		return fmt.Errorf("运行不存在")
	}
	ws, err := PrepareWorkspace(e.deps.WorkspaceRoot, run.ID)
	if err != nil {
		return err
	}
	run.WorkspaceDir = ws.Root
	if err := e.deps.Runs.UpdateRun(ctx, run); err != nil {
		return fmt.Errorf("回写工作区失败：%w", err)
	}
	if err := e.cloneProjectRepos(ctx, run, ws); err != nil {
		return err
	}
	if err := e.ensureNodeRuns(ctx, run); err != nil {
		return err
	}
	e.linkItemOnStart(ctx, run)
	if !e.deps.DisableRunLoop {
		e.startRunLoop(run.ID)
	}
	return nil
}

// cloneProjectRepos 克隆运行所属项目绑定的仓库（项目未绑定或非项目运行时为空操作）。
// 失败直接返回：仓库拿不到时后续步骤没有作业目录，重试比带病执行更划算。
func (e *Engine) cloneProjectRepos(ctx context.Context, run *types.SopRun, ws *Workspace) error {
	if run.ProjectID == "" || e.deps.Projects == nil {
		return nil
	}
	proj, err := e.deps.Projects.GetByID(ctx, run.TenantID, run.ProjectID)
	if err != nil {
		return fmt.Errorf("读取项目失败：%w", err)
	}
	if len(proj.Repos) == 0 {
		return nil
	}
	if _, err := CloneRepos(ws, run.ID, proj.Repos); err != nil {
		return err
	}
	return nil
}

// ensureNodeRuns 为运行快照里的每个节点建一条 pending 记录。
//
// 幂等：已存在的 (run_id, node_id) 原样保留——重试/崩溃回收后的再认领不能把
// 已经推进过的节点打回 pending（那会静默重跑并丢历史）。
func (e *Engine) ensureNodeRuns(ctx context.Context, run *types.SopRun) error {
	existing, err := e.deps.Runs.ListNodeRuns(ctx, run.TenantID, run.ID)
	if err != nil {
		return fmt.Errorf("读取节点运行记录失败：%w", err)
	}
	seen := make(map[string]bool, len(existing))
	for _, nr := range existing {
		seen[nr.NodeID] = true
	}
	for i := range run.WorkflowSnapshot.Nodes {
		node := &run.WorkflowSnapshot.Nodes[i]
		if seen[node.ID] {
			continue
		}
		nr := &types.SopNodeRun{
			TenantID: run.TenantID,
			RunID:    run.ID,
			NodeID:   node.ID,
			Status:   types.SopNodePending,
		}
		if err := e.deps.Runs.CreateNodeRun(ctx, nr); err != nil {
			return fmt.Errorf("创建节点 %s 的运行记录失败：%w", node.ID, err)
		}
	}
	return nil
}

// linkItemOnStart 把事项从 todo 推进到 in_progress（开始执行）。
//
// 只处理「当前恰好 todo」的事项：别的状态说明已经有人动过（或根本不适用），
// 引擎不替用户做状态决策。非法迁移/写入失败只记审计，不阻断运行。
func (e *Engine) linkItemOnStart(ctx context.Context, run *types.SopRun) {
	if run.ItemID == "" || e.deps.ItemStatus == nil {
		return
	}
	if e.deps.Items != nil {
		item, err := e.deps.Items.GetByID(ctx, run.TenantID, run.ItemID)
		if err != nil {
			// 事项读不到（如脚本运行、事项已删）就没什么可联动的。
			return
		}
		if item.Status != types.SopItemStatusTodo {
			return
		}
		if !types.CanTransitSopItem(item.Status, types.SopItemStatusInProgress) {
			e.audit(ctx, run, "事项状态未联动",
				fmt.Sprintf("状态机不允许 %s → %s", item.Status, types.SopItemStatusInProgress))
			return
		}
	}
	if err := e.deps.ItemStatus.UpdateStatus(
		ctx, run.TenantID, run.ItemID, types.SopItemStatusInProgress,
	); err != nil {
		e.audit(ctx, run, "事项状态未联动", "写入事项状态失败："+err.Error())
	}
}

// finalizeRun 收口一次运行的终态：汇总节点状态与用量、补 FinishedAt、事项联动、
// 通知与审计。可重复调用（幂等：同样的节点状态得到同样的运行状态）。
//
// 状态口径：
//   - 存在 failed 节点 → run failed（error=首个失败节点的错误文案）；
//   - 否则全部 succeeded/skipped → run succeeded；
//   - 仍有非终态节点（pending/running/waiting_*）→ 不擅自收口，返回错误由调用方决定
//     （正常推进路径只在全部终态后才调本函数）；
//   - run 已是 cancelled → 保留 cancelled，只补时间戳（取消不是失败，不发失败通知）。
func (e *Engine) finalizeRun(ctx context.Context, run *types.SopRun) error {
	if run == nil {
		return fmt.Errorf("运行不存在")
	}
	// 幂等闸门：库里这条运行已收口（终态 + FinishedAt）就直接返回，重入不再产生
	// 事项联动 / 通知 / 审计等副作用。以「库里的新鲜行」为准，不看调用方手里的旧值。
	if cur, err := e.deps.Runs.GetRun(ctx, 0, run.ID); err == nil && cur != nil &&
		isTerminalRun(cur.Status) && cur.FinishedAt != nil {
		return nil
	}
	nrs, err := e.deps.Runs.ListNodeRuns(ctx, run.TenantID, run.ID)
	if err != nil {
		return fmt.Errorf("读取节点运行记录失败：%w", err)
	}
	now := time.Now()
	var usage types.SopUsage
	failedErr := ""
	allTerminal := true
	for i := range nrs {
		nr := &nrs[i]
		usage.InputTokens += nr.Usage.InputTokens
		usage.OutputTokens += nr.Usage.OutputTokens
		usage.CostUSD += nr.Usage.CostUSD
		switch nr.Status {
		case types.SopNodeFailed:
			if failedErr == "" {
				failedErr = firstNonEmpty(nr.Error, nodeDisplayName(run, nr.NodeID))
			}
		case types.SopNodeSucceeded, types.SopNodeSkipped:
		default:
			allTerminal = false
		}
	}
	run.Usage = usage

	status := run.Status
	switch {
	case run.Status == types.SopRunCancelled:
		status = types.SopRunCancelled
	case failedErr != "" || run.Status == types.SopRunFailed:
		status = types.SopRunFailed
		if run.Error == "" {
			run.Error = failedErr
		}
	case !allTerminal:
		return fmt.Errorf("运行 %s 仍有未结束的节点，不能收口", run.ID)
	default:
		status = types.SopRunSucceeded
	}
	if status == types.SopRunFailed {
		// 失败收口：还没开始的节点不可能再跑，标记跳过而不是留在 pending。
		for i := range nrs {
			nr := &nrs[i]
			if nr.Status != types.SopNodePending {
				continue
			}
			nr.Status = types.SopNodeSkipped
			nr.FinishedAt = &now
			if err := e.deps.Runs.UpdateNodeRun(ctx, nr); err != nil {
				log.Printf("[sop-engine] 运行 %s 节点 %s 标记跳过失败：%v", run.ID, nr.NodeID, err)
			}
		}
	}
	run.Status = status
	run.FinishedAt = &now
	if run.StartedAt != nil {
		run.ExecutionDurationMs = now.Sub(*run.StartedAt).Milliseconds()
	}
	if err := e.deps.Runs.UpdateRun(ctx, run); err != nil {
		return fmt.Errorf("写入运行终态失败：%w", err)
	}
	if status == types.SopRunSucceeded {
		e.linkItemOnSuccess(ctx, run)
	}
	e.notifyFinished(ctx, run)
	e.notifyRunFinal(ctx, run)
	e.releaseMCP(ctx, run)
	return nil
}

// releaseMCP 回收本次运行签发的 MCP 端点（失败只记日志：清理不影响运行结论）。
func (e *Engine) releaseMCP(ctx context.Context, run *types.SopRun) {
	if e.deps.ReleaseMCP == nil || run == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[sop-engine] 运行 %s MCP 端点清理 panic：%v", run.ID, r)
		}
	}()
	if err := e.deps.ReleaseMCP(ctx, run.TenantID, run.ID); err != nil {
		log.Printf("[sop-engine] 运行 %s MCP 端点清理失败：%v", run.ID, err)
	}
}

// notifyRunFinal 调 OnRunFinal（项目黑板自动摘要）：panic 与错误都只记日志。
func (e *Engine) notifyRunFinal(ctx context.Context, run *types.SopRun) {
	if e.deps.OnRunFinal == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[sop-engine] 运行 %s 终态回调 panic：%v", run.ID, r)
		}
	}()
	e.deps.OnRunFinal(ctx, run)
}

// linkItemOnSuccess 按快照声明的 OnSuccessItemStatus 推进事项（仅成功时）。
//
// 目标状态口径：模板级（快照 OnSuccessItemStatus）优先；为空时回退到项目级
// OnRunSuccessItemStatus；两者皆空则不做任何改动（引擎不猜业务意图）。
// 状态机允许才写；不允许（或事项读不到）就记审计跳过——成功后的事项状态是
// 引擎唯一的业务副作用，宁可少写也不越过 8 态状态机。
func (e *Engine) linkItemOnSuccess(ctx context.Context, run *types.SopRun) {
	target := strings.TrimSpace(run.WorkflowSnapshot.OnSuccessItemStatus)
	if target == "" {
		target = strings.TrimSpace(e.projectSuccessItemStatus(ctx, run))
	}
	if run.ItemID == "" || target == "" || e.deps.ItemStatus == nil {
		return
	}
	if e.deps.Items != nil {
		item, err := e.deps.Items.GetByID(ctx, run.TenantID, run.ItemID)
		if err != nil {
			// 读不到事项（已删/脚本运行）就没有可联动的对象；读错误绝不能退化成
			// 「照写状态」——那会在不知道当前状态的情况下硬闯状态机。
			return
		}
		if !types.CanTransitSopItem(item.Status, target) {
			e.audit(ctx, run, "事项状态未联动",
				fmt.Sprintf("状态机不允许 %s → %s", item.Status, target))
			return
		}
	}
	if err := e.deps.ItemStatus.UpdateStatus(ctx, run.TenantID, run.ItemID, target); err != nil {
		e.audit(ctx, run, "事项状态未联动", "写入事项状态失败："+err.Error())
	}
}

// projectSuccessItemStatus 取项目级「运行成功后事项状态」回退值。
//
// 非项目运行或项目仓储缺失时为空。仓储返回的 (零值结构体, error) 成对出现，
// 因此必须先判 error 再用结构体，否则会把零值当成「项目没配」以外的语义。
func (e *Engine) projectSuccessItemStatus(ctx context.Context, run *types.SopRun) string {
	if run.ProjectID == "" || e.deps.Projects == nil {
		return ""
	}
	proj, err := e.deps.Projects.GetByID(ctx, run.TenantID, run.ProjectID)
	if err != nil || proj == nil {
		return ""
	}
	return proj.OnRunSuccessItemStatus
}

// notifyFinished 发运行终态通知（succeeded→sop_run_finished，failed→sop_run_failed）。
// 取消不发：取消是用户主动行为，不是需要提醒的异常。
func (e *Engine) notifyFinished(ctx context.Context, run *types.SopRun) {
	kind, action := "", ""
	switch run.Status {
	case types.SopRunSucceeded:
		kind, action = "sop_run_finished", "运行成功"
	case types.SopRunFailed:
		kind, action = "sop_run_failed", "运行失败"
	default:
		return
	}
	title := e.runTitle(ctx, run)
	body := ""
	if run.Status == types.SopRunFailed {
		body = firstNonEmpty(run.Error, "运行失败")
	}
	if e.deps.Notify != nil {
		e.deps.Notify(ctx, run.TenantID, kind, title, body, run.ID)
	}
	detail := title
	if body != "" {
		detail += "：" + body
	}
	e.audit(ctx, run, action, detail)
}

// CancelRun 取消一次正在本进程推进的运行（ops 调用）。
//
// 只取消 active 里的 context：真正的子进程杀灭由 provider.Cancel 负责，runLoop
// 收到取消后走 finishCancelled 把节点与运行落成 cancelled。非 active 的运行
// （queued/paused/无推进协程）不在此处理，由调用方直接收口。
func (e *Engine) CancelRun(runID string) {
	if runID == "" {
		return
	}
	e.mu.Lock()
	cancel := e.active[runID]
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// activeCount 返回本进程正在推进的运行数。
func (e *Engine) activeCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.active)
}

// runTitle 给通知用的标题：优先模板名（取得到时），否则一句通用文案。
//
// 模板读取失败不影响收口：通知标题只是展示，任何读取问题都回落到通用文案。
func (e *Engine) runTitle(ctx context.Context, run *types.SopRun) string {
	const fallback = "SOP 运行已结束"
	if run.TemplateID == "" || e.deps.Templates == nil {
		return fallback
	}
	tpl, err := e.deps.Templates.GetByID(ctx, run.TenantID, run.TemplateID)
	if err != nil || tpl == nil || strings.TrimSpace(tpl.Name) == "" {
		return fallback
	}
	return tpl.Name
}

// audit 往事项活动流写一条审计记录（事件只增不改）。
//
// 事项为空或仓储缺失时静默跳过；事项已不存在（被删/脚本运行）也跳过——活动表
// 拒绝挂在不存在的事项上，这不是错误。其余写失败只记日志：审计是留痕能力，
// 不能让一次通知/状态联动把它变成运行失败点。
func (e *Engine) audit(ctx context.Context, run *types.SopRun, action, detail string) {
	if run == nil || run.ItemID == "" || e.deps.Extras == nil {
		return
	}
	if e.deps.Items != nil {
		if _, err := e.deps.Items.GetByID(ctx, run.TenantID, run.ItemID); err != nil {
			return
		}
	}
	err := e.deps.Extras.AddActivity(ctx, &types.SopItemActivity{
		TenantID: run.TenantID,
		ItemID:   run.ItemID,
		Kind:     types.SopActivityKindAudit,
		Actor:    "system",
		Action:   action,
		Detail:   detail,
	})
	if err != nil {
		log.Printf("[sop-engine] 运行 %s 审计写入失败：%v", run.ID, err)
	}
}

// nodeDisplayName 取节点在快照里的可读名（缺失回落节点 id）。
func nodeDisplayName(run *types.SopRun, nodeID string) string {
	for i := range run.WorkflowSnapshot.Nodes {
		if run.WorkflowSnapshot.Nodes[i].ID == nodeID {
			return firstNonEmpty(run.WorkflowSnapshot.Nodes[i].Name, nodeID)
		}
	}
	return nodeID
}
