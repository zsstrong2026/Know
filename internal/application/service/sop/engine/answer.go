// answer.go 是「答复挂起步骤」的引擎侧入口：HITL 挂起（pending_input）之后，
// 人工答复要能被原会话接上继续跑，而不是从头再来一遍。
//
// 两条路径：
//   - AnswerPrompt：用户回答了智能体的追问。答复先写进挂起提示（hitl Broker，
//     可选依赖）并追加到运行的人工上下文（任务书会带上），节点回 pending 并保留
//     会话，Advance 按正常就绪路径用原会话续跑（ResumeSessionID 取挂起快照）；
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

// PromptSuspensionLocator 按会话定位挂起提示 ID（真实实现 = hitl.Suspensions /
// PromptRepository 适配，T21 接线；nil 时答复按会话投递）。
type PromptSuspensionLocator interface {
	PromptIDBySession(ctx context.Context, sessionID string) (string, error)
}

// PromptAnswerer 把人工答复写进挂起提示（真实实现 = hitl.Broker.Resolve 适配，
// T21 接线）。
//
// 引擎只声明这一个方法：答复要在恢复执行之前落库，否则续跑拿到的是未答复的快照。
type PromptAnswerer interface {
	Answer(ctx context.Context, req PromptAnswer) error
}

// PromptAnswer 一次人工答复：定位到挂起提示所需的全部线索。
//
// PromptID 可能为空（没有挂起快照或跨进程定位不到），此时适配层按 SessionID
// 取该会话尚未答复的挂起提示；两者都定位不到时应返回错误，不能静默丢弃答复。
type PromptAnswer struct {
	TenantID  uint64
	SessionID string
	PromptID  string
	Text      string
}

// answerContextSource 是答复写进人工上下文的来源标记（任务书据此标注「人工补充」）。
const answerSource = "answer"

// AnswerPrompt 回答一个等待补充信息的节点：答复落库 → 写人工上下文 → 节点回 pending。
//
// 为什么直接回 pending 而不是「先 running、再由 Advance 打回」：那是进程内状态，
// 进程重启或推进协程起不来时节点会永久停在 running（租约被心跳续着，扫不回收）。
// pending + 保留 resume_session_id 是库里的状态：Advance 按正常就绪路径重跑时，
// 执行器凭 resume_session_id 取挂起快照续会话，崩溃后重启也一样成立。
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
		req := PromptAnswer{TenantID: run.TenantID, SessionID: nr.SessionID, Text: text}
		if e.deps.SuspensionPrompts != nil {
			id, lookupErr := e.deps.SuspensionPrompts.PromptIDBySession(ctx, nr.SessionID)
			if lookupErr != nil {
				return fmt.Errorf("定位挂起提示失败：%w", lookupErr)
			}
			req.PromptID = id
		}
		if err := e.deps.Prompts.Answer(ctx, req); err != nil {
			return fmt.Errorf("提交答复失败：%w", err)
		}
	}
	if err := e.AddHumanContext(ctx, run.TenantID, run.ID, text, answerSource, nr.NodeID); err != nil {
		return err
	}
	// CAS：并发答复只有一个能把它从 pending_input 放走。
	//
	// attempt 归零：答复是用户给出的新信息，下一轮是「知情后的第一次尝试」；
	// 不归零的话，用户答三次就会把节点推到重试耗尽，等于把人工介入算成机器的失败。
	nr.SessionID, nr.ResumeSessionID = resumeSessions(nr)
	won, err := e.deps.Runs.TransitionNodeRun(ctx, run.TenantID, run.ID, nr.NodeID,
		types.SopNodePendingInput, types.SopNodePending, map[string]any{
			"attempt": 0, "session_id": nr.SessionID, "resume_session_id": nr.ResumeSessionID,
			"gate_result": "", "gate_reason": "", "finished_at": nil,
		})
	if err != nil {
		return fmt.Errorf("写入节点状态失败：%w", err)
	}
	if !won {
		return fmt.Errorf("该步骤的答复已被处理，请刷新后再看")
	}
	e.recomputeRunWaitStatus(ctx, run)
	e.ensureRunning(run.ID)
	return nil
}

// resumeSessions 决定续跑用哪个会话：沿用挂起前的会话，并把同一个 id 写进
// resume_session_id（执行器据此取挂起快照）。
//
// 这里不受 carry_over 影响：挂起快照是绑在会话上的，换会话就等于丢掉用户刚回答
// 的那个问题。carry_over 管的是「返工是否复用上下文」，与「答复必须回到原会话」
// 是两件事。
func resumeSessions(nr *types.SopNodeRun) (string, string) {
	session := strings.TrimSpace(nr.SessionID)
	if session == "" {
		return "", ""
	}
	return session, session
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
		"attempt": 0, "feedback": "", "gate_result": "", "gate_reason": "", "finished_at": nil,
	}
	if node != nil && node.CarryOver {
		// 保留会话：新一轮沿用之前的上下文；不设 ResumeSessionID（没有快照可续）。
		patch["resume_session_id"] = ""
	} else {
		patch["session_id"] = ""
		patch["resume_session_id"] = ""
	}
	won, err := e.deps.Runs.TransitionNodeRun(ctx, run.TenantID, run.ID, nr.NodeID,
		types.SopNodePendingInput, types.SopNodePending, patch)
	if err != nil {
		return fmt.Errorf("写入节点状态失败：%w", err)
	}
	if !won {
		return fmt.Errorf("该步骤已被处理，请刷新后再看")
	}
	e.recomputeRunWaitStatus(ctx, run)
	e.ensureRunning(run.ID)
	return nil
}
