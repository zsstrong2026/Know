// node.go 是节点推进的实现：计算就绪节点、按并行额度执行、裁定门禁三段、
// 落实失败四策略（loop / reject / fail / human）与返工/重试预算。
//
// 并发模型：同一轮里就绪的 agent/script 节点各自起一个协程执行，额度是
// Deps.MaxParallel（默认 2），同一 serial_group 每轮最多放行一个节点。
// 节点级错误不终止同批兄弟：兄弟照常跑完，错误只影响本条节点（写入节点记录
// 或把运行推入 waiting/failed）。
//
// 取消口径：任何执行路径拿到 ctx 取消都原样返回 ctx.Err()，由 runLoop 收口为
// cancelled —— 取消不是失败，不得走失败策略。
package engine

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service/sop/executor"
	"github.com/Tencent/WeKnora/internal/types"
)

// gateScriptTimeout 是门禁脚本的执行上限：脚本只做文件断言与 grep，60s 足够；
// 超时按未通过（打回）处理，避免一个挂死的检查卡住整个运行。
const gateScriptTimeout = 60 * time.Second

// ---- 就绪计算 ----

// readyNodes 返回本轮可执行节点（纯函数，不读库）。
//
// 就绪口径：节点 pending 且全部上游已 succeeded/skipped；同一 serial_group
// 每轮最多一个（按定义顺序取第一个）。上游记录缺失视为未就绪（保守等待）。
func readyNodes(def *types.SopDefinition, nrs map[string]*types.SopNodeRun) []types.SopNode {
	if def == nil {
		return nil
	}
	var ready []types.SopNode
	seenGroup := map[string]bool{}
	for _, n := range def.Nodes {
		nr := nrs[n.ID]
		if nr == nil || nr.Status != types.SopNodePending {
			continue
		}
		ok := true
		for _, up := range Upstreams(def, n.ID) {
			upNR := nrs[up]
			if upNR == nil {
				ok = false
				break
			}
			if upNR.Status != types.SopNodeSucceeded && upNR.Status != types.SopNodeSkipped {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		if n.SerialGroup != "" {
			if seenGroup[n.SerialGroup] {
				continue
			}
			seenGroup[n.SerialGroup] = true
		}
		ready = append(ready, n)
	}
	return ready
}

// ---- 运行推进 ----

// Advance 推进一次运行：计算就绪节点并执行 / 等待人工，返回「本轮是否有动作」。
//
// 返回 (false, nil) 表示无事可做（等待子运行、等待人工、已终态），runLoop 据此
// 退避轮询；返回错误表示仓储等基础设施失败，由 runLoop 落成 failed。
//
// 串行化：同一运行的推进在进程内互斥（lockRun）。两个推进者（重复调度、运维手动
// 触发）若同时读同一批 pending 节点，会各自执行一遍——互斥把「读状态→执行→写回」
// 变成整体临界区，后到者看到的已是前者的结果。
func (e *Engine) Advance(ctx context.Context, runID string) (bool, error) {
	unlock := e.lockRun(runID)
	defer unlock()

	run, err := e.deps.Runs.GetRun(ctx, 0, runID)
	if err != nil {
		return false, err
	}
	// 推进链路（执行器、AgentQA 的知识库检索）从 ctx 读租户，而调度循环本身没有
	// 租户上下文；这里按运行行注入，与 automation_runner.go:202 同款做法。所有推进
	// 入口（runLoop、运维手动触发、测试同步驱动）都经过 Advance，一处注入即可。
	ctx = WithRunTenant(ctx, run)
	if run.Status != types.SopRunRunning && run.Status != types.SopRunWaiting {
		return false, nil
	}
	nrs, err := e.deps.Runs.ListNodeRuns(ctx, run.TenantID, runID)
	if err != nil {
		return false, err
	}
	byID := make(map[string]*types.SopNodeRun, len(nrs))
	for i := range nrs {
		byID[nrs[i].NodeID] = &nrs[i]
	}

	// 子流程对账先于就绪计算：子运行可能刚刚到终态，节点状态与产物要先落地，
	// 下一轮的 readyNodes 才能看到下游就绪。有推进就本轮收工，让调用方继续推进。
	childMoved, err := e.reconcileChildRuns(ctx, run, byID)
	if err != nil {
		return childMoved, err
	}
	if childMoved {
		return true, nil
	}

	ready := readyNodes(&run.WorkflowSnapshot, byID)
	if len(ready) == 0 {
		return e.settleNoReady(ctx, run, nrs)
	}

	ws, err := PrepareWorkspace(e.deps.WorkspaceRoot, run.ID)
	if err != nil {
		return false, err
	}

	limit := e.deps.MaxParallel
	if limit <= 0 {
		limit = defaultMaxParallel
	}
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error

	for i := range ready {
		node := ready[i]
		nr := byID[node.ID]
		if nr == nil {
			continue
		}
		switch node.Type {
		case types.SopNodeTypeApproval:
			nr.Status = types.SopNodeWaitingApproval
			nr.GateReason = firstNonEmpty(node.ApproverHint, "等待人工审批")
			now := time.Now()
			nr.FinishedAt = &now
			if err := e.deps.Runs.UpdateNodeRun(ctx, nr); err != nil {
				return true, err
			}
			e.markRunStatus(ctx, run, types.SopRunWaiting)
			e.notifyApproval(ctx, run, &node, nr.GateReason)
			ws.CheckpointBestEffort(ctx, "node "+node.ID+" waiting approval")
		case types.SopNodeTypeSOP:
			// 子流程：建 child run 并让节点进入 running，后续由 reconcileChildRuns
			// 回看子运行终态（产物导入/失败策略见 sop_node.go）。
			if err := e.startChildRun(ctx, run, &node, nr); err != nil {
				return true, err
			}
		default:
			wg.Add(1)
			go func(n types.SopNode, nodeRun *types.SopNodeRun) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				if err := e.executeNode(ctx, run, &n, nodeRun, ws); err != nil {
					errMu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					errMu.Unlock()
				}
			}(node, nr)
		}
	}
	wg.Wait()
	if firstErr != nil {
		return true, firstErr
	}

	e.syncRunUsage(ctx, run)
	return true, nil
}

// lockRun 取得某运行的推进锁（进程内串行化），返回解锁函数。
//
// 锁表本身受 e.mu 保护，锁在锁表之外持有：不要用 e.mu 直接当运行锁，那会把不同
// 运行的推进也串成一列。条目只增不删——删除需要等待者计数，而单进程内运行的
// 数量远小于内存能承受的范围，宁可留一条空 mutex 也不引入删除竞态。
func (e *Engine) lockRun(runID string) func() {
	e.mu.Lock()
	mu := e.runLocks[runID]
	if mu == nil {
		mu = &sync.Mutex{}
		e.runLocks[runID] = mu
	}
	e.mu.Unlock()
	mu.Lock()
	return mu.Unlock
}

// settleNoReady 处理「本轮没有就绪节点」：还在等执行/人工就退避，全部终态则收口。
//
// 收口口径与 finalizeRun 一致：有 failed 节点就是 run failed（原因取首个失败文案），
// 有 cancelled 节点就是 run cancelled；只有全部 succeeded/skipped 才是 run succeeded。
func (e *Engine) settleNoReady(ctx context.Context, run *types.SopRun, nrs []types.SopNodeRun) (bool, error) {
	allTerminal := true
	failedReason := ""
	cancelled := false
	for i := range nrs {
		switch nrs[i].Status {
		case types.SopNodeFailed:
			if failedReason == "" {
				failedReason = firstNonEmpty(nrs[i].Error, nodeDisplayName(run, nrs[i].NodeID))
			}
		case types.SopNodeCancelled:
			cancelled = true
		case types.SopNodeSucceeded, types.SopNodeSkipped:
		default:
			allTerminal = false
		}
	}
	if !allTerminal {
		return false, nil
	}
	if run.Status != types.SopRunRunning {
		return false, nil
	}
	if cancelled {
		e.markRunStatus(ctx, run, types.SopRunCancelled)
		return true, nil
	}
	if failedReason != "" {
		e.markRunFailed(ctx, run, failedReason)
		return true, nil
	}
	run.Status = types.SopRunSucceeded
	if err := e.deps.Runs.UpdateRun(ctx, run); err != nil {
		return true, err
	}
	return true, nil
}

// syncRunUsage 把各节点累计用量汇总回运行（终态收口时 finalizeRun 会再汇总一次，
// 这里只保证运行中途打开详情也能看到用量）。
func (e *Engine) syncRunUsage(ctx context.Context, run *types.SopRun) {
	nrs, err := e.deps.Runs.ListNodeRuns(ctx, run.TenantID, run.ID)
	if err != nil {
		return
	}
	var usage types.SopUsage
	for i := range nrs {
		usage.InputTokens += nrs[i].Usage.InputTokens
		usage.OutputTokens += nrs[i].Usage.OutputTokens
		usage.CostUSD += nrs[i].Usage.CostUSD
	}
	if usage == run.Usage {
		return
	}
	fresh, err := e.deps.Runs.GetRun(ctx, 0, run.ID)
	if err != nil || fresh == nil || isTerminalRun(fresh.Status) {
		return
	}
	fresh.Usage = usage
	if err := e.deps.Runs.UpdateRun(ctx, fresh); err != nil {
		log.Printf("[sop-engine] 运行 %s 用量回写失败：%v", run.ID, err)
	}
}

// markRunStatus 把运行切到指定状态（只改状态列，基于库里最新行）。
//
// 已终态的运行不再接受状态改写（取消/失败后不允许被并行节点翻回 waiting）。
// 不修改调用方手里的 run（那是并行节点共享的只读快照，改动会造成数据竞争）。
func (e *Engine) markRunStatus(ctx context.Context, run *types.SopRun, status string) {
	if status == "" {
		return
	}
	fresh, err := e.deps.Runs.GetRun(ctx, 0, run.ID)
	if err != nil || fresh == nil || isTerminalRun(fresh.Status) {
		return
	}
	fresh.Status = status
	if err := e.deps.Runs.UpdateRun(ctx, fresh); err != nil {
		log.Printf("[sop-engine] 运行 %s 状态写入失败：%v", run.ID, err)
	}
}

// markRunFailed 把运行置失败并写原因（已终态则不动）。
func (e *Engine) markRunFailed(ctx context.Context, run *types.SopRun, reason string) {
	fresh, err := e.deps.Runs.GetRun(ctx, 0, run.ID)
	if err != nil || fresh == nil || isTerminalRun(fresh.Status) {
		return
	}
	fresh.Status = types.SopRunFailed
	fresh.Error = reason
	if err := e.deps.Runs.UpdateRun(ctx, fresh); err != nil {
		log.Printf("[sop-engine] 运行 %s 置失败写入失败：%v", run.ID, err)
	}
}

// ---- 单节点执行 ----

// nodeScoped 返回节点的副本，把「节点自己的产出路径」改写到 out/<node_id>/。
//
// 布局与 inputs/<node_id> 对称：并行兄弟节点各写各的 out/<node_id>/，不再共用一个
// 运行级 out/（否则两个节点都写 out/a.md，后写的静默覆盖前者的产物）。改写范围：
//   - artifact_outputs[].path：out/x → out/<node>/x（其它前缀原样保留，如 work/repos）；
//   - gate_extra 与 script 正文里的 out/ 引用：同样按词边界加节点段，保证「写完自己
//     检查自己」的脚本仍然自洽。跨节点读上游产物请改用物化后的 inputs/<upstream>/
//     （运行根目录就是 gate 脚本与 script 的工作目录）。
//
// 门禁命令由改写后的产物路径现编（CompileGateCommands 读的是同一个节点副本），
// 所以三处口径一致。落库的产物 Path 是运行根口径 out/<node>/...：下游物化、产物
// 列表、审批指纹都按这个布局读。
func nodeScoped(def *types.SopNode) *types.SopNode {
	if def == nil {
		return &types.SopNode{}
	}
	out := *def
	out.ArtifactOutputs = make([]types.SopArtifactOutput, len(def.ArtifactOutputs))
	copy(out.ArtifactOutputs, def.ArtifactOutputs)
	for i := range out.ArtifactOutputs {
		out.ArtifactOutputs[i].Path = scopeOutPath(def.ID, out.ArtifactOutputs[i].Path)
		if out.ArtifactOutputs[i].Check.Type == types.SopArtifactCheckCommand {
			out.ArtifactOutputs[i].Check.Command = scopeOutRefs(def.ID, out.ArtifactOutputs[i].Check.Command)
		}
	}
	out.GateExtra = scopeOutRefs(def.ID, def.GateExtra)
	out.Script = scopeOutRefs(def.ID, def.Script)
	return &out
}

// scopeOutPath 把声明路径里的 out/ 前缀改写到 out/<node_id>/（无前缀原样返回）。
func scopeOutPath(nodeID, rel string) string {
	p := filepath.ToSlash(strings.TrimSpace(rel))
	if p == "" {
		return p
	}
	seg := sanitizeSegment(nodeID)
	switch {
	case p == "out":
		return slashJoin("out", seg)
	case strings.HasPrefix(p, "out/"):
		return slashJoin("out", seg, strings.TrimPrefix(p, "out/"))
	default:
		return p
	}
}

// outRefRe 匹配 shell 片段里作为独立词出现的 out/ 路径前缀（前面只能是串首、
// 空白或 shell 的分隔/重定向字符）。用词边界而非裸替换，避免把 pout/ 或 shout/
// 误伤。
var outRefRe = regexp.MustCompile(`(^|[\s;|&<>"'=(])out/`)

// scopeOutRefs 把一段 shell 文本里的 out/ 引用改写到 out/<node_id>/。
func scopeOutRefs(nodeID, text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	return outRefRe.ReplaceAllString(text, "${1}out/"+sanitizeSegment(nodeID)+"/")
}

// ensureNodeOutDir 建好并返回节点的产出目录 out/<node_id>（绝对路径，正斜杠）。
func ensureNodeOutDir(ws *Workspace, nodeID string) (string, error) {
	if ws == nil || strings.TrimSpace(ws.OutDir) == "" {
		return "", fmt.Errorf("工作区未初始化")
	}
	dir := slashJoin(ws.OutDir, sanitizeSegment(nodeID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建节点产出目录 out/%s 失败：%w", sanitizeSegment(nodeID), err)
	}
	return dir, nil
}

// executeNode 执行一个 agent/script 节点：渲染任务书 → 物化上游产物 → 调用执行器
// → 收集产物 → 门禁裁定 → 检查点。错误只落在本条节点记录上。
//
// 产出目录按节点隔离：每个节点在 out/<node_id> 下开工（与 inputs/<node_id> 对称），
// 并行兄弟节点各写各的，不会再互相覆盖 out/a.md 这类同名产物。声明里的产物路径、
// 门禁命令与门禁脚本一律按节点产出目录解析（见 nodeScoped），落库的产物路径则是
// 运行根口径 out/<node_id>/...（下游物化与产物列表都按此读）。
func (e *Engine) executeNode(
	ctx context.Context, run *types.SopRun, def *types.SopNode, nr *types.SopNodeRun, ws *Workspace,
) error {
	node := nodeScoped(def)
	outDir, err := ensureNodeOutDir(ws, node.ID)
	if err != nil {
		return e.executionError(ctx, run, node, nr, err.Error(), ws)
	}
	nr.Attempt++
	nr.Status = types.SopNodeRunning
	started := time.Now()
	nr.StartedAt = &started
	nr.FinishedAt = nil
	if err := e.deps.Runs.UpdateNodeRun(ctx, nr); err != nil {
		return err
	}
	// 进入 running 就广播一次：前端卡片不必等完成/失败才有事件，运行中的
	// 「正在执行」状态能立刻显示出来。
	e.publishNodeStatus(run.ID, nr)

	resolved := ResolvedNode{}
	if e.deps.Resolver != nil {
		r, err := e.deps.Resolver.Resolve(ctx, run, node)
		if err != nil {
			return e.executionError(ctx, run, node, nr, "解析执行角色失败："+err.Error(), ws)
		}
		resolved = r
	}

	nrs, err := e.deps.Runs.ListNodeRuns(ctx, run.TenantID, run.ID)
	if err != nil {
		return err
	}
	byID := make(map[string]*types.SopNodeRun, len(nrs))
	for i := range nrs {
		byID[nrs[i].NodeID] = &nrs[i]
	}
	var upstreamIDs []string
	for _, up := range Upstreams(&run.WorkflowSnapshot, node.ID) {
		upstreamIDs = append(upstreamIDs, up)
	}
	upstreamSet := map[string]bool{}
	upOutputs := map[string]string{}
	upNames := map[string]string{}
	for _, up := range upstreamIDs {
		upstreamSet[up] = true
		if o := byID[up]; o != nil {
			upOutputs[up] = o.Output
		}
		upNames[up] = nodeDisplayName(run, up)
	}
	refOutputs := map[string]string{}
	for _, ref := range node.TaskRefs {
		if r := byID[ref]; r != nil {
			refOutputs[ref] = r.Output
		}
	}

	projectContext := e.projectContextFor(ctx, run, ws)
	// 首个节点把「项目现状」文本回写运行行：运行详情「任务书」视图要展示本次实际注入的内容
	// （后续节点重算的文本可能因黑板新增而不同，展示口径以首次注入为准）。
	if strings.TrimSpace(projectContext) != "" && strings.TrimSpace(run.ProjectContext) == "" {
		run.ProjectContext = projectContext
		if err := e.deps.Runs.UpdateRun(ctx, run); err != nil {
			log.Printf("[sop-engine] 运行 %s 回写项目现状失败：%v", run.ID, err)
		}
	}
	prompt := RenderTaskBook(TaskBookInput{
		Node:                   *node,
		Item:                   e.loadItem(ctx, run),
		ProjectContext:         projectContext,
		RunInput:               run.Input,
		UpstreamOutputs:        upOutputs,
		UpstreamNames:          upNames,
		RefOutputs:             refOutputs,
		Feedback:               nr.Feedback,
		HumanContext:           run.HumanContext,
		AnsweredClarifications: e.answeredClarifications(ctx, run),
		Role:                   resolved.Role,
		ResolvedSkills:         resolved.Skills,
		InputsDir:              slashJoin(ws.InputsDir, sanitizeSegment(node.ID)),
		OutDir:                 outDir,
	})

	// 物化上游产物到 inputs/<node>/：只取直接上游在本运行内的产物。
	// 上游重跑/返工会为同一路径留下多条尝试记录，只取最新一份（见 latestArtifacts）。
	arts, err := e.deps.Runs.ListArtifacts(ctx, run.TenantID, run.ID)
	if err != nil {
		return err
	}
	var inputs []types.SopArtifact
	for _, a := range arts {
		if upstreamSet[a.NodeID] {
			inputs = append(inputs, a)
		}
	}
	if err := MaterializeInputs(ws, latestArtifacts(inputs), ws.Root); err != nil {
		return e.executionError(ctx, run, node, nr, err.Error(), ws)
	}

	provider, err := e.deps.Registry.Resolve(node)
	if err != nil {
		return e.executionError(ctx, run, node, nr, err.Error(), ws)
	}
	if err := provider.Prepare(ctx, node, ws.Root); err != nil {
		return e.executionError(ctx, run, node, nr, "准备执行环境失败："+err.Error(), ws)
	}

	model := resolved.Model
	if model == "" {
		model = node.Executor.Model
	}
	tb := executor.TaskBook{
		RunID:             run.ID,
		NodeRunID:         nr.ID,
		NodeID:            node.ID,
		NodeName:          node.Name,
		Prompt:            prompt,
		SystemPrompt:      RenderAgentSystem(resolved.Role, resolved.Skills, node.ExtraRules, ""),
		WorkspaceRoot:     ws.Root,
		InputsDir:         slashJoin(ws.InputsDir, sanitizeSegment(node.ID)),
		OutDir:            outDir,
		Feedback:          nr.Feedback,
		Attempt:           nr.Attempt,
		ResumeSessionID:   nr.ResumeSessionID,
		Timeout:           nodeTimeout(node),
		Env:               node.CredentialEnv,
		Script:            node.Script,
		Model:             model,
		Credential:        resolved.Credential,
		Role:              resolved.Role,
		MCPServers:        resolved.MCPServers,
		Skills:            resolved.Skills,
		KBIDs:             resolved.KBIDs,
		LoopMaxIterations: node.LoopMaxIterations,
	}

	// CLI 节点：签发受限 Know MCP 端点（检索 + 工作区读写）。
	// 平台节点不需要：它通过会话工具直接检索，MCP 子集由 Resolver 给。
	if node.Executor.Kind == types.SopExecutorCLI && e.deps.MCP != nil {
		kbIDs := resolved.KBIDs
		if node.Executor.KBScope == "all" {
			kbIDs = nil // 空=全部自有 KB（服务端语义）
		}
		url, token, mcpErr := e.deps.MCP.EnsureNodeEndpoint(ctx, run.TenantID, run.ID, node.ID, kbIDs)
		if mcpErr != nil {
			return e.executionError(ctx, run, node, nr, "无法生成 Know MCP 凭据："+mcpErr.Error(), ws)
		}
		tb.KnowMCP = &executor.KnowMCPEndpoint{
			URL: url, Token: token, KBScope: node.Executor.KBScope, KBIDs: resolved.KBIDs,
		}
	}

	logFile := e.openAttemptLog(ws, node.ID, nr.Attempt)
	if logFile != nil {
		defer logFile.Close()
	}
	emit := func(ev executor.StreamEvent) {
		if logFile == nil {
			return
		}
		line, mErr := json.Marshal(ev)
		if mErr != nil {
			return
		}
		_, _ = logFile.Write(append(line, '\n'))
	}

	execCtx, cancel := context.WithTimeout(ctx, nodeTimeout(node))
	defer cancel()
	res, err := provider.Execute(execCtx, tb, executor.ExecOptions{Emit: emit, SessionID: nr.SessionID})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return e.executionError(ctx, run, node, nr, err.Error(), ws)
	}
	nr.Usage.InputTokens += res.Usage.InputTokens
	nr.Usage.OutputTokens += res.Usage.OutputTokens
	nr.Usage.CostUSD += res.Usage.CostUSD

	// 挂起等用户输入（HITL）：不走门禁，节点原地等待补充信息后重跑。
	if res.Suspended {
		nr.Status = types.SopNodePendingInput
		nr.SessionID = res.SessionID
		nr.ResumeSessionID = res.ResumeSessionID
		nr.GateReason = "需要补充信息"
		if err := e.deps.Runs.UpdateNodeRun(ctx, nr); err != nil {
			return err
		}
		e.markRunStatus(ctx, run, types.SopRunWaiting)
		e.notifyApproval(ctx, run, node, "需要补充信息")
		ws.CheckpointBestEffort(ctx, fmt.Sprintf("node %s waiting input", node.ID))
		return nil
	}

	if err := e.collectArtifacts(ctx, run, node, nr, ws); err != nil {
		return err
	}
	if err := e.applyGateOutcome(ctx, run, node, nr, res, ws); err != nil {
		return err
	}
	// 门禁通过后按节点声明建子事项（清单格式错/超限 → 节点失败）。
	if nr.Status == types.SopNodeSucceeded {
		if err := e.maybeCreateChildItems(ctx, run, node, ws); err != nil {
			nr.Status, nr.Error = types.SopNodeFailed, err.Error()
			now := time.Now()
			nr.FinishedAt = &now
			if uerr := e.deps.Runs.UpdateNodeRun(ctx, nr); uerr != nil {
				return uerr
			}
			e.publishNodeStatus(run.ID, nr)
			e.markRunFailed(ctx, run, err.Error())
			return nil
		}
	}
	ws.CheckpointBestEffort(ctx, fmt.Sprintf("node %s attempt %d %s", node.ID, nr.Attempt, nr.GateResult))
	// 自动归档：门禁通过且节点开了 auto_ingest 才做；失败只是少一次沉淀。
	if node.AutoIngest && e.deps.AutoIngest != nil && nr.Status == types.SopNodeSucceeded {
		arts, err := e.deps.Runs.ListArtifacts(ctx, run.TenantID, run.ID)
		if err == nil {
			var own []*types.SopArtifact
			for i := range arts {
				if arts[i].NodeID == node.ID && arts[i].Attempt == nr.Attempt {
					art := arts[i]
					own = append(own, &art)
				}
			}
			if len(own) > 0 {
				e.deps.AutoIngest(ctx, run, node, own)
			}
		}
	}
	return nil
}

// publishEvent 把事件推给 SSE 订阅方（EventSink 未装配时静默跳过）。
func (e *Engine) publishEvent(runID string, ev executor.StreamEvent) {
	if e.deps.EventSink == nil || runID == "" {
		return
	}
	if ev.At.IsZero() {
		ev.At = time.Now()
	}
	e.deps.EventSink(runID, ev)
}

// publishNodeStatus 广播节点状态变更（前端据此刷新步骤卡片）。
func (e *Engine) publishNodeStatus(runID string, nr *types.SopNodeRun) {
	if e.deps.EventSink == nil || nr == nil {
		return
	}
	payload, err := json.Marshal(nr)
	if err != nil {
		return
	}
	e.publishEvent(runID, executor.StreamEvent{
		Type: executor.StreamEventNodeStatus, Payload: payload, At: time.Now(),
	})
}

// executionError 落实「执行器报错」的重试口径：预算内回 pending 并带反馈；
// 预算耗尽转人工（run waiting），不发失败通知。
func (e *Engine) executionError(
	ctx context.Context, run *types.SopRun, node *types.SopNode, nr *types.SopNodeRun, reason string, ws *Workspace,
) error {
	maxAttempts := node.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = types.SopDefaultMaxAttempts
	}
	if nr.Attempt < maxAttempts {
		nr.Status = types.SopNodePending
		nr.Feedback = reason
		nr.GateResult = ""
		nr.GateReason = reason
		if !node.CarryOver {
			nr.SessionID, nr.ResumeSessionID = "", ""
		}
		return e.deps.Runs.UpdateNodeRun(ctx, nr)
	}
	nr.Status = types.SopNodeWaitingApproval
	nr.Error = reason
	nr.GateReason = "执行失败：" + reason
	now := time.Now()
	nr.FinishedAt = &now
	if err := e.deps.Runs.UpdateNodeRun(ctx, nr); err != nil {
		return err
	}
	e.markRunStatus(ctx, run, types.SopRunWaiting)
	e.notifyApproval(ctx, run, node, "执行失败："+reason)
	ws.CheckpointBestEffort(ctx, fmt.Sprintf("node %s attempt %d error", node.ID, nr.Attempt))
	return nil
}

