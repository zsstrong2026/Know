// scheduler.go 是引擎的调度循环：单进程内一个 ticker 周期性跑一轮 TickOnce
// （回收过期租约 → 认领到期运行 → 初始化新运行），每条被认领的运行各起一个
// 推进协程（runLoop）并维持心跳续租。
//
// 为什么只有「一个」循环：SOP 的幂等与互斥全部落在数据库状态和租约上
// （ClaimDue 是 CAS、Heartbeat 续租、ExpireLeases 崩溃回收），进程内再多循环也
// 只是同一批 CAS 的竞争者；配 2s 一轮足够让队列在秒级消费完，又不会把库打热。
package engine

import (
	"context"
	"log"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// ---- 调度节拍与租约时长（测试与运维口径的唯一来源）----
const (
	// tickInterval 是调度循环的扫库间隔。
	tickInterval = 2 * time.Second
	// claimLease 是认领时授予的租约时长：执行中每 heartbeatInterval 续一次，
	// 进程崩溃后最多一个租约周期就会被 ExpireLeases 回收。
	claimLease = 30 * time.Second
	// heartbeatInterval 是执行中的续租间隔，必须显著小于 claimLease，
	// 留出一次失败重试的余量（10s 续 30s）。
	heartbeatInterval = 10 * time.Second
	// idlePoll 是无进展时的轮询间隔：节点 / 子运行还在别处推进，稍后再看。
	idlePoll = time.Second
	// shutdownWait 是 Stop 等待推进协程退出的上限。
	shutdownWait = 30 * time.Second
)

// Start 启动单实例调度循环（dig 生命周期 OnStart）。
//
// 幂等：已启动时直接返回（no-op）。不能像以前那样「先取消旧循环再起新的」：
// e.ctx 是所有在途 runLoop 的父 ctx，取消它会把正在跑的运行统统打成 cancelled。
// 需要重启时先 Stop（会清空 e.cancel），再次 Start 才建立新的根 ctx。
func (e *Engine) Start(ctx context.Context) error {
	e.mu.Lock()
	if e.started {
		e.mu.Unlock()
		return nil
	}
	e.ctx, e.cancel = context.WithCancel(ctx)
	e.started = true
	loopCtx := e.ctx
	e.mu.Unlock()

	go func() {
		t := time.NewTicker(tickInterval)
		defer t.Stop()
		for {
			select {
			case <-loopCtx.Done():
				return
			case <-t.C:
				if err := e.TickOnce(loopCtx); err != nil && loopCtx.Err() == nil {
					log.Printf("[sop-engine] 调度轮次失败：%v", err)
				}
			}
		}
	}()
	return nil
}

// Stop 停止调度循环并等待全部推进协程退出（dig 生命周期 OnStop）。
//
// 先取消引擎根 ctx（runLoop 收到取消后走 finishCancelled 落成 cancelled），
// 再轮询 active 清空；超过 shutdownWait 只记日志，不让进程退出被卡住——
// 残留的运行靠租约过期在下次启动时回收。
func (e *Engine) Stop(ctx context.Context) error {
	e.mu.Lock()
	cancel := e.cancel
	e.cancel = nil
	e.started = false
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	deadline := time.Now().Add(shutdownWait)
	for {
		if e.activeCount() == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			log.Printf("[sop-engine] 停止超时：仍有 %d 条运行在推进，交租约回收", e.activeCount())
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// TickOnce 同步跑一轮调度：回收过期租约 → 审批超时清扫 → 在并行额度内认领并
// 初始化到期运行 → 重启恢复（把无人推进的 waiting 运行接回推进协程）。
//
// 测试用 DisableRunLoop=true 时，这是唯一驱动入口（不会有后台协程抢跑）；
// 生产由 Start 的 ticker 调用。返回的错误只来自仓储失败：一条运行初始化失败
// 不终止整轮（把它置 failed 后继续消费下一条），否则一条坏运行会堵住队列。
func (e *Engine) TickOnce(ctx context.Context) error {
	if _, err := e.deps.Runs.ExpireLeases(ctx, time.Now()); err != nil {
		return err
	}
	e.sweepApprovalTimeouts(ctx)
	// 额度 = active 里的推进协程 + 本轮刚认领但未起协程的条数。生产模式 startRunLoop
	// 同步登记，刚认领的运行立刻计入 active；测试模式（DisableRunLoop）不登记，
	// 只能靠 claimed 计数，否则同一轮会把队列一口气认领光。
	claimed := 0
	for {
		if e.activeCount()+claimed >= e.deps.MaxParallel {
			break
		}
		run, err := e.deps.Runs.ClaimDue(ctx, time.Now().Add(claimLease))
		if err != nil {
			return err
		}
		if run == nil {
			break
		}
		if e.deps.DisableRunLoop {
			claimed++
		}
		if err := e.startQueued(ctx, run); err != nil {
			if ctx.Err() != nil {
				// 停机期间被取消：这条运行留在 running，由租约过期回收，不是配置问题。
				return ctx.Err()
			}
			run.Status = types.SopRunFailed
			run.Error = err.Error()
			if uerr := e.deps.Runs.UpdateRun(ctx, run); uerr != nil {
				log.Printf("[sop-engine] 运行 %s 置失败写入失败：%v", run.ID, uerr)
			}
		}
	}
	e.resumeWaiting(ctx)
	return nil
}

// resumeWaiting 把「库里 waiting、但本进程没有推进协程」的接回推进（重启恢复）。
//
// 进程重启后等待审批/等待子流程的运行不会被 ClaimDue 认领（它不是 queued），
// 若不恢复推进，审批通过后那条运行会永远停在 waiting。已在 active 的运行跳过；
// DisableRunLoop=true（测试同步驱动）时不启动任何协程。
func (e *Engine) resumeWaiting(ctx context.Context) {
	if e.deps.DisableRunLoop {
		return
	}
	runs, err := e.deps.Runs.ListRuns(ctx, 0, types.SopRunFilter{Status: types.SopRunWaiting}, 0, 0)
	if err != nil {
		log.Printf("[sop-engine] 恢复 waiting 运行读取失败：%v", err)
		return
	}
	for i := range runs {
		e.startRunLoop(runs[i].ID)
	}
}

// advanceRun 是 runLoop 调用推进步骤的唯一入口：有测试挂点时走挂点，否则走 Advance。
//
// 挂点让「推进途中被取消」这一时序可确定复现（生产恒为 nil），从而能证明取消
// 不会被落成 failed。
func (e *Engine) advanceRun(ctx context.Context, runID string) (bool, error) {
	if e.advanceHook != nil {
		return e.advanceHook(ctx, runID)
	}
	return e.Advance(ctx, runID)
}

// runLoop 推进单条运行直到终态（生产模式）。
//
// 结构：登记 active（并行额度与 CancelRun 入口）→ 起心跳协程续租 → 循环
// 「读运行 → 响应暂停/取消 → Advance 推进一步 → 判定终态 / 无进展退避」。
// 退出时取消本运行的 ctx 并从 active 摘除。
func (e *Engine) runLoop(runID string) {
	e.mu.Lock()
	parent := e.ctx
	e.mu.Unlock()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	e.mu.Lock()
	e.active[runID] = cancel
	e.mu.Unlock()
	e.driveRun(ctx, cancel, runID)
}

// startRunLoop 登记并启动一个推进协程，返回是否真的启动了。
//
// 登记必须同步完成：resumeWaiting/ensureRunning 每轮都可能被调用，若等协程里再
// 写 active，两次调用之间会出现「同一运行两个推进协程」的窗口（第二个还会覆盖
// active 里的取消函数，让 CancelRun 只取消得到最后一个）。
func (e *Engine) startRunLoop(runID string) bool {
	e.mu.Lock()
	if _, ok := e.active[runID]; ok {
		e.mu.Unlock()
		return false
	}
	parent := e.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	e.active[runID] = cancel
	e.mu.Unlock()
	go e.driveRun(ctx, cancel, runID)
	return true
}

// driveRun 是推进协程主体：调用方已完成 active 登记。
func (e *Engine) driveRun(ctx context.Context, cancel context.CancelFunc, runID string) {
	defer func() {
		cancel()
		e.mu.Lock()
		delete(e.active, runID)
		e.mu.Unlock()
	}()

	hb := time.NewTicker(heartbeatInterval)
	defer hb.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-hb.C:
				if err := e.deps.Runs.Heartbeat(ctx, runID, time.Now().Add(claimLease)); err != nil {
					log.Printf("[sop-engine] 运行 %s 心跳续租失败：%v", runID, err)
				}
			}
		}
	}()

	for {
		if ctx.Err() != nil {
			e.finishCancelled(ctx, runID)
			return
		}
		run, err := e.deps.Runs.GetRun(ctx, 0, runID) // tenantID 0=引擎内部按 id 直读
		if err != nil {
			return
		}
		if run.CancelRequested && !isTerminalRun(run.Status) {
			// 取消标记是唯一事实：进程在置标记与收口之间崩溃时，重启后这里必须按
			// 取消收口（而不是被 ExpireLeases 判成 failed）。仓储侧 ExpireLeases
			// 对同类行也走 cancelled，两条路径不冲突（后者只处理租约已过期的行）；
			// 已终态的行保留原结论，不让一条迟到的标记改写成功/失败。
			e.finishCancelled(ctx, runID)
			return
		}
		if run.PauseRequested {
			run.Status = types.SopRunPaused
			run.PauseRequested = false
			_ = e.deps.Runs.UpdateRun(ctx, run)
			return
		}
		progressed, err := e.advanceRun(ctx, runID)
		if err != nil {
			if ctx.Err() != nil {
				// 取消发生在推进途中（CancelRun / Stop）：Advance 的报错只是取消的
				// 副作用，不能当失败落库——收口为 cancelled，且不发失败通知。
				e.finishCancelled(ctx, runID)
				return
			}
			run.Status = types.SopRunFailed
			run.Error = err.Error()
			_ = e.deps.Runs.UpdateRun(ctx, run)
			_ = e.finalizeRun(WithRunTenant(ctx, run), run)
			return
		}
		latest, err := e.deps.Runs.GetRun(ctx, 0, runID)
		if err != nil {
			return
		}
		if isTerminalRun(latest.Status) {
			_ = e.finalizeRun(WithRunTenant(ctx, latest), latest)
			return
		}
		if !progressed {
			select {
			case <-ctx.Done():
				e.finishCancelled(ctx, runID)
				return
			case <-time.After(idlePoll):
			}
		}
	}
}

