// sop_node.go 是子流程（sop 类型节点）的编排：启动子运行、等待子运行终态、
// 把子运行产物导入父运行工作区。
//
// 两段职责：
//   - startChildRun：节点第一次就绪时按子模板的已发布 revision 建一条 child run
//     （trigger_kind=child_run），子 run 由调度器按同一套租约机制正常抢占推进；
//   - reconcileChildRun：每轮 Advance 对「ChildRunID 非空且节点 running」的节点
//     回看子 run——未终态就继续等，成功则导入产物并让节点成功，失败/取消按节点的
//     OnFailure 策略落实（与普通节点共用 handleFailure）。
//
// 防递归：发布校验（T9 ValidateDefinition）已拒绝模板自引用，运行期这里再守一层
// （模板 id 相同即拒），避免历史数据或跨租户内置模板绕过发布校验形成自调用死循环。
//
// 产物路径口径：子 run 的产物路径是「子 run 根」口径（out/<子节点>/x），导入父
// 工作区时保留同一相对路径（spec §3.4「子 run 产物按同路径回传父工作区」）。下游
// 物化按父 run 的产物行读取，与普通节点产物完全同构。
package engine

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// startChildRun 为一个就绪的 sop 节点创建子运行并让节点进入 running。
//
// 子模板必须已发布且带发布 revision：运行一律引用冻结快照，不读活模板（spec §3.3）。
// 校验失败（未发布/找不到/自引用）把节点与运行一起置失败——这类问题重试也没用，
// 让用户先修模板更划算；仓储错误原样返回，由 runLoop 落 failed。
func (e *Engine) startChildRun(
	ctx context.Context, run *types.SopRun, node *types.SopNode, nr *types.SopNodeRun,
) error {
	childTplID := strings.TrimSpace(node.ChildTemplateID)
	if childTplID == "" {
		return e.failChildNode(ctx, run, nr, "子流程步骤没有选择子流程模板")
	}
	if run.TemplateID != "" && childTplID == run.TemplateID {
		return e.failChildNode(ctx, run, nr, "子流程不能递归引用自身")
	}
	tpl, err := e.deps.Templates.GetByID(ctx, run.TenantID, childTplID)
	if err != nil || tpl == nil || tpl.ID == "" {
		return e.failChildNode(ctx, run, nr, "子流程模板不存在或无权访问")
	}
	if tpl.Status != types.SopTemplateStatusPublished || strings.TrimSpace(tpl.PublishedRevisionID) == "" {
		return e.failChildNode(ctx, run, nr, "子流程模板尚未发布")
	}
	rev, err := e.deps.Templates.GetRevision(ctx, run.TenantID, tpl.PublishedRevisionID)
	if err != nil || rev == nil || rev.ID == "" {
		return e.failChildNode(ctx, run, nr, "子流程模板的发布版本不可用，请重新发布后再试")
	}
	// 运行期递归守卫：子模板与父运行同源即拒（与上面的模板 id 判断互为兜底）。
	if run.TemplateID != "" && rev.TemplateID == run.TemplateID {
		return e.failChildNode(ctx, run, nr, "子流程不能递归引用自身")
	}

	child := &types.SopRun{
		TenantID:         run.TenantID,
		TemplateID:       tpl.ID,
		RevisionID:       rev.ID,
		WorkflowSnapshot: rev.Definition,
		ProjectID:        run.ProjectID,
		ItemID:           run.ItemID,
		ParentRunID:      run.ID,
		TriggerKind:      types.SopRunTriggerChildRun,
		TriggerRefID:     node.ID,
		Status:           types.SopRunQueued,
		Input:            run.Input,
	}
	if err := e.deps.Runs.CreateRun(ctx, child); err != nil {
		return fmt.Errorf("创建子流程运行失败：%w", err)
	}

	nr.Attempt++
	nr.ChildRunID = child.ID
	nr.Status = types.SopNodeRunning
	nr.Error = ""
	nr.GateResult = ""
	nr.GateReason = ""
	now := time.Now()
	nr.StartedAt = &now
	nr.FinishedAt = nil
	if err := e.deps.Runs.UpdateNodeRun(ctx, nr); err != nil {
		return err
	}
	e.audit(ctx, run, "启动子流程",
		fmt.Sprintf("%s：%s（子运行 %s）", displayName(node), tpl.Name, child.ID))
	return nil
}