// collectArtifacts 逐条校验节点声明的产物并按内容指纹入库（attempt=nr.Attempt）。
// 缺失/越界/不可读在本函数里不报错——那是门禁第 ② 段的裁定范围。
//
// 产物历史按尝试累积（同一路径每次尝试一条），由下游消费方去重（latestArtifacts），
// 不做原地更新：运行历史可回溯本身就是产物表的价值。
func (e *Engine) collectArtifacts(
	ctx context.Context, run *types.SopRun, node *types.SopNode, nr *types.SopNodeRun, ws *Workspace,
) error {
	for _, out := range node.ArtifactOutputs {
		rel := strings.TrimSpace(out.Path)
		if rel == "" {
			continue
		}
		full, err := SafeWorkspacePath(ws, rel)
		if err != nil {
			continue
		}
		st, err := os.Stat(full)
		if err != nil || st.IsDir() {
			continue
		}
		sum, err := hashFile(full)
		if err != nil {
			log.Printf("[sop-engine] 节点 %s 产物指纹失败：%v", node.ID, err)
			continue
		}
		name := strings.TrimSpace(out.Name)
		if name == "" {
			name = filepath.Base(full)
		}
		art := &types.SopArtifact{
			TenantID:  run.TenantID,
			RunID:     run.ID,
			NodeID:    node.ID,
			Attempt:   nr.Attempt,
			Name:      name,
			Path:      filepath.ToSlash(rel),
			MediaType: out.MediaType,
			Size:      st.Size(),
			SHA256:    sum,
		}
		if err := e.deps.Runs.CreateArtifact(ctx, art); err != nil {
			return err
		}
	}
	return nil
}

