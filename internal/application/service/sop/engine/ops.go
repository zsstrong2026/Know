// ops.go 是运行的人工操作面：审批三操作（通过/打回上游/补充信息重跑）、
// 终止、整体重试、暂停/恢复/取消、人工上下文追加，以及审批超时清扫。
//
// 三条不可越界的口径：
//  1. 节点录取走 CAS（TransitionNodeRun，WHERE status='waiting_approval'）：并发审批
//     只有一个能改到节点，落败方直接返回「已被处理」且不写留痕文件；
//  2. 录取成功后再落盘 .sop/decisions/<node>-<unix>-<nonce>.md（人/时间/结论/意见 +
//     产物指纹），留痕写失败则尽力把节点状态退回，绝不留下「状态变了但没留痕」；
//     留痕文件先写同目录临时文件再 os.Rename，避免读到半截内容；
//  3. 终态运行（succeeded/failed/cancelled）拒绝一切会改状态的操作，不允许复活；
//  4. 人工打回重置自动重试预算（Attempt=0），但保留返工计数（ReworkCount 兜底）；
//  5. 取消不是失败：Cancel 只置标记并打断在途推进，收口统一走 finishCancelled。
//
// 审批超时的默认策略是「无限等待」（spec §6）：只有节点显式配置了
// ApprovalTimeoutHours>0 才参与清扫，Action=fail 自动终止，Action=notify（默认）
// 只提醒一次（以 run.HumanContext 里的 system_reminder 标记去重）。
package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// ---- 审批决定常量 ----

const (
	decisionApprove   = "approve"
	decisionReject    = "reject"
	decisionRework    = "rework"
	decisionTerminate = "terminate"
)

// decisionLabel 把决定键渲染成给用户看的中文（留痕正文用）。
func decisionLabel(decision string) string {
	switch decision {
	case decisionApprove:
		return "通过"
	case decisionReject:
		return "打回"
	case decisionRework:
		return "补充信息重跑"
	case decisionTerminate:
		return "终止"
	default:
		return decision
	}
}

// systemReminderSource 是审批超时提醒的去重标记来源（HumanContext.Source）。
const systemReminderSource = "system_reminder"

// ---- 审批三操作 + 终止 ----

// errNodeAlreadyResolved 是并发审批的落败文案：另一个审批人已把节点改走。
var errNodeAlreadyResolved = fmt.Errorf("该步骤已被处理（可能已由他人通过或打回），请刷新后重试")

// Approve 通过一个等待审批的节点：CAS 录取 → 留痕 → 节点 succeeded/pass → 运行状态重算。
func (e *Engine) Approve(
	ctx context.Context, tenantID uint64, runID, nodeID, comment, approver string,
) error {
	run, _, err := e.loadApprovalTarget(ctx, tenantID, runID, nodeID)
	if err != nil {
		return err
	}
	now := time.Now()
	won, err := e.deps.Runs.TransitionNodeRun(ctx, run.TenantID, run.ID, nodeID,
		types.SopNodeWaitingApproval, types.SopNodeSucceeded, map[string]any{
			"gate_result": types.SopGatePass,
			"gate_reason": comment,
			"error":       "",
			"finished_at": now,
		})
	if err != nil {
		return fmt.Errorf("写入节点状态失败：%w", err)
	}
	if !won {
		return errNodeAlreadyResolved
	}
	if err := e.writeDecision(ctx, run, nodeID, decisionApprove, comment, approver); err != nil {
		// 留痕失败则尽力退回等待审批：宁可让操作失败可重试，也不留「状态变了没留痕」。
		e.revertNodeTransition(ctx, run, nodeID, types.SopNodeSucceeded, types.SopNodeWaitingApproval, map[string]any{
			"gate_result": "", "gate_reason": "", "finished_at": nil,
		})
		return err
	}
	e.recomputeRunWaitStatus(ctx, run)
	e.businessActivity(ctx, run, decisionApprove, approver, comment)
	e.ensureRunning(runID)
	return nil
}

