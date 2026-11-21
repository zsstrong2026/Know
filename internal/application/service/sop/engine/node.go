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
func (e *Engine) Advance(ctx context.Context, runID string) (bool, error) {
	run, err := e.deps.Runs.GetRun(ctx, 0, runID)
	if err != nil {
		return false, err
	}
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
			// 子流程编排由 T15（sop_node.go）交付；在此之前明确报错而不是空转，
			// 避免运行无限续租却不前进。
			return true, fmt.Errorf("步骤「%s」是子流程步骤：子流程推进尚未接入", displayName(&node))
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

// settleNoReady 处理「本轮没有就绪节点」：还在等执行/人工就退避，全部终态则收口成功。
func (e *Engine) settleNoReady(ctx context.Context, run *types.SopRun, nrs []types.SopNodeRun) (bool, error) {
	allTerminal := true
	for i := range nrs {
		switch nrs[i].Status {
		case types.SopNodeSucceeded, types.SopNodeSkipped, types.SopNodeFailed, types.SopNodeCancelled:
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

// executeNode 执行一个 agent/script 节点：渲染任务书 → 物化上游产物 → 调用执行器
// → 收集产物 → 门禁裁定 → 检查点。错误只落在本条节点记录上。
func (e *Engine) executeNode(
	ctx context.Context, run *types.SopRun, node *types.SopNode, nr *types.SopNodeRun, ws *Workspace,
) error {
	nr.Attempt++
	nr.Status = types.SopNodeRunning
	started := time.Now()
	nr.StartedAt = &started
	nr.FinishedAt = nil
	if err := e.deps.Runs.UpdateNodeRun(ctx, nr); err != nil {
		return err
	}

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

	prompt := RenderTaskBook(TaskBookInput{
		Node:                   *node,
		Item:                   e.loadItem(ctx, run),
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
		OutDir:                 ws.OutDir,
	})

	// 物化上游产物到 inputs/<node>/：只取直接上游在本运行内的产物。
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
	if err := MaterializeInputs(ws, inputs, ws.Root); err != nil {
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
		RunID:           run.ID,
		NodeRunID:       nr.ID,
		NodeID:          node.ID,
		NodeName:        node.Name,
		Prompt:          prompt,
		SystemPrompt:    RenderAgentSystem(resolved.Role, resolved.Skills, node.ExtraRules, ""),
		WorkspaceRoot:   ws.Root,
		InputsDir:       slashJoin(ws.InputsDir, sanitizeSegment(node.ID)),
		OutDir:          ws.OutDir,
		Feedback:        nr.Feedback,
		Attempt:         nr.Attempt,
		ResumeSessionID: nr.ResumeSessionID,
		Timeout:         nodeTimeout(node),
		Env:             node.CredentialEnv,
		Script:          node.Script,
		Model:           model,
		Credential:      resolved.Credential,
		Role:            resolved.Role,
		MCPServers:      resolved.MCPServers,
		Skills:          resolved.Skills,
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
	ws.CheckpointBestEffort(ctx, fmt.Sprintf("node %s attempt %d %s", node.ID, nr.Attempt, nr.GateResult))
	return nil
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
	return e.deps.Runs.UpdateNodeRun(ctx, nr)
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
			// 打回目标缺失是定义错误：退化为自动重跑本条，不让运行卡死。
			nr.Status = types.SopNodePending
			nr.Feedback = reason
			return e.deps.Runs.UpdateNodeRun(ctx, nr)
		}
		ids := []string{target}
		for id := range Descendants(&run.WorkflowSnapshot, target) {
			ids = append(ids, id)
		}
		if err := e.deps.Runs.ResetNodes(ctx, run.ID, ids); err != nil {
			return err
		}
		tnr, err := e.deps.Runs.GetNodeRun(ctx, run.TenantID, run.ID, target)
		if err != nil {
			return err
		}
		tnr.Feedback = reason
		tnr.ReworkCount++
		if !node.CarryOver {
			tnr.SessionID, tnr.ResumeSessionID = "", ""
		}
		if err := e.deps.Runs.UpdateNodeRun(ctx, tnr); err != nil {
			return err
		}
		// 打回方自身也回到 pending，等上游重新产出后自然重新就绪。
		nr.Status = types.SopNodePending
		nr.Feedback = reason
		if err := e.deps.Runs.UpdateNodeRun(ctx, nr); err != nil {
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