// latestArtifacts 按 (node, path) 只保留 attempt 最大的一条产物。
//
// 产物行随每次尝试累积：上游返工/重跑后同一路径会有多条记录。下游物化输入时若
// 全量传入，MaterializeInputs 会因「inputs/<node>/同名文件」直接报错，连累整条
// 下游链。这里取最高 attempt 的一条，恰好是上游最新一次成功执行的产物。
// 入参需按 attempt 升序（ListArtifacts 的排序保证了这一点），同段并列时后到者胜。
func latestArtifacts(arts []types.SopArtifact) []types.SopArtifact {
	if len(arts) == 0 {
		return nil
	}
	out := make([]types.SopArtifact, 0, len(arts))
	index := map[string]int{}
	for _, a := range arts {
		key := a.NodeID + "\x00" + filepath.ToSlash(a.Path)
		if i, ok := index[key]; ok {
			if a.Attempt < out[i].Attempt {
				continue
			}
			out[i] = a
			continue
		}
		index[key] = len(out)
		out = append(out, a)
	}
	return out
}

// applyGateOutcome 三段裁定（spec §4.2）：① 执行器上报一致采纳；② 必需产物契约；
// ③ 门禁脚本退出码。裁定后按四策略落实节点状态。
func (e *Engine) applyGateOutcome(
	ctx context.Context, run *types.SopRun, node *types.SopNode, nr *types.SopNodeRun, res executor.ExecResult, ws *Workspace,
) error {
	ev := GateEvidence{Reported: res.GateResult, Reason: res.GateReason}
	if !gateVerdictKnown(res.GateResult) {
		for _, a := range node.ArtifactOutputs {
			if !a.Required {
				continue
			}
			full, err := SafeWorkspacePath(ws, a.Path)
			if err != nil {
				ev.MissingArtifacts = append(ev.MissingArtifacts, a.Path)
				continue
			}
			if st, err := os.Stat(full); err != nil || st.Size() == 0 {
				ev.MissingArtifacts = append(ev.MissingArtifacts, a.Path)
			}
		}
		if len(ev.MissingArtifacts) == 0 {
			if script := JoinGateScript(CompileGateCommands(node)); script != "" {
				out, ok := runGateScript(ctx, ws, script)
				ev.ScriptRan, ev.ScriptOK, ev.ScriptOutput = true, ok, out
			}
		}
	}

	verdict, reason := DecideGate(ev)
	nr.GateResult, nr.GateReason = verdict, reason
	now := time.Now()
	switch verdict {
	case types.SopGatePass:
		nr.Status, nr.Output = types.SopNodeSucceeded, res.Output
		nr.Error = ""
		// 上一轮的返工意见只服务于本轮；通过后必须清掉，否则重试/重跑会把
		// 旧意见当成新指令再喂给执行器。GateResult 保留 pass 作为最终结论。
		nr.Feedback, nr.GateReason = "", ""
		nr.SessionID, nr.ResumeSessionID = res.SessionID, res.ResumeSessionID
		nr.FinishedAt = &now
	case types.SopGateRework:
		return e.handleFailure(ctx, run, node, nr, reason, ws)
	case types.SopGateFail:
		nr.Status, nr.Error = types.SopNodeFailed, reason
		nr.FinishedAt = &now
		if err := e.deps.Runs.UpdateNodeRun(ctx, nr); err != nil {
			return err
		}
		e.markRunFailed(ctx, run, reason)
		return nil
	case types.SopGateHuman:
		nr.Status = types.SopNodeWaitingApproval
		nr.FinishedAt = &now
		e.markRunStatus(ctx, run, types.SopRunWaiting)
		e.notifyApproval(ctx, run, node, reason)
	}
	if err := e.deps.Runs.UpdateNodeRun(ctx, nr); err != nil {
		return err
	}
	e.publishNodeStatus(run.ID, nr)
	return nil
}

