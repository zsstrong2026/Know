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
// 防递归：发布校验（T9 ValidateDefinition）已拒绝模板自引用，运行期这里再守一层：
// 沿 ParentRunID 走祖先链（最多 sopChildChainCap 层），子模板出现在链上即拒，
// 覆盖 A→B→A 这类发布期看不出来的环，也避免历史数据或跨租户内置模板绕过校验。
//
// 启动幂等：CreateRun(child) 与回写 ChildRunID 不是原子的两步，进程在中间崩溃会留下
// 一条孤儿子运行。拉起前先按 (ParentRunID, TriggerRefID, 活跃态) 探测并复用，保证
// 同一父节点任何时刻只有一条活跃子运行。
//
// 产物口径：一个子运行对应父节点下的一批产物。换子运行（重试/返工）时先作废父运行
// 该节点此前的全部产物行，再按新子运行的产物重建——产物行不携带子运行 id，靠「先清
// 后建」把归属收束到当前子运行，避免旧内容以同路径残留。
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

// sopChildChainCap 是祖先链防递归的遍历上限：超过这个深度的链不再上溯（环至少
// 会在链上出现两次，因此 16 层足够截住任何有意义的自引用环，同时防止脏数据成环
// 时把启动路径拖成无限循环）。
const sopChildChainCap = 16