// reconcileChildRuns 对本轮快照里「ChildRunID 非空且 running」的子流程节点逐个对账，
// 返回本轮是否有节点因此推进。
func (e *Engine) reconcileChildRuns(
	ctx context.Context, run *types.SopRun, byID map[string]*types.SopNodeRun,
) (bool, error) {
	progressed := false
	for i := range run.WorkflowSnapshot.Nodes {
		node := &run.WorkflowSnapshot.Nodes[i]
		if node.Type != types.SopNodeTypeSOP {
			continue
		}
		nr := byID[node.ID]
		if nr == nil || strings.TrimSpace(nr.ChildRunID) == "" || nr.Status != types.SopNodeRunning {
			continue
		}
		moved, err := e.reconcileChildRun(ctx, run, node, nr)
		if err != nil {
			return progressed, err
		}
		if moved {
			progressed = true
		}
	}
	return progressed, nil
}

// reconcileChildRun 回看一个子运行的终态并据此推进父节点，返回「节点是否已推进」。
//
// 子运行仍在 queued/running/waiting/paused 时返回 (false, nil)，runLoop 会退避轮询；
// 成功导入产物、失败按 OnFailure 策略落实。子 run 读不到属于数据不一致，
// 返回错误让运行收口，避免无限空转。
func (e *Engine) reconcileChildRun(
	ctx context.Context, run *types.SopRun, node *types.SopNode, nr *types.SopNodeRun,
) (bool, error) {
	child, err := e.deps.Runs.GetRun(ctx, 0, nr.ChildRunID)
	if err != nil || child == nil || child.ID == "" {
		return false, fmt.Errorf("读取子流程运行失败：%w", err)
	}
	if run.TemplateID != "" && child.TemplateID == run.TemplateID {
		return true, e.failChildNode(ctx, run, nr, "子流程不能递归引用自身")
	}
	switch child.Status {
	case types.SopRunSucceeded:
		return true, e.completeChildRun(ctx, run, node, nr, child)
	case types.SopRunFailed, types.SopRunCancelled:
		reason := "子流程执行失败：" + firstNonEmpty(
			strings.TrimSpace(child.Error), "子流程未成功完成（"+child.Status+"）")
		ws, werr := e.opsWorkspace(run)
		if werr != nil {
			return true, werr
		}
		return true, e.handleFailure(ctx, run, node, nr, reason, ws)
	default:
		// queued/running/waiting/paused：子运行还在别处推进，父节点继续等。
		return false, nil
	}
}

// completeChildRun 处理「子运行成功」：导入产物、汇总子节点输出、节点置成功。
func (e *Engine) completeChildRun(
	ctx context.Context, run *types.SopRun, node *types.SopNode, nr *types.SopNodeRun, child *types.SopRun,
) error {
	ws, err := e.opsWorkspace(run)
	if err != nil {
		return err
	}
	if err := e.importChildArtifacts(ctx, ws, run, node, nr, child); err != nil {
		return err
	}

	childNRs, err := e.deps.Runs.ListNodeRuns(ctx, child.TenantID, child.ID)
	if err != nil {
		return fmt.Errorf("读取子流程节点记录失败：%w", err)
	}
	var b strings.Builder
	for _, cnr := range childNRs {
		if cnr.Status != types.SopNodeSucceeded {
			continue
		}
		text := strings.TrimSpace(cnr.Output)
		if text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "【%s】%s", nodeDisplayName(child, cnr.NodeID), text)
	}
	nr.Output = firstNonEmpty(b.String(), "子流程执行完成")
	nr.Status = types.SopNodeSucceeded
	nr.GateResult = types.SopGatePass
	nr.GateReason = "子流程执行完成"
	nr.Error = ""
	now := time.Now()
	nr.FinishedAt = &now
	if err := e.deps.Runs.UpdateNodeRun(ctx, nr); err != nil {
		return err
	}
	ws.CheckpointBestEffort(ctx, "subflow "+node.ID+" imported from "+child.ID)
	e.audit(ctx, run, "子流程完成", fmt.Sprintf("%s：导入 %d 个产物", displayName(node), len(childNRs)))
	return nil
}

