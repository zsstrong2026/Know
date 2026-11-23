// trigger.go 是 SOP 任务触发器：到点扫描「该跑的事项」并发起运行。
//
// 三条口径（spec §7）：
//   - 候选 = 状态命中 ∧ 来源命中 ∧ 从未跑过：跑过的事项不再自动跑（返工由人决定）；
//   - 幂等：同一「配置 × 事项 × 调度槽」只派发一次（ReserveDispatch 的唯一索引兜底），
//     于是单实例调度、重复 tick、进程重启都不会重复发起；
//   - 上限截断：单轮最多 PerRunLimit 条，其余留给下一个槽——宁可慢一轮，也不要把
//     一屏待办一口气全部排进执行队列。
package sop

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// TriggerDeps 触发器的外部依赖。
type TriggerDeps struct {
	Triggers  interfaces.SopTriggerRepository
	Items     interfaces.SopItemRepository
	Runs      *RunService
	Templates interfaces.SopTemplateRepository
}

// NextTriggerRun 计算 after 之后的下一次触发时刻（按配置的时区语义）。
//
// interval 档位：15m/30m/hourly 直接加时长；daily9 是时区内下一个 09:00；
// weekly1 是时区内下一个周一 09:00。cron 用标准 5 段解析（时区内求值）。
func NextTriggerRun(cfg *types.SopTriggerConfig, after time.Time) (*time.Time, error) {
	if cfg == nil {
		return nil, fmt.Errorf("触发配置为空")
	}
	loc, err := triggerLocation(cfg.Timezone)
	if err != nil {
		return nil, err
	}
	switch cfg.Mode {
	case types.SopTriggerModeCron:
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
		schedule, err := parser.Parse(strings.TrimSpace(cfg.Cron))
		if err != nil {
			return nil, fmt.Errorf("cron 表达式无法解析：%w", err)
		}
		next := schedule.Next(after.In(loc))
		if next.IsZero() {
			return nil, fmt.Errorf("cron 表达式没有下一次触发时间")
		}
		return &next, nil
	case types.SopTriggerModeInterval:
		switch cfg.IntervalKey {
		case types.SopTriggerInterval15m:
			next := after.Add(15 * time.Minute)
			return &next, nil
		case types.SopTriggerInterval30m:
			next := after.Add(30 * time.Minute)
			return &next, nil
		case types.SopTriggerIntervalHourly:
			next := after.Add(time.Hour)
			return &next, nil
		case types.SopTriggerIntervalDaily9:
			next := nextDailyAt(after.In(loc), 9, 0)
			return &next, nil
		case types.SopTriggerIntervalWeekly1:
			next := nextWeeklyAt(after.In(loc), time.Monday, 9, 0)
			return &next, nil
		default:
			return nil, fmt.Errorf("未知的触发频率：%q", cfg.IntervalKey)
		}
	default:
		return nil, fmt.Errorf("未知的触发方式：%q", cfg.Mode)
	}
}

// triggerLocation 取配置时区（空=UTC，避免服务器本地时区造成「同一配置两处行为不同」）。
func triggerLocation(name string) (*time.Location, error) {
	if strings.TrimSpace(name) == "" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("时区无法识别：%q", name)
	}
	return loc, nil
}

// nextDailyAt 返回 t 之后最近的一个「时区内 hh:mm」（当天已过则次日）。
func nextDailyAt(t time.Time, hour, minute int) time.Time {
	candidate := time.Date(t.Year(), t.Month(), t.Day(), hour, minute, 0, 0, t.Location())
	if !candidate.After(t) {
		candidate = candidate.AddDate(0, 0, 1)
	}
	return candidate
}

// nextWeeklyAt 返回 t 之后最近的「时区内某 weekday 的 hh:mm」（最多推 8 天必中）。
func nextWeeklyAt(t time.Time, weekday time.Weekday, hour, minute int) time.Time {
	candidate := time.Date(t.Year(), t.Month(), t.Day(), hour, minute, 0, 0, t.Location())
	for i := 0; i < 8; i++ {
		if candidate.Weekday() == weekday && candidate.After(t) {
			return candidate
		}
		candidate = candidate.AddDate(0, 0, 1)
	}
	return candidate
}