// Reject 打回一个等待审批的节点。
//
// 目标优先级：调用方给的 rejectTo > 节点模板 RejectTo。目标必须先通过校验（存在且
// 是当前节点或它的上游），校验与 CAS 都通过后才写留痕；目标非空时重置目标及其
// 下游（含审批节点自身），目标反馈写入 comment、返工计数自增；超过目标节点
// MaxReworks 则目标与运行一起转为等待人工，不再自动重跑。目标为空时：
// approval 节点要求显式指定打回目标（白话错误），其它类型（agent 重试预算耗尽
// 被打回）只要当前节点重跑即可，并重置自动重试预算。
func (e *Engine) Reject(
	ctx context.Context, tenantID uint64, runID, nodeID, comment, approver, rejectTo string,
) error {
	run, nr, err := e.loadApprovalTarget(ctx, tenantID, runID, nodeID)
	if err != nil {
		return err
	}
	node := nodeByID(&run.WorkflowSnapshot, nodeID)
	target := strings.TrimSpace(rejectTo)
	if target == "" && node != nil {
		target = strings.TrimSpace(node.RejectTo)
	}
	// 先校验再留痕：留痕一旦落盘就是「已发生的事实」，不能为一次被拒绝的操作留下
	// 一份写着 reject 的决定文件（打回目标写错同样属于被拒绝的操作）。
	if target == "" && node != nil && node.Type == types.SopNodeTypeApproval {
		return fmt.Errorf("请选择打回哪一步")
	}
	if target != "" {
		if err := validateRejectTarget(&run.WorkflowSnapshot, nodeID, target); err != nil {
			return err
		}
	}

	// CAS 录取：只有真正把审批节点从 waiting_approval 改走的一方才能落留痕。
	patch := map[string]any{
		"attempt": 0, "feedback": comment,
		"gate_result": "", "gate_reason": "", "finished_at": nil,
	}
	if target == "" {
		patch["rework_count"] = nr.ReworkCount + 1
	}
	won, err := e.deps.Runs.TransitionNodeRun(ctx, run.TenantID, run.ID, nodeID,
		types.SopNodeWaitingApproval, types.SopNodePending, patch)
	if err != nil {
		return fmt.Errorf("写入节点状态失败：%w", err)
	}
	if !won {
		return errNodeAlreadyResolved
	}
	if err := e.writeDecision(ctx, run, nodeID, decisionReject, comment, approver); err != nil {
		e.revertNodeTransition(ctx, run, nodeID, types.SopNodePending, types.SopNodeWaitingApproval, nil)
		return err
	}

	if target == "" {
		e.recomputeRunWaitStatus(ctx, run)
		e.businessActivity(ctx, run, decisionReject, approver, comment)
		e.ensureRunning(runID)
		return nil
	}

	// 重置目标 + 其后代 + 审批节点自身：打回后审批节点要重新排队等待。
	ids := []string{target}
	seen := map[string]bool{target: true}
	for id := range Descendants(&run.WorkflowSnapshot, target) {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if !seen[nodeID] {
		ids = append(ids, nodeID)
	}
	if err := e.deps.Runs.ResetNodes(ctx, run.ID, ids, carryOverOf(&run.WorkflowSnapshot)); err != nil {
		return fmt.Errorf("重置节点失败：%w", err)
	}
	tnr, err := e.deps.Runs.GetNodeRun(ctx, run.TenantID, run.ID, target)
	if err != nil {
		return fmt.Errorf("读取打回目标节点失败：%w", err)
	}
	tnr.Feedback = comment
	tnr.ReworkCount++
	tnr.Attempt = 0 // ResetNodes 已归零，显式带回以钉住「打回不吃重试预算」
	maxReworks := 0
	if tn := nodeByID(&run.WorkflowSnapshot, target); tn != nil {
		maxReworks = tn.MaxReworks
	}
	if maxReworks <= 0 {
		maxReworks = types.SopDefaultMaxReworks
	}
	exhausted := tnr.ReworkCount >= maxReworks
	if exhausted {
		// 打回次数用尽：目标不再自动重跑，转人工（run 由状态重算停在 waiting）。
		tnr.Status = types.SopNodeWaitingApproval
		tnr.GateReason = "打回次数已达上限，请人工决定"
		tnr.FinishedAt = timePtr(time.Now())
	}
	if err := e.deps.Runs.UpdateNodeRun(ctx, tnr); err != nil {
		return fmt.Errorf("写入打回目标状态失败：%w", err)
	}
	if exhausted {
		notifyNode := nodeByID(&run.WorkflowSnapshot, target)
		if notifyNode == nil {
			notifyNode = &types.SopNode{ID: target}
		}
		e.notifyApproval(ctx, run, notifyNode, "打回次数已达上限")
	}
	e.recomputeRunWaitStatus(ctx, run)
	e.businessActivity(ctx, run, decisionReject, approver, comment)
	e.ensureRunning(runID)
	return nil
}

// ReworkCurrent 让当前节点携补充信息重跑（spec §6 第二操作）：
// 补充信息同时进人工上下文（下一轮任务书可见）与节点反馈，自动重试预算归零。
func (e *Engine) ReworkCurrent(
	ctx context.Context, tenantID uint64, runID, nodeID, comment, approver string,
) error {
	run, err := e.loadRun(ctx, tenantID, runID)
	if err != nil {
		return err
	}
	if err := guardMutableRun(run); err != nil {
		return err
	}
	nr, err := e.loadNodeRun(ctx, run, nodeID)
	if err != nil {
		return err
	}
	switch nr.Status {
	case types.SopNodeWaitingApproval, types.SopNodePendingInput:
	default:
		return fmt.Errorf("该步骤当前不在等待人工处理（当前状态：%s）", nr.Status)
	}
	won, err := e.deps.Runs.TransitionNodeRun(ctx, run.TenantID, run.ID, nodeID,
		nr.Status, types.SopNodePending, map[string]any{
			"attempt": 0, "feedback": comment,
			"gate_result": "", "gate_reason": "", "finished_at": nil,
		})
	if err != nil {
		return fmt.Errorf("写入节点状态失败：%w", err)
	}
	if !won {
		return errNodeAlreadyResolved
	}
	if err := e.writeDecision(ctx, run, nodeID, decisionRework, comment, approver); err != nil {
		e.revertNodeTransition(ctx, run, nodeID, types.SopNodePending, nr.Status, nil)
		return err
	}
	// 人工上下文写库基于新鲜行：推进期间运行状态可能已被其它路径改过。
	if fresh, ferr := e.deps.Runs.GetRun(ctx, 0, run.ID); ferr == nil && fresh != nil && fresh.ID != "" {
		run = fresh
	}
	run.HumanContext = append(run.HumanContext, types.SopHumanContextEntry{
		At: time.Now(), Source: decisionRework, NodeID: nodeID, Text: comment,
	})
	if err := e.deps.Runs.UpdateRun(ctx, run); err != nil {
		return fmt.Errorf("写入人工上下文失败：%w", err)
	}
	e.recomputeRunWaitStatus(ctx, run)
	e.businessActivity(ctx, run, decisionRework, approver, comment)
	e.ensureRunning(runID)
	return nil
}

// Terminate 人工终止一个等待审批的节点：节点 failed、运行 failed 并立即收口。
func (e *Engine) Terminate(
	ctx context.Context, tenantID uint64, runID, nodeID, comment, approver string,
) error {
	run, _, err := e.loadApprovalTarget(ctx, tenantID, runID, nodeID)
	if err != nil {
		return err
	}
	reason := "人工终止"
	if strings.TrimSpace(comment) != "" {
		reason += "：" + comment
	}
	won, err := e.deps.Runs.TransitionNodeRun(ctx, run.TenantID, run.ID, nodeID,
		types.SopNodeWaitingApproval, types.SopNodeFailed, map[string]any{
			"error": reason, "finished_at": time.Now(),
		})
	if err != nil {
		return fmt.Errorf("写入节点状态失败：%w", err)
	}
	if !won {
		return errNodeAlreadyResolved
	}
	if err := e.writeDecision(ctx, run, nodeID, decisionTerminate, comment, approver); err != nil {
		e.revertNodeTransition(ctx, run, nodeID, types.SopNodeFailed, types.SopNodeWaitingApproval, map[string]any{
			"error": "", "finished_at": nil,
		})
		return err
	}
	run.Status = types.SopRunFailed
	run.Error = reason
	if err := e.deps.Runs.UpdateRun(ctx, run); err != nil {
		return fmt.Errorf("写入运行状态失败：%w", err)
	}
	e.businessActivity(ctx, run, decisionTerminate, approver, comment)
	return e.finalizeRun(ctx, run)
}

// ---- 运行级操作 ----

// Retry 整体重试一次已失败/已取消的运行：failed/cancelled 节点重置为 pending、
// 自动重试预算归零，运行回到 queued 由调度器重新认领。
func (e *Engine) Retry(ctx context.Context, tenantID uint64, runID string) error {
	run, err := e.loadRun(ctx, tenantID, runID)
	if err != nil {
		return err
	}
	if run.Status != types.SopRunFailed && run.Status != types.SopRunCancelled {
		return fmt.Errorf("只有失败或已取消的运行可以重试（当前状态：%s）", run.Status)
	}
	nrs, err := e.deps.Runs.ListNodeRuns(ctx, run.TenantID, run.ID)
	if err != nil {
		return fmt.Errorf("读取节点运行记录失败：%w", err)
	}
	var ids []string
	for i := range nrs {
		switch nrs[i].Status {
		case types.SopNodeFailed, types.SopNodeCancelled:
			ids = append(ids, nrs[i].NodeID)
		}
	}
	if err := e.deps.Runs.ResetRunForRetry(ctx, run.TenantID, run.ID, ids); err != nil {
		return fmt.Errorf("重置运行失败：%w", err)
	}
	e.businessActivity(ctx, run, "retry", "", "")
	return nil
}

// Pause 请求暂停（节点边界生效：runLoop 在下一轮推进前落成 paused 并退出）。
func (e *Engine) Pause(ctx context.Context, tenantID uint64, runID string) error {
	run, err := e.loadRun(ctx, tenantID, runID)
	if err != nil {
		return err
	}
	if run.Status != types.SopRunRunning {
		return fmt.Errorf("只有运行中的流程可以暂停（当前状态：%s）", run.Status)
	}
	run.PauseRequested = true
	if err := e.deps.Runs.UpdateRun(ctx, run); err != nil {
		return fmt.Errorf("写入暂停请求失败：%w", err)
	}
	return nil
}

// Resume 恢复一次已暂停的运行：回到 queued 由调度器重新认领推进。
func (e *Engine) Resume(ctx context.Context, tenantID uint64, runID string) error {
	run, err := e.loadRun(ctx, tenantID, runID)
	if err != nil {
		return err
	}
	if run.Status != types.SopRunPaused {
		return fmt.Errorf("只有已暂停的流程可以恢复（当前状态：%s）", run.Status)
	}
	run.Status = types.SopRunQueued
	run.PauseRequested = false
	run.FinishedAt = nil
	if err := e.deps.Runs.UpdateRun(ctx, run); err != nil {
		return fmt.Errorf("写入运行状态失败：%w", err)
	}
	return nil
}

// Cancel 取消一次运行：先置取消标记，active 时打断在途推进（runLoop 收口为
// cancelled）；非 active（queued/paused/waiting 无推进协程）直接收口。
//
// 取消向下传播：本运行派生的子运行（sop 节点的 child run）一并取消，否则父运行
// 收口后子运行仍在调度器里继续跑，白烧 token 且会往父工作区回写产物（父节点已被
// 置 cancelled，导入只会留下孤儿文件）。传播按祖父孙逐层进行，同样受层数上限保护。
//
// 传播失败不回滚父运行的取消：取消失败比「子运行多跑一会儿」更严重，父运行必须
// 收口；子运行残留依靠活跃态扫描/租约过期兜底。
func (e *Engine) Cancel(ctx context.Context, tenantID uint64, runID string) error {
	run, err := e.loadRun(ctx, tenantID, runID)
	if err != nil {
		return err
	}
	if isTerminalRun(run.Status) {
		return fmt.Errorf("运行已结束，无法取消（当前状态：%s）", run.Status)
	}
	run.CancelRequested = true
	if err := e.deps.Runs.UpdateRun(ctx, run); err != nil {
		return fmt.Errorf("写入取消请求失败：%w", err)
	}
	e.cancelChildRuns(ctx, runID, sopChildChainCap)
	if e.isActive(runID) {
		e.CancelRun(runID)
		return nil
	}
	e.finishCancelled(ctx, runID)
	return nil
}

// cancelChildRuns 递归取消某运行派生的全部活跃子运行（depth 为剩余层数）。
//
// 只动活跃态（queued/running/waiting/paused）：终态子运行保留原结论，不让一次取消
// 把已经成功的历史改写成 cancelled。单条子运行处理失败只记日志，不阻断其余子运行
// 与父运行的收口。
func (e *Engine) cancelChildRuns(ctx context.Context, parentRunID string, depth int) {
	if depth <= 0 || parentRunID == "" {
		return
	}
	children, err := e.deps.Runs.ListChildRuns(ctx, parentRunID)
	if err != nil {
		log.Printf("[sop-engine] 运行 %s 取消向下传播读取子运行失败：%v", parentRunID, err)
		return
	}
	for i := range children {
		child := &children[i]
		if !types.IsSopRunActive(child.Status) {
			continue
		}
		child.CancelRequested = true
		if err := e.deps.Runs.UpdateRun(ctx, child); err != nil {
			log.Printf("[sop-engine] 子运行 %s 取消标记写入失败：%v", child.ID, err)
			continue
		}
		e.cancelChildRuns(ctx, child.ID, depth-1)
		if e.isActive(child.ID) {
			e.CancelRun(child.ID)
			continue
		}
		e.finishCancelled(ctx, child.ID)
	}
}

// AddHumanContext 追加一条人工上下文（审批意见/补充信息/系统提醒），执行时注入任务书。
func (e *Engine) AddHumanContext(
	ctx context.Context, tenantID uint64, runID, text, source, nodeID string,
) error {
	run, err := e.loadRun(ctx, tenantID, runID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("补充内容不能为空")
	}
	if strings.TrimSpace(source) == "" {
		source = "manual"
	}
	run.HumanContext = append(run.HumanContext, types.SopHumanContextEntry{
		At: time.Now(), Source: source, NodeID: nodeID, Text: text,
	})
	if err := e.deps.Runs.UpdateRun(ctx, run); err != nil {
		return fmt.Errorf("写入人工上下文失败：%w", err)
	}
	e.businessActivity(ctx, run, "human_context", source, text)
	return nil
}

// ---- 审批超时清扫 ----

// sweepApprovalTimeouts 扫描全部 waiting 运行，对超时的等待审批节点执行配置的策略。
//
// 默认无限等待（spec §6）：节点 ApprovalTimeoutHours<=0 一律跳过。
// Action=fail 走 Terminate（留痕 + 运行失败）；Action=notify（默认）只提醒一次，
// 去重标记写在 run.HumanContext（Source=system_reminder、NodeID=节点）。
// 单条运行失败只记日志，不影响其它运行与本轮调度。
func (e *Engine) sweepApprovalTimeouts(ctx context.Context) {
	runs, err := e.deps.Runs.ListRuns(ctx, 0, types.SopRunFilter{Status: types.SopRunWaiting}, 0, 0)
	if err != nil {
		log.Printf("[sop-engine] 审批超时清扫读取运行失败：%v", err)
		return
	}
	now := time.Now()
	for i := range runs {
		run := &runs[i]
		nrs, err := e.deps.Runs.ListNodeRuns(ctx, run.TenantID, run.ID)
		if err != nil {
			log.Printf("[sop-engine] 运行 %s 审批超时清扫读取节点失败：%v", run.ID, err)
			continue
		}
		for j := range nrs {
			nr := &nrs[j]
			if nr.Status != types.SopNodeWaitingApproval {
				continue
			}
			node := nodeByID(&run.WorkflowSnapshot, nr.NodeID)
			if node == nil || node.ApprovalTimeoutHours <= 0 {
				continue
			}
			hours := time.Duration(node.ApprovalTimeoutHours) * time.Hour
			if now.Before(nr.UpdatedAt.Add(hours)) {
				continue
			}
			if node.ApprovalTimeoutAction == "fail" {
				comment := fmt.Sprintf("审批超时（超过 %d 小时无人处理）", node.ApprovalTimeoutHours)
				if err := e.Terminate(ctx, run.TenantID, run.ID, nr.NodeID, comment, "system"); err != nil {
					log.Printf("[sop-engine] 运行 %s 审批超时自动终止失败：%v", run.ID, err)
				}
				break // 运行已失败，剩余节点不必再看
			}
			e.remindApprovalTimeout(ctx, run, node, nr)
		}
	}
}

// remindApprovalTimeout 发一次审批超时提醒（同一节点已提醒过则跳过）。
func (e *Engine) remindApprovalTimeout(
	ctx context.Context, run *types.SopRun, node *types.SopNode, nr *types.SopNodeRun,
) {
	for _, h := range run.HumanContext {
		if h.Source == systemReminderSource && h.NodeID == nr.NodeID {
			return
		}
	}
	body := fmt.Sprintf("审批已等待超过 %d 小时，请尽快处理", node.ApprovalTimeoutHours)
	if e.deps.Notify != nil {
		e.deps.Notify(ctx, run.TenantID, "sop_approval", firstNonEmpty(node.Name, node.ID), body, run.ID)
	}
	run.HumanContext = append(run.HumanContext, types.SopHumanContextEntry{
		At: time.Now(), Source: systemReminderSource, NodeID: nr.NodeID, Text: body,
	})
	if err := e.deps.Runs.UpdateRun(ctx, run); err != nil {
		log.Printf("[sop-engine] 运行 %s 审批提醒标记写入失败：%v", run.ID, err)
	}
}

// ---- 内部辅助 ----

// loadRun 按租户读取运行，统一把仓储错误转成白话。
func (e *Engine) loadRun(ctx context.Context, tenantID uint64, runID string) (*types.SopRun, error) {
	if strings.TrimSpace(runID) == "" {
		return nil, fmt.Errorf("运行不存在")
	}
	run, err := e.deps.Runs.GetRun(ctx, tenantID, runID)
	if err != nil || run == nil || run.ID == "" {
		return nil, fmt.Errorf("运行不存在或无权访问")
	}
	return run, nil
}

// loadNodeRun 读取运行内的节点记录。
func (e *Engine) loadNodeRun(
	ctx context.Context, run *types.SopRun, nodeID string,
) (*types.SopNodeRun, error) {
	if strings.TrimSpace(nodeID) == "" {
		return nil, fmt.Errorf("请选择步骤")
	}
	nr, err := e.deps.Runs.GetNodeRun(ctx, run.TenantID, run.ID, nodeID)
	if err != nil || nr == nil || nr.ID == "" {
		return nil, fmt.Errorf("步骤不存在：%s", nodeID)
	}
	return nr, nil
}

// loadApprovalTarget 读取运行与节点，要求运行未终态且节点恰好处于等待审批。
func (e *Engine) loadApprovalTarget(
	ctx context.Context, tenantID uint64, runID, nodeID string,
) (*types.SopRun, *types.SopNodeRun, error) {
	run, err := e.loadRun(ctx, tenantID, runID)
	if err != nil {
		return nil, nil, err
	}
	if err := guardMutableRun(run); err != nil {
		return nil, nil, err
	}
	nr, err := e.loadNodeRun(ctx, run, nodeID)
	if err != nil {
		return nil, nil, err
	}
	if nr.Status != types.SopNodeWaitingApproval {
		return nil, nil, fmt.Errorf("该步骤当前不在等待审批（当前状态：%s）", nr.Status)
	}
	return run, nr, nil
}

// guardMutableRun 拒绝在终态运行上做会改状态的操作：已结束（成功/失败/取消）的
// 运行不允许被一条迟到的审批/打回重新拉回 running（那等于复活一条已收口的运行）。
func guardMutableRun(run *types.SopRun) error {
	if run == nil {
		return fmt.Errorf("运行不存在")
	}
	if isTerminalRun(run.Status) {
		return fmt.Errorf("运行已结束，不能再执行该操作（当前状态：%s）", run.Status)
	}
	return nil
}

// validateRejectTarget 校验打回目标：必须存在于流程快照且是当前节点或它的上游。
// 校验放在留痕与状态写入之前——一个写错的目标不该留下任何「已打回」的痕迹。
func validateRejectTarget(def *types.SopDefinition, nodeID, target string) error {
	if nodeByID(def, target) == nil {
		return fmt.Errorf("打回目标不存在：%s", target)
	}
	if target == nodeID || Ancestors(def, nodeID)[target] {
		return nil
	}
	return fmt.Errorf("只能打回到当前步骤或它的上游步骤：%s", target)
}

// revertNodeTransition 在留痕写失败时尽力把节点状态退回（CAS 语义，失败只记日志）。
func (e *Engine) revertNodeTransition(
	ctx context.Context, run *types.SopRun, nodeID, from, to string, patch map[string]any,
) {
	if run == nil {
		return
	}
	if _, err := e.deps.Runs.TransitionNodeRun(ctx, run.TenantID, run.ID, nodeID, from, to, patch); err != nil {
		log.Printf("[sop-engine] 运行 %s 节点 %s 状态回退失败：%v", run.ID, nodeID, err)
	}
}

// recomputeRunWaitStatus 按库里全部节点记录重算运行状态：还有节点在等待人工
// （waiting_approval/pending_input）就是 waiting，否则 running。
//
// 这是人工操作后运行状态的唯一裁决点：通过一个分支时另一个分支仍在等待，运行必须
// 停在 waiting，不能被「先到者」一把拉回 running。已终态的行原样保留（不复活）。
func (e *Engine) recomputeRunWaitStatus(ctx context.Context, run *types.SopRun) {
	if run == nil {
		return
	}
	fresh, err := e.deps.Runs.GetRun(ctx, 0, run.ID)
	if err != nil || fresh == nil || fresh.ID == "" || isTerminalRun(fresh.Status) {
		return
	}
	nrs, err := e.deps.Runs.ListNodeRuns(ctx, fresh.TenantID, fresh.ID)
	if err != nil {
		log.Printf("[sop-engine] 运行 %s 重算状态读取节点失败：%v", fresh.ID, err)
		return
	}
	status := types.SopRunRunning
	for i := range nrs {
		switch nrs[i].Status {
		case types.SopNodeWaitingApproval, types.SopNodePendingInput:
			status = types.SopRunWaiting
		}
		if status == types.SopRunWaiting {
			break
		}
	}
	if fresh.Status == status {
		return
	}
	fresh.Status = status
	if status == types.SopRunRunning {
		fresh.Error = ""
	}
	if err := e.deps.Runs.UpdateRun(ctx, fresh); err != nil {
		log.Printf("[sop-engine] 运行 %s 状态写入失败：%v", fresh.ID, err)
	}
}

// isActive 判断运行是否有本进程的推进协程。
func (e *Engine) isActive(runID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.active[runID]
	return ok
}

// ensureRunning 在没有推进协程时补起一个（审批通过后运行需要继续推进；
// 测试模式 DisableRunLoop=true 下不启动，由测试同步驱动 Advance）。
func (e *Engine) ensureRunning(runID string) {
	if e.deps.DisableRunLoop {
		return
	}
	e.startRunLoop(runID)
}

// businessActivity 往事项活动流写一条业务动作（审批/打回/重跑/终止/留言）。
// 事项为空或不存在时静默跳过；写失败只记日志，不反向影响运行操作。
func (e *Engine) businessActivity(ctx context.Context, run *types.SopRun, action, actor, detail string) {
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
		Kind:     types.SopActivityKindBusiness,
		Actor:    actor,
		Action:   action,
		Detail:   detail,
	})
	if err != nil {
		log.Printf("[sop-engine] 运行 %s 活动写入失败：%v", run.ID, err)
	}
}

