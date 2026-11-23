// answer.go 是「答复挂起步骤」的引擎侧入口：HITL 挂起（pending_input）之后，
// 人工答复要能被原会话接上继续跑，而不是从头再来一遍。
//
// 两条路径：
//   - AnswerPrompt：用户回答了智能体的追问。答复先写进挂起提示（hitl Broker，
//     可选依赖）并追加到运行的人工上下文（任务书会带上），节点由 pending_input
//     交还 running，Advance 在原会话里续跑（ResumeSessionID）；
//   - ResumeNode：没有可答复的挂起快照（提示已过期/被回收），退化为「保留会话
//     重新执行」——会话还带着之前的上下文，代价是多一轮模型调用，但不是从零。
//
// 幂等：节点状态用 CAS 迁移，重复答复/重复续跑只有第一个生效。
package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// PromptAnswerer 把人工答复写进挂起提示（真实实现 = hitl.Broker 适配，T21 接线）。
//
// 引擎只声明这一个方法：答复要在恢复执行之前落库，否则续跑拿到的是未答复的快照。
type PromptAnswerer interface {
	Answer(ctx context.Context, tenantID uint64, sessionID, answer string) error
}

// answerContextSource 是答复写进人工上下文的来源标记（任务书据此标注「人工补充」）。
const answerSource = "answer"

// AnswerPrompt 回答一个等待补充信息的节点：答复落库 → 写人工上下文 → 交还推进。
func (e *Engine) AnswerPrompt(
	ctx context.Context, tenantID uint64, runID, nodeID, answer string,
) error {
	text := strings.TrimSpace(answer)
	if text == "" {
		return fmt.Errorf("答复内容不能为空")
	}
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
	if nr.Status != types.SopNodePendingInput {
		return fmt.Errorf("该步骤当前不在等待补充信息（当前状态：%s）", nr.Status)
	}
	// 先落挂起提示再改状态：答复写失败时节点仍是 pending_input，用户可重试。
	if e.deps.Prompts != nil && strings.TrimSpace(nr.SessionID) != "" {
		if err := e.deps.Prompts.Answer(ctx, run.TenantID, nr.SessionID, text); err != nil {
			return fmt.Errorf("提交答复失败：%w", err)
		}
	}
	if err := e.AddHumanContext(ctx, run.TenantID, run.ID, text, answerSource, nr.NodeID); err != nil {
		return err
	}
	// CAS：并发答复只有一个能把节点从 pending_input 交还 running。
	won, err := e.deps.Runs.TransitionNodeRun(ctx, run.TenantID, run.ID, nr.NodeID,
		types.SopNodePendingInput, types.SopNodeRunning, map[string]any{
			"session_id": nr.SessionID, "resume_session_id": nr.SessionID, "gate_reason": "",
		})
	if err != nil {
		return fmt.Errorf("写入节点状态失败：%w", err)
	}
	if !won {
		return fmt.Errorf("该步骤的答复已被处理，请刷新后再看")
	}
	e.markResume(run.ID, nr.NodeID)
	e.recomputeRunWaitStatus(ctx, run)
	e.ensureRunning(run.ID)
	return nil
}

// ResumeNode 在没有挂起快照时续跑一个等待补充信息的节点：保留会话直接重跑。
//
// 场景：挂起提示已过期或被回收，但用户仍想让这一步继续。节点回 pending（Attempt
// 不清零，仍受重试预算约束），carry_over=true 的节点保留会话，Advance 重新执行。
func (e *Engine) ResumeNode(ctx context.Context, tenantID uint64, runID, nodeID string) error {
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
	if nr.Status != types.SopNodePendingInput {
		return fmt.Errorf("该步骤当前不在等待补充信息（当前状态：%s）", nr.Status)
	}
	node := nodeByID(&run.WorkflowSnapshot, nr.NodeID)
	patch := map[string]any{
		"feedback": "", "gate_result": "", "gate_reason": "", "finished_at": nil,
	}
	if node != nil && node.CarryOver {
		// 保留会话：新一轮沿用之前的上下文；不设 ResumeSessionID（没有快照可续）。
		patch["resume_session_id"] = ""
	} else {
		patch["session_id"] = ""
		patch["resume_session_id"] = ""
	}
	if _, err := e.deps.Runs.TransitionNodeRun(ctx, run.TenantID, run.ID, nr.NodeID,
		types.SopNodePendingInput, types.SopNodePending, patch); err != nil {
		return fmt.Errorf("写入节点状态失败：%w", err)
	}
	e.recomputeRunWaitStatus(ctx, run)
	e.ensureRunning(run.ID)
	return nil
}

// ---- 续跑登记（进程内）----

// markResume 登记「答复已落库、等 Advance 续跑」的节点。
//
// 不落库的原因：节点已是 running 且会话仍在库里，进程崩溃后由租约回收成 failed
// 再重试；把「待续跑」写进节点状态会占用 8 态里没有语义的空位。
func (e *Engine) markResume(runID, nodeID string) {
	if runID == "" || nodeID == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.resumes == nil {
		e.resumes = map[string]map[string]bool{}
	}
	set := e.resumes[runID]
	if set == nil {
		set = map[string]bool{}
		e.resumes[runID] = set
	}
	set[nodeID] = true
}

// takeResumes 取走某运行的续跑登记（取后即删，一次答复只续跑一次）。
func (e *Engine) takeResumes(runID string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	set := e.resumes[runID]
	if len(set) == 0 {
		return nil
	}
	delete(e.resumes, runID)
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	return out
}

// clearResumes 丢弃某运行的续跑登记（运行取消/终态时调用）。
func (e *Engine) clearResumes(runID string) {
	e.mu.Lock()
	delete(e.resumes, runID)
	e.mu.Unlock()
}