// TriggerService 触发扫描与手动运行。
type TriggerService struct {
	d TriggerDeps
}

// NewTriggerService 构造触发服务。
func NewTriggerService(d TriggerDeps) *TriggerService { return &TriggerService{d: d} }

// ScanOnce 跑一轮扫描并派发。
//
// kind=auto|manual；slot 是本次调度槽（自动模式取配置里算出的 NextRunAt，手动模式
// 取当前分钟）。返回本轮历史记录（scanned = fired + skipped 恒等）。
func (s *TriggerService) ScanOnce(
	ctx context.Context, cfg *types.SopTriggerConfig, kind string, slot time.Time,
) (*types.SopTriggerLog, error) {
	if cfg == nil {
		return nil, fmt.Errorf("触发配置为空")
	}
	if s.d.Items == nil || s.d.Triggers == nil || s.d.Runs == nil {
		return nil, fmt.Errorf("触发器未就绪：依赖未注入")
	}
	statuses := cfg.Statuses
	if len(statuses) == 0 {
		// 未选状态时默认「待办」：把 backlog 也跑起来是另一件事，不能默认开。
		statuses = []string{types.SopItemStatusTodo}
	}
	sources := splitSopTriggerSources(cfg.Sources)
	items, err := s.d.Items.ListTriggerCandidates(ctx, cfg.TenantID, cfg.ProjectID, statuses, sources)
	if err != nil {
		return nil, fmt.Errorf("读取待触发事项失败：%w", err)
	}

	limit := cfg.PerRunLimit
	if limit <= 0 {
		limit = 10
	}
	logRec := &types.SopTriggerLog{
		TenantID: cfg.TenantID, ConfigID: cfg.ID, Kind: kind, Scanned: len(items),
	}
	var notes []string
	for i := range items {
		it := &items[i]
		// 先预约再决定派不派：预约是「本槽已处理」的标记。若只在真正派发时才预约，
		// 被上限截断的事项会在同槽重扫时被再次派发，幂等承诺就落空了。
		ok, err := s.d.Triggers.ReserveDispatch(ctx, cfg.ID, it.ID, slot)
		if err != nil {
			return nil, fmt.Errorf("预约派发失败：%w", err)
		}
		if !ok {
			continue // 同槽已处理（幂等）
		}
		if logRec.Fired >= limit {
			continue // 上限截断：本槽不再派发，留给下一个槽
		}
		tplID := s.templateFor(ctx, cfg, it)
		if tplID == "" {
			notes = append(notes, it.No+"：未绑定 SOP 模板且没有默认模板")
			continue
		}
		_, err = s.d.Runs.Create(ctx, cfg.TenantID, "system", CreateRunInput{
			TemplateID:   tplID,
			ItemID:       it.ID,
			TriggerKind:  types.SopRunTriggerSchedule,
			TriggerRefID: cfg.ID,
			Input: map[string]any{
				"item_no": it.No, "item_title": it.Title, "item_description": it.Description,
			},
		})
		if err != nil {
			notes = append(notes, it.No+"："+err.Error())
			continue
		}
		logRec.Fired++
	}
	// scanned = fired + skipped 恒等（被上限截断的也计入 skipped，前端一眼对得上）。
	logRec.Skipped = len(items) - logRec.Fired
	logRec.Note = strings.Join(notes, "；")
	if err := s.d.Triggers.AddLog(ctx, logRec); err != nil {
		return nil, fmt.Errorf("写入触发历史失败：%w", err)
	}

	// 回写下次触发时间（失败不影响本轮结果：下一 tick 会按 NextRunAt 为空再补算）。
	if next, err := NextTriggerRun(cfg, slot); err == nil {
		cfg.NextRunAt = next
	}
	last := slot
	cfg.LastRunAt = &last
	if err := s.d.Triggers.UpsertConfig(ctx, cfg); err != nil {
		log.Printf("[sop-trigger] 回写触发配置失败：%v", err)
	}
	return logRec, nil
}