// handleFailure 落实失败四策略（spec §4.2）：fail 直接失败、human 转人工、
// reject 打回上游并重置下游、loop 在重试预算内自动重跑。
func (e *Engine) handleFailure(
	ctx context.Context, run *types.SopRun, node *types.SopNode, nr *types.SopNodeRun, reason string, ws *Workspace,
) error {
	switch node.OnFailure {
	case types.SopOnFailureFail:
		nr.Status, nr.Error = types.SopNodeFailed, reason
		now := time.Now()
		nr.FinishedAt = &now
		if err := e.deps.Runs.UpdateNodeRun(ctx, nr); err != nil {
			return err
		}
		e.markRunFailed(ctx, run, reason)
		return nil
	case types.SopOnFailureHuman:
		nr.Status = types.SopNodeWaitingApproval
		now := time.Now()
		nr.FinishedAt = &now
		if err := e.deps.Runs.UpdateNodeRun(ctx, nr); err != nil {
			return err
		}
		e.markRunStatus(ctx, run, types.SopRunWaiting)
		e.notifyApproval(ctx, run, node, reason)
		return nil
	case types.SopOnFailureReject:
		nr.ReworkCount++
		maxReworks := node.MaxReworks
		if maxReworks <= 0 {
			maxReworks = types.SopDefaultMaxReworks
		}
		if nr.ReworkCount >= maxReworks {
			nr.Status = types.SopNodeWaitingApproval
			now := time.Now()
			nr.FinishedAt = &now
			if err := e.deps.Runs.UpdateNodeRun(ctx, nr); err != nil {
				return err
			}
			e.markRunStatus(ctx, run, types.SopRunWaiting)
			e.notifyApproval(ctx, run, node, "返工次数已达上限："+reason)
			return nil
		}
		target := strings.TrimSpace(node.RejectTo)
		if target == "" {
			// 定义错误：不能悄悄退化成「本节点重跑」——那与 loop 语义混淆，用户
			// 永远不知道模板缺了打回目标。带着清楚的原因让运行失败。
			reason := fmt.Sprintf("步骤「%s」配置了失败后打回上游，但没选打回目标，请到模板里补上", nodeDisplayName(run, node.ID))
			nr.Status, nr.Error = types.SopNodeFailed, reason
			now := time.Now()
			nr.FinishedAt = &now
			if err := e.deps.Runs.UpdateNodeRun(ctx, nr); err != nil {
				return err
			}
			e.markRunFailed(ctx, run, reason)
			return nil
		}
		// 只重置「目标 → 本节点」这条链：无条件重置目标的全部下游会把并行兄弟
		// 分支的产出一起抹掉，下游重跑时就读不到本该存在的数据。
		ids := NodesOnPaths(&run.WorkflowSnapshot, target, node.ID)
		if err := e.deps.Runs.ResetNodes(ctx, run.ID, ids, carryOverOf(&run.WorkflowSnapshot)); err != nil {
			return err
		}
		// ResetNodes 已把 Attempt 归零、计时/会话按 carry_over 处理；这里只补
		// 「谁被打回、为什么」：Feedback 只给目标节点，ReworkCount 继续记账。
		// 写入基于重置后的新鲜行，绝不把重置前的 Output/GateResult/FinishedAt 写回去。
		for _, id := range ids {
			fresh, err := e.deps.Runs.GetNodeRun(ctx, run.TenantID, run.ID, id)
			if err != nil {
				return err
			}
			if id == target {
				fresh.Feedback = reason
				fresh.ReworkCount++
			}
			if err := e.deps.Runs.UpdateNodeRun(ctx, fresh); err != nil {
				return err
			}
		}
		// 打回方自身也回到 pending（它通常也在重置范围内，这里只补状态与反馈）。
		// ReworkCount 必须沿用本轮自增后的值：ResetNodes 不动它，但库里那行还是
		// 自增前的数，不显式带回就等于本轮返工不计数，重试上限永远到不了。
		sender, err := e.deps.Runs.GetNodeRun(ctx, run.TenantID, run.ID, node.ID)
		if err != nil {
			return err
		}
		sender.Status = types.SopNodePending
		sender.Feedback = reason
		sender.ReworkCount = nr.ReworkCount
		if err := e.deps.Runs.UpdateNodeRun(ctx, sender); err != nil {
			return err
		}
		ws.CheckpointBestEffort(ctx, fmt.Sprintf("node %s rework to %s", node.ID, target))
		return nil
	default: // loop
		maxAttempts := node.MaxAttempts
		if maxAttempts <= 0 {
			maxAttempts = types.SopDefaultMaxAttempts
		}
		if nr.Attempt >= maxAttempts {
			nr.Status = types.SopNodeWaitingApproval
			now := time.Now()
			nr.FinishedAt = &now
			if err := e.deps.Runs.UpdateNodeRun(ctx, nr); err != nil {
				return err
			}
			e.markRunStatus(ctx, run, types.SopRunWaiting)
			e.notifyApproval(ctx, run, node, "自动重试次数已达上限："+reason)
			return nil
		}
		nr.Status = types.SopNodePending
		nr.Feedback = reason
		if !node.CarryOver {
			nr.SessionID, nr.ResumeSessionID = "", ""
		}
		return e.deps.Runs.UpdateNodeRun(ctx, nr)
	}
}