// importChildArtifacts 把子运行最新一次尝试的产物复制进父工作区同一相对路径，
// 并在父运行下登记产物行（node=父节点、attempt=节点本次尝试）。
//
// 双侧都过 SafeWorkspacePath：子侧防子 run 登记了越界路径，父侧防快照里的路径把
// 文件写到父工作区之外。已经登记过的同路径跳过（崩溃重试不重复累加产物行）。
func (e *Engine) importChildArtifacts(
	ctx context.Context, ws *Workspace, run *types.SopRun, node *types.SopNode,
	nr *types.SopNodeRun, child *types.SopRun,
) error {
	if strings.TrimSpace(child.WorkspaceDir) == "" {
		return fmt.Errorf("子流程工作区未就绪，无法导入产物")
	}
	childWS := &Workspace{Root: filepath.ToSlash(child.WorkspaceDir)}
	arts, err := e.deps.Runs.ListArtifacts(ctx, child.TenantID, child.ID)
	if err != nil {
		return fmt.Errorf("读取子流程产物失败：%w", err)
	}
	existing, err := e.deps.Runs.ListArtifacts(ctx, run.TenantID, run.ID)
	if err != nil {
		return fmt.Errorf("读取运行产物失败：%w", err)
	}
	seen := map[string]bool{}
	for _, a := range existing {
		if a.NodeID == node.ID && a.Attempt == nr.Attempt {
			seen[filepath.ToSlash(a.Path)] = true
		}
	}

	for _, a := range latestAttemptArtifacts(arts) {
		rel := filepath.ToSlash(strings.TrimSpace(a.Path))
		if rel == "" || seen[rel] {
			continue
		}
		src, err := SafeWorkspacePath(childWS, rel)
		if err != nil {
			log.Printf("[sop-engine] 子运行 %s 产物 %q 越界，跳过导入：%v", child.ID, rel, err)
			continue
		}
		dst, err := SafeWorkspacePath(ws, rel)
		if err != nil {
			log.Printf("[sop-engine] 运行 %s 导入路径 %q 越界，跳过：%v", run.ID, rel, err)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fmt.Errorf("创建产物目录失败：%w", err)
		}
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("导入子流程产物 %s 失败：%w", rel, err)
		}
		st, err := os.Stat(dst)
		if err != nil {
			return fmt.Errorf("读取导入后的产物 %s 失败：%w", rel, err)
		}
		sum, err := hashFile(dst)
		if err != nil {
			return fmt.Errorf("计算导入产物指纹失败：%w", err)
		}
		art := &types.SopArtifact{
			TenantID:  run.TenantID,
			RunID:     run.ID,
			NodeID:    node.ID,
			Attempt:   nr.Attempt,
			Name:      firstNonEmpty(strings.TrimSpace(a.Name), filepath.Base(dst)),
			Path:      rel,
			MediaType: a.MediaType,
			Size:      st.Size(),
			SHA256:    sum,
		}
		if err := e.deps.Runs.CreateArtifact(ctx, art); err != nil {
			return err
		}
		seen[rel] = true
	}
	return nil
}

// latestAttemptArtifacts 只保留每个子节点 attempt 最大的那一批产物。
//
// 子节点返工/重跑会为同一路径留下多条尝试记录：全量导入会让父运行出现同路径的
// 陈旧副本，且下游物化按路径去重后取到的可能是旧内容。
func latestAttemptArtifacts(arts []types.SopArtifact) []types.SopArtifact {
	if len(arts) == 0 {
		return nil
	}
	maxAttempt := map[string]int{}
	for _, a := range arts {
		if a.Attempt > maxAttempt[a.NodeID] {
			maxAttempt[a.NodeID] = a.Attempt
		}
	}
	out := make([]types.SopArtifact, 0, len(arts))
	for _, a := range arts {
		if a.Attempt == maxAttempt[a.NodeID] {
			out = append(out, a)
		}
	}
	return out
}

// failChildNode 把子流程节点与运行一起置失败（配置性错误，不走重试策略）。
func (e *Engine) failChildNode(
	ctx context.Context, run *types.SopRun, nr *types.SopNodeRun, reason string,
) error {
	nr.Status = types.SopNodeFailed
	nr.Error = reason
	nr.GateResult = ""
	nr.GateReason = reason
	now := time.Now()
	nr.FinishedAt = &now
	if err := e.deps.Runs.UpdateNodeRun(ctx, nr); err != nil {
		return err
	}
	e.markRunFailed(ctx, run, reason)
	return nil
}
