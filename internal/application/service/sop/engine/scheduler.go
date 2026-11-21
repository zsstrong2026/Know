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
	"fmt"
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
// 可重复调用：已有循环时先停掉旧的再起新的，避免一个进程内叠两套 ticker。
func (e *Engine) Start(ctx context.Context) error {
	e.mu.Lock()
	if e.cancel != nil {
		e.cancel()
	}
	e.ctx, e.cancel = context.WithCancel(ctx)
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

// TickOnce 同步跑一轮调度：回收过期租约 → 在并行额度内认领并初始化到期运行。
//
// 测试用 DisableRunLoop=true 时，这是唯一驱动入口（不会有后台协程抢跑）；
// 生产由 Start 的 ticker 调用。返回的错误只来自仓储失败：一条运行初始化失败
// 不终止整轮（把它置 failed 后继续消费下一条），否则一条坏运行会堵住队列。
func (e *Engine) TickOnce(ctx context.Context) error {
	if _, err := e.deps.Runs.ExpireLeases(ctx, time.Now()); err != nil {
		return err
	}
	// 额度 = 已在推进的协程 + 本轮刚认领的条数：刚认领的运行要等 runLoop 启动后
	// 才登记进 active，只数 active 会在同一轮里超发。
	claimed := 0
	for {
		if e.activeCount()+claimed >= e.deps.MaxParallel {
			return nil
		}
		run, err := e.deps.Runs.ClaimDue(ctx, time.Now().Add(claimLease))
		if err != nil || run == nil {
			return err
		}
		claimed++
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
}

// Advance 推进一次运行（就绪节点执行与门禁裁定）。
//
// T12 只交付调度骨架，本桩是**临时占位**：T13 在 node.go 里给出真实实现时，
// 必须同时删掉本函数（否则同包重复声明），签名保持不变：
//
//	func (e *Engine) Advance(ctx context.Context, runID string) (bool, error)
//
// 占位实现显式返回「尚未接入」：T13 接入前被误驱动的运行会立刻以明确原因失败，
// 而不是空转并无限续租。
func (e *Engine) Advance(ctx context.Context, runID string) (bool, error) {
	return false, fmt.Errorf("运行 %s 的节点推进尚未接入", runID)
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
		if run.PauseRequested {
			run.Status = types.SopRunPaused
			run.PauseRequested = false
			_ = e.deps.Runs.UpdateRun(ctx, run)
			return
		}
		progressed, err := e.Advance(ctx, runID) // T13 实现
		if err != nil {
			run.Status = types.SopRunFailed
			run.Error = err.Error()
			_ = e.deps.Runs.UpdateRun(ctx, run)
			_ = e.finalizeRun(ctx, run)
			return
		}
		latest, err := e.deps.Runs.GetRun(ctx, 0, runID)
		if err != nil {
			return
		}
		if isTerminalRun(latest.Status) {
			_ = e.finalizeRun(ctx, latest)
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
	}
}

// isTerminalRun 判断运行是否已到终态（succeeded/failed/cancelled）。
func isTerminalRun(status string) bool {
	switch status {
	case types.SopRunSucceeded, types.SopRunFailed, types.SopRunCancelled:
		return true
	}
	return false
}