// ---- 辅助 ----

// gateVerdictKnown 判断执行器上报的门禁结论是否是四值之一（空=缺失，交裁定）。
func gateVerdictKnown(v string) bool {
	switch v {
	case types.SopGatePass, types.SopGateRework, types.SopGateFail, types.SopGateHuman:
		return true
	}
	return false
}

// nodeTimeout 取节点执行超时（未配置回落到默认 60 分钟）。
func nodeTimeout(node *types.SopNode) time.Duration {
	minutes := node.TimeoutMinutes
	if minutes <= 0 {
		minutes = types.SopDefaultTimeoutMinutes
	}
	return time.Duration(minutes) * time.Minute
}

// runGateScript 在运行根执行门禁脚本（Git Bash 语义，set -e 已由 JoinGateScript 写入）。
// 返回合并输出与是否通过；超时按未通过处理。
func runGateScript(ctx context.Context, ws *Workspace, script string) (string, bool) {
	if ws == nil || strings.TrimSpace(script) == "" {
		return "", true
	}
	ctx, cancel := context.WithTimeout(ctx, gateScriptTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", script)
	cmd.Dir = ws.Root
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return firstNonEmpty(text, "检查超时"), false
		}
		return firstNonEmpty(text, err.Error()), false
	}
	return text, true
}