// startChildRun 为一个就绪的 sop 节点拉起子运行并让节点进入 running。
//
// 子模板必须已发布且带发布 revision：运行一律引用冻结快照，不读活模板（spec §3.3）。
// 校验失败（未发布/找不到/递归引用）把节点与运行一起置失败——这类问题重试也没用，
// 让用户先修模板更划算；仓储错误原样返回，由 runLoop 落 failed。
func (e *Engine) startChildRun(
	ctx context.Context, run *types.SopRun, node *types.SopNode, nr *types.SopNodeRun,
) error {
	childTplID := strings.TrimSpace(node.ChildTemplateID)
	if childTplID == "" {
		return e.failChildNode(ctx, run, nr, "子流程步骤没有选择子流程模板")
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
	// 运行期递归守卫：子模板出现在「本运行 + 祖先链」的模板集合里即拒（A→B→A 环）。
	chain, err := e.ancestorTemplateIDs(ctx, run, sopChildChainCap)
	if err != nil {
		return err
	}
	if chain[childTplID] || chain[rev.TemplateID] {
		return e.failChildNode(ctx, run, nr, "子流程不能递归引用自身")
	}

	child, created, err := e.ensureChildRun(ctx, run, node, tpl, rev)
	if err != nil {
		return err
	}
	if nr.ChildRunID != child.ID {
		// 换了一条子运行（或首次拉起）：父节点此前的产物属于旧子运行的事实，先整体
		// 作废；随后导入路径无条件重建，父产物因此始终对应「当前子运行」。
		if _, derr := e.deps.Runs.DeleteArtifactsByNode(ctx, run.TenantID, run.ID, node.ID); derr != nil {
			return fmt.Errorf("清理子流程旧产物失败：%w", derr)
		}
	}
	if created {
		// 只有真正新建子运行才算一次新尝试；崩溃后接管残留子运行不算（同一条子运行）。
		nr.Attempt++
	}

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

// ensureChildRun 取本次要用的子运行：优先复用崩溃遗留的活跃子运行，否则新建一条。
//
// 复用口径 (ParentRunID=本运行, TriggerRefID=本节点, 活跃态)：那是「上一次拉起已经
// 建好子运行、但没来得及把 ChildRunID 写回节点记录」的残留，直接接管比再建一条安全。
// 复用时不自增 Attempt（同一条子运行不应被算成另一次尝试）。
func (e *Engine) ensureChildRun(
	ctx context.Context, run *types.SopRun, node *types.SopNode,
	tpl *types.SopTemplate, rev *types.SopTemplateRevision,
) (*types.SopRun, bool, error) {
	existing, err := e.deps.Runs.FindChildRun(ctx, run.ID, node.ID)
	if err != nil {
		return nil, false, fmt.Errorf("查询已有子流程运行失败：%w", err)
	}
	if existing != nil && existing.ID != "" {
		return existing, false, nil
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
		return nil, false, fmt.Errorf("创建子流程运行失败：%w", err)
	}
	return child, true, nil
}

// ancestorTemplateIDs 收集「本运行 + 沿 ParentRunID 上溯的祖先运行」涉及的模板 id。
//
// 最多遍历 cap 层：命中上限即停止上溯（防脏数据成环把启动路径拖死）。祖先行读不到
// 属于数据不一致，返回错误让运行收口，而不是悄悄放行一次可能的递归。
func (e *Engine) ancestorTemplateIDs(
	ctx context.Context, run *types.SopRun, cap int,
) (map[string]bool, error) {
	ids := map[string]bool{}
	cur := run
	for depth := 0; cur != nil && depth < cap; depth++ {
		if cur.TemplateID != "" {
			ids[cur.TemplateID] = true
		}
		if cur.ParentRunID == "" {
			return ids, nil
		}
		parent, err := e.deps.Runs.GetRun(ctx, 0, cur.ParentRunID)
		if err != nil {
			return ids, fmt.Errorf("读取父运行失败：%w", err)
		}
		if parent == nil || parent.ID == "" {
			return ids, fmt.Errorf("读取父运行失败：父运行 %s 不存在", cur.ParentRunID)
		}
		cur = parent
	}
	return ids, nil
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
	if err != nil {
		return false, fmt.Errorf("读取子流程运行失败：%w", err)
	}
	if child == nil || child.ID == "" {
		return false, fmt.Errorf("读取子流程运行失败：子运行 %s 不存在", nr.ChildRunID)
	}
	// 归属校验：节点记录的 ChildRunID 必须真的是本运行派生的子运行。跨运行串号
	// （手工改库、崩溃时的半截回写）会让父节点读到别人的运行，一旦导入产物就是把
	// 无关内容写进父工作区；这里直接判失败，不做「尽力而为」的导入。
	if child.ParentRunID != run.ID {
		return true, e.failChildNode(ctx, run, nr, "子流程运行归属不一致：该运行不是本流程派生的子流程")
	}
	if err := e.guardChildRecursion(ctx, run, nr, child); err != nil {
		return true, err
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

// guardChildRecursion 对已存在的子运行再验一次递归：它的模板若出现在「本运行 +
// 祖先链」里，说明这份快照本身构成 A→B→A 环，节点判失败终止循环。
func (e *Engine) guardChildRecursion(
	ctx context.Context, run *types.SopRun, nr *types.SopNodeRun, child *types.SopRun,
) error {
	chain, err := e.ancestorTemplateIDs(ctx, run, sopChildChainCap)
	if err != nil {
		return err
	}
	if child.TemplateID != "" && chain[child.TemplateID] {
		return e.failChildNode(ctx, run, nr, "子流程不能递归引用自身")
	}
	return nil
}

// completeChildRun 处理「子运行成功」：导入产物、汇总子节点输出、节点置成功。
func (e *Engine) completeChildRun(
	ctx context.Context, run *types.SopRun, node *types.SopNode, nr *types.SopNodeRun, child *types.SopRun,
) error {
	ws, err := e.opsWorkspace(run)
	if err != nil {
		return err
	}
	imported, err := e.importChildArtifacts(ctx, ws, run, node, nr, child)
	if err != nil {
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
	e.audit(ctx, run, "子流程完成",
		fmt.Sprintf("%s：导入 %d 个产物", displayName(node), imported))
	return nil
}

// importChildArtifacts 把子运行最新一次尝试的产物复制进父工作区同一相对路径，
// 并在父运行下登记产物行（node=父节点、attempt=节点本次尝试），返回导入条数。
//
// 双侧都过 SafeWorkspacePath：子侧防子 run 登记了越界路径，父侧防快照里的路径把
// 文件写到父工作区之外。
//
// 归属靠「换子运行先清空」而不是靠去重键：调用前（startChildRun）已按换子运行
// 清空过该节点旧产物行，因此这里按路径跳过只可能是同一次导入崩在半途留下的残行，
// 属于可重入跳过，不会把旧子运行的内容留下来。去重键绝不能含 attempt——子运行
// 重试/返工会让父节点 Attempt 归零，按 (node, attempt, path) 去重会撞上旧行，
// 新内容永远导不进来（这就是 review 的 CRITICAL 1）。
func (e *Engine) importChildArtifacts(
	ctx context.Context, ws *Workspace, run *types.SopRun, node *types.SopNode,
	nr *types.SopNodeRun, child *types.SopRun,
) (int, error) {
	if strings.TrimSpace(child.WorkspaceDir) == "" {
		return 0, fmt.Errorf("子流程工作区未就绪，无法导入产物")
	}
	childWS := &Workspace{Root: filepath.ToSlash(child.WorkspaceDir)}
	arts, err := e.deps.Runs.ListArtifacts(ctx, child.TenantID, child.ID)
	if err != nil {
		return 0, fmt.Errorf("读取子流程产物失败：%w", err)
	}
	existing, err := e.deps.Runs.ListArtifacts(ctx, run.TenantID, run.ID)
	if err != nil {
		return 0, fmt.Errorf("读取运行产物失败：%w", err)
	}
	importedPaths := map[string]bool{}
	for _, a := range existing {
		if a.NodeID == node.ID {
			importedPaths[filepath.ToSlash(a.Path)] = true
		}
	}

	imported := 0
	for _, a := range latestAttemptArtifacts(arts) {
		rel := filepath.ToSlash(strings.TrimSpace(a.Path))
		if rel == "" || importedPaths[rel] {
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
			return imported, fmt.Errorf("创建产物目录失败：%w", err)
		}
		if err := copyFile(src, dst); err != nil {
			return imported, fmt.Errorf("导入子流程产物 %s 失败：%w", rel, err)
		}
		st, err := os.Stat(dst)
		if err != nil {
			return imported, fmt.Errorf("读取导入后的产物 %s 失败：%w", rel, err)
		}
		sum, err := hashFile(dst)
		if err != nil {
			return imported, fmt.Errorf("计算导入产物指纹失败：%w", err)
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
			return imported, err
		}
		imported++
	}
	return imported, nil
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