// opsWorkspace 取运行工作区：优先用运行已回写的 WorkspaceDir；尚未起步的运行
// （如冷启动后直接审批）按标准布局准备一份，保证留痕总落在运行根之下。
func (e *Engine) opsWorkspace(run *types.SopRun) (*Workspace, error) {
	if run != nil && strings.TrimSpace(run.WorkspaceDir) != "" {
		return &Workspace{Root: filepath.ToSlash(run.WorkspaceDir)}, nil
	}
	if run == nil || strings.TrimSpace(e.deps.WorkspaceRoot) == "" {
		return nil, fmt.Errorf("运行工作区尚未就绪")
	}
	return PrepareWorkspace(e.deps.WorkspaceRoot, run.ID)
}

// writeDecision 把一次审批决定落盘为不可篡改的留痕文件并打检查点：
// .sop/decisions/<node>-<unix>-<nonce>.md（人/决定/意见/时间 + 产物指纹段）。
//
// 两步保证「不留半截文件、不撞名」：
//  1. 文件名带 4 字节随机 nonce：同一节点同一秒内的两次决定（并发审批/重试）不会
//     互相覆盖，并发测试也能用「恰好一个文件」当判据；
//  2. 先写同目录临时文件再 os.Rename（同盘 rename 原子）：读者要么看不到文件，
//     要么看到完整正文。
func (e *Engine) writeDecision(
	ctx context.Context, run *types.SopRun, nodeID, decision, comment, approver string,
) error {
	ws, err := e.opsWorkspace(run)
	if err != nil {
		return err
	}
	dir := filepath.Join(ws.Root, ".sop", "decisions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建审批留痕目录失败：%w", err)
	}
	name := fmt.Sprintf("%s-%d-%s.md", sanitizeSegment(nodeID), time.Now().Unix(), decisionNonce())
	body := e.decisionBody(ctx, run, nodeID, decision, comment, approver)
	tmp, err := os.CreateTemp(dir, ".decision-*.tmp")
	if err != nil {
		return fmt.Errorf("写入审批留痕失败：%w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		// rename 成功后路径已不存在；失败/中途退出时清掉残留临时文件。
		_ = os.Remove(tmpName)
	}()
	if _, err := tmp.WriteString(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("写入审批留痕失败：%w", err)
	}
	if err := tmp.Chmod(0o644); err != nil { // CreateTemp 默认 0600，留痕文件按 0644 与旧口径一致
		_ = tmp.Close()
		return fmt.Errorf("写入审批留痕失败：%w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("写入审批留痕失败：%w", err)
	}
	if err := os.Rename(tmpName, filepath.Join(dir, name)); err != nil {
		return fmt.Errorf("写入审批留痕失败：%w", err)
	}
	ws.CheckpointBestEffort(ctx, fmt.Sprintf("decision %s %s", nodeID, decision))
	return nil
}

// decisionNonce 返回一个短随机串（十六进制），只用于让留痕文件名在同秒内唯一。
func decisionNonce() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// 随机源不可用时退化为纳秒时间戳：仍然比纯秒级粒度更能避免撞名。
		return fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff)
	}
	return hex.EncodeToString(b[:])
}