// openAttemptLog 打开本次尝试的事件日志文件（失败只记日志，不影响执行）。
func (e *Engine) openAttemptLog(ws *Workspace, nodeID string, attempt int) *os.File {
	if ws == nil || strings.TrimSpace(ws.LogsDir) == "" {
		return nil
	}
	if err := os.MkdirAll(ws.LogsDir, 0o755); err != nil {
		return nil
	}
	path := filepath.Join(
		ws.LogsDir,
		fmt.Sprintf("%s-attempt-%d.jsonl", sanitizeSegment(nodeID), attempt),
	)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Printf("[sop-engine] 节点 %s 日志文件打开失败：%v", nodeID, err)
		return nil
	}
	return f
}

// loadItem 取运行关联事项（无事项/读不到时返回 nil，任务书有兜底）。
func (e *Engine) loadItem(ctx context.Context, run *types.SopRun) *types.SopItem {
	if run.ItemID == "" || e.deps.Items == nil {
		return nil
	}
	item, err := e.deps.Items.GetByID(ctx, run.TenantID, run.ItemID)
	if err != nil {
		return nil
	}
	return item
}

// answeredClarifications 取事项已答澄清（任务书「已确认的信息」块）。
func (e *Engine) answeredClarifications(ctx context.Context, run *types.SopRun) []types.SopItemClarification {
	if run.ItemID == "" || e.deps.Extras == nil {
		return nil
	}
	list, err := e.deps.Extras.ListClarifications(ctx, run.TenantID, run.ItemID)
	if err != nil {
		return nil
	}
	var out []types.SopItemClarification
	for _, c := range list {
		if c.Status == types.SopClarificationStatusAnswered {
			out = append(out, c)
		}
	}
	return out
}

// notifyApproval 发「需要人工处理」通知并留审计（事项为空时只发通知）。
func (e *Engine) notifyApproval(ctx context.Context, run *types.SopRun, node *types.SopNode, reason string) {
	if e.deps.Notify != nil {
		title := firstNonEmpty(node.Name, node.ID, "步骤需要人工处理")
		e.deps.Notify(ctx, run.TenantID, "sop_approval", title, reason, run.ID)
	}
	e.audit(ctx, run, "等待人工处理", firstNonEmpty(nodeDisplayName(run, node.ID), node.ID)+"："+reason)
}

// hashFile 计算文件内容的 sha256（小写十六进制）。
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
