package workbench

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// CleanupRunner 是工作台的进程内定时清理器（§5.6）。
//
// 不复用 Asynq：Lite 部署没有 Redis，Ticker 在两种部署下行为一致。
// 启动后延迟一轮再跑第一次，避开启动高峰；之后按 interval（默认 24h）重复。
type CleanupRunner struct {
	svc      *Service
	interval time.Duration
	// startupDelay 首次执行的延迟；测试里置 0 以便立刻跑。
	startupDelay time.Duration

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}
	// started 用于区分“从未启动”与“已启动”，避免 Stop 在未启动时死等 doneCh。
	started atomic.Bool
}

// cleanupInterval 是两轮清理之间的间隔。
const cleanupInterval = 24 * time.Hour

// cleanupStartupDelay 是首轮清理的启动延迟。
const cleanupStartupDelay = 10 * time.Minute

// NewCleanupRunner 构造清理器；不启动任何后台协程。
func NewCleanupRunner(svc *Service) *CleanupRunner {
	return &CleanupRunner{
		svc:          svc,
		interval:     cleanupInterval,
		startupDelay: cleanupStartupDelay,
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
	}
}

// Start 启动后台清理循环。重复调用是幂等的；svc 为空时保持休眠。
func (r *CleanupRunner) Start(ctx context.Context) {
	if r == nil || r.svc == nil {
		return
	}
	r.startOnce.Do(func() {
		r.started.Store(true)
		logger.Infof(ctx, "[workbench-cleanup] starting: interval=%s startup_delay=%s",
			r.interval, r.startupDelay)
		go r.loop()
	})
}

// Stop 通知循环退出；未启动时立即返回。幂等。
func (r *CleanupRunner) Stop() {
	r.StopWithin(0)
}

// StopWithin 是带超时的 Stop。timeout<=0 表示一直等到循环返回。
func (r *CleanupRunner) StopWithin(timeout time.Duration) {
	if r == nil || !r.started.Load() {
		return
	}
	r.stopOnce.Do(func() {
		close(r.stopCh)
	})
	if timeout <= 0 {
		<-r.doneCh
		return
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-r.doneCh:
	case <-timer.C:
		logger.Warnf(context.Background(),
			"[workbench-cleanup] still running after %s; continuing shutdown", timeout)
	}
}

// RunOnce 立刻执行一轮清理，供运维与测试调用。
func (r *CleanupRunner) RunOnce(ctx context.Context) (types.WorkbenchCleanupStats, error) {
	return r.svc.RunCleanup(ctx)
}

// loop 是循环主体。每轮用全新的 context：Start 的入参来自容器初始化，
// 调用方返回后就被取消，不能复用到几分钟后的第一次清理。
func (r *CleanupRunner) loop() {
	defer close(r.doneCh)

	if r.startupDelay > 0 {
		startup := time.NewTimer(r.startupDelay)
		defer startup.Stop()
		select {
		case <-startup.C:
		case <-r.stopCh:
			return
		}
	}

	r.runOnce()

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			r.runOnce()
		case <-r.stopCh:
			return
		}
	}
}

// runOnce 给单轮清理一个上限，避免卡住连接拖住整个循环。
func (r *CleanupRunner) runOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	stats, err := r.svc.RunCleanup(ctx)
	if err != nil {
		logger.Warnf(ctx, "[workbench-cleanup] sweep finished with errors: %+v err=%v", stats, err)
		return
	}
	logger.Infof(ctx,
		"[workbench-cleanup] sweep complete: trash_purged=%d orphans_purged=%d notices=%d",
		stats.TrashPurged, stats.OrphansPurged, stats.NoticesCreated)
}