// decisionBody 渲染留痕正文：标题/审批人/决定/时间/意见 + 产物指纹（sha256）。
func (e *Engine) decisionBody(
	ctx context.Context, run *types.SopRun, nodeID, decision, comment, approver string,
) string {
	now := time.Now()
	var b strings.Builder
	fmt.Fprintf(&b, "# 审批决定：%s\n\n", displayName(nodeByID(&run.WorkflowSnapshot, nodeID)))
	fmt.Fprintf(&b, "- 运行：%s\n", run.ID)
	fmt.Fprintf(&b, "- 步骤：%s\n", nodeID)
	fmt.Fprintf(&b, "- 审批人：%s\n", firstNonEmpty(approver, "（未署名）"))
	fmt.Fprintf(&b, "- 决定：%s（%s）\n", decisionLabel(decision), decision)
	fmt.Fprintf(&b, "- 时间：%s\n", now.Format("2006-01-02 15:04:05 -07:00"))
	fmt.Fprintf(&b, "- 意见：%s\n\n", firstNonEmpty(comment, "（无）"))
	b.WriteString("## 产物指纹（sha256）\n\n")
	lines := e.artifactFingerprints(ctx, run, nodeID)
	if len(lines) == 0 {
		b.WriteString("- （本次步骤没有登记产物）\n")
	}
	for _, line := range lines {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// artifactFingerprints 取该节点最新一次尝试的产物指纹行（`- name path sha256:hash`）。
func (e *Engine) artifactFingerprints(ctx context.Context, run *types.SopRun, nodeID string) []string {
	arts, err := e.deps.Runs.ListArtifacts(ctx, run.TenantID, run.ID)
	if err != nil {
		log.Printf("[sop-engine] 运行 %s 审批留痕读取产物失败：%v", run.ID, err)
		return nil
	}
	var mine []types.SopArtifact
	for _, a := range arts {
		if a.NodeID == nodeID {
			mine = append(mine, a)
		}
	}
	latest := latestArtifacts(mine)
	lines := make([]string, 0, len(latest))
	for _, a := range latest {
		lines = append(lines, fmt.Sprintf("- %s %s sha256:%s", a.Name, a.Path, a.SHA256))
	}
	return lines
}

// nodeByID 在快照里找节点定义（找不到返回 nil）。
func nodeByID(def *types.SopDefinition, nodeID string) *types.SopNode {
	if def == nil || nodeID == "" {
		return nil
	}
	for i := range def.Nodes {
		if def.Nodes[i].ID == nodeID {
			return &def.Nodes[i]
		}
	}
	return nil
}

// carryOverOf 收集定义里开了 carry_over 的节点：重置时这些节点的 session 要保留
// （返工续跑靠它），其余节点必须清掉旧会话。
func carryOverOf(def *types.SopDefinition) map[string]bool {
	out := map[string]bool{}
	if def == nil {
		return out
	}
	for i := range def.Nodes {
		if def.Nodes[i].CarryOver {
			out[def.Nodes[i].ID] = true
		}
	}
	return out
}

// timePtr 返回时间指针（FinishedAt 等可选时间字段用）。
func timePtr(t time.Time) *time.Time { return &t }