// finishCancelled 把一次被取消的运行收口为 cancelled：running/pending 节点一律
// cancelled，运行补 FinishedAt。取消不是失败，因此不发失败通知。
//
// 收口写库不能复用已被取消的 ctx（写操作会立刻失败），故先剥离取消信号再限时。
func (e *Engine) finishCancelled(ctx context.Context, runID string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	run, err := e.deps.Runs.GetRun(ctx, 0, runID)
	if err != nil {
		return
	}
	nrs, err := e.deps.Runs.ListNodeRuns(ctx, run.TenantID, runID)
	if err != nil {
		return
	}
	now := time.Now()
	for i := range nrs {
		nr := &nrs[i]
		switch nr.Status {
		case types.SopNodeRunning, types.SopNodePending, types.SopNodeWaitingApproval,
			types.SopNodePendingInput:
			nr.Status = types.SopNodeCancelled
			nr.FinishedAt = &now
			if err := e.deps.Runs.UpdateNodeRun(ctx, nr); err != nil {
				log.Printf("[sop-engine] 运行 %s 节点 %s 取消写入失败：%v", runID, nr.NodeID, err)
			}
		}
	}
	run.Status = types.SopRunCancelled
	run.CancelRequested = false
	run.FinishedAt = &now
	if run.StartedAt != nil {
		run.ExecutionDurationMs = now.Sub(*run.StartedAt).Milliseconds()
	}
	if err := e.deps.Runs.UpdateRun(ctx, run); err != nil {
		log.Printf("[sop-engine] 运行 %s 取消收口写入失败：%v", runID, err)
		return
	}
	e.releaseMCP(ctx, run)
}

// WithRunTenant 把运行所属租户注入 ctx：执行器（平台智能体的角色读取、AgentQA 的
// 知识库检索）都从 ctx 取租户，而调度循环本身没有租户上下文。
//
// 只在推进入口调用一次；调用方已有的租户值不覆盖（上层已显式注入时以它为准）。
func WithRunTenant(ctx context.Context, run *types.SopRun) context.Context {
	if run == nil || run.TenantID == 0 {
		return ctx
	}
	if id, ok := types.TenantIDFromContext(ctx); ok && id != 0 {
		return ctx
	}
	return context.WithValue(ctx, types.TenantIDContextKey, run.TenantID)
}

// isTerminalRun 判断运行是否已到终态（succeeded/failed/cancelled）。
func isTerminalRun(status string) bool {
	switch status {
	case types.SopRunSucceeded, types.SopRunFailed, types.SopRunCancelled:
		return true
	}
	return false
}