// templateFor 选本次要用的模板：事项绑定优先（PreferItemTemplate），否则默认模板。
func (s *TriggerService) templateFor(
	ctx context.Context, cfg *types.SopTriggerConfig, it *types.SopItem,
) string {
	if cfg.PreferItemTemplate && strings.TrimSpace(it.TemplateID) != "" {
		return strings.TrimSpace(it.TemplateID)
	}
	if id := strings.TrimSpace(cfg.DefaultTemplateID); id != "" {
		return id
	}
	// 默认模板也空时，再看事项自己绑的（否则明明配了模板的事项也会被跳过）。
	return strings.TrimSpace(it.TemplateID)
}

// splitSopTriggerSources 把 "all" / "a,b" 拆成来源列表（空=不限）。
func splitSopTriggerSources(sources string) []string {
	trimmed := strings.TrimSpace(sources)
	if trimmed == "" || trimmed == "all" {
		return nil
	}
	parts := strings.Split(trimmed, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// RunNow 立即跑一轮（未启用的配置也允许手动触发）。
func (s *TriggerService) RunNow(
	ctx context.Context, tenantID uint64, userID, projectID string,
) error {
	if s.d.Triggers == nil {
		return fmt.Errorf("触发器未就绪：仓储未注入")
	}
	cfg, err := s.d.Triggers.GetConfig(ctx, tenantID, projectID)
	if err != nil {
		return fmt.Errorf("该项目还没有触发配置：%w", err)
	}
	_, err = s.ScanOnce(ctx, cfg, types.SopTriggerLogManual, time.Now().Truncate(time.Minute))
	return err
}

// ---- 调度循环 ----

// triggerTickInterval 是扫描节拍：30s 足够覆盖分钟级语义，又不会把库打热。
const triggerTickInterval = 30 * time.Second

// TriggerScheduler 单实例进程内调度：每 30s 找到点的配置并跑一轮扫描。
//
// 多副本同时上线也安全：同一槽的同一事项由 ReserveDispatch 的唯一索引兜底，
// 抢输的一方只是白跑一次扫描（写一条 fired=0 的历史）。
type TriggerScheduler struct {
	d      TriggerDeps
	svc    *TriggerService
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// NewTriggerScheduler 构造调度器。
func NewTriggerScheduler(d TriggerDeps) *TriggerScheduler {
	return &TriggerScheduler{d: d, svc: NewTriggerService(d)}
}

// Start 启动调度循环（幂等）。
func (s *TriggerScheduler) Start(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(triggerTickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				s.tick(runCtx)
			}
		}
	}()
}

// Stop 停止调度循环并等待当前 tick 收尾。
func (s *TriggerScheduler) Stop() {
	s.mu.Lock()
	cancel := s.cancel
	done := s.done
	s.cancel, s.done = nil, nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			log.Printf("[sop-trigger] 调度循环 30s 内未退出，交由进程结束")
		}
	}
}

// tick 跑一轮：补算 NextRunAt、到点的逐个扫描；单配置失败不影响其它配置。
func (s *TriggerScheduler) tick(ctx context.Context) {
	if s.d.Triggers == nil {
		return
	}
	configs, err := s.d.Triggers.ListEnabledConfigs(ctx)
	if err != nil {
		log.Printf("[sop-trigger] 读取启用配置失败：%v", err)
		return
	}
	now := time.Now()
	for i := range configs {
		cfg := &configs[i]
		if cfg.NextRunAt == nil {
			next, err := NextTriggerRun(cfg, now)
			if err != nil {
				log.Printf("[sop-trigger] 配置 %s 计算下次触发失败：%v", cfg.ID, err)
				continue
			}
			cfg.NextRunAt = next
			if err := s.d.Triggers.UpsertConfig(ctx, cfg); err != nil {
				log.Printf("[sop-trigger] 配置 %s 回写下次触发失败：%v", cfg.ID, err)
			}
			continue
		}
		if cfg.NextRunAt.After(now) {
			continue
		}
		slot := *cfg.NextRunAt
		if _, err := s.svc.ScanOnce(ctx, cfg, types.SopTriggerLogAuto, slot); err != nil {
			log.Printf("[sop-trigger] 配置 %s 扫描失败：%v", cfg.ID, err)
		}
	}
}
