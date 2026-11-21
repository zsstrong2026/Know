package types

import (
	"errors"
	"fmt"
	"strings"
	"time"

	// 时区解析：Asia/Shanghai 等 IANA 名称必须在没有 zoneinfo 数据库的宿主上也能解析
	// （Lite 发行版），与 internal/automation/trigger.go 同口径。
	_ "time/tzdata"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
)

// ---- 常量：触发模式（interval 固定档位 / cron 表达式）----
const (
	SopTriggerModeInterval = "interval"
	SopTriggerModeCron     = "cron"
)

// ---- 常量：interval 固定档位（15m/30m/hourly/daily9/weekly1）----
const (
	SopTriggerInterval15m     = "15m"
	SopTriggerInterval30m     = "30m"
	SopTriggerIntervalHourly  = "hourly"
	SopTriggerIntervalDaily9  = "daily9"
	SopTriggerIntervalWeekly1 = "weekly1"
)

// SopTriggerIntervalKeys 是 interval 模式允许的档位清单（顺序即前端下拉顺序）。
var SopTriggerIntervalKeys = []string{
	SopTriggerInterval15m, SopTriggerInterval30m, SopTriggerIntervalHourly,
	SopTriggerIntervalDaily9, SopTriggerIntervalWeekly1,
}

// ---- 常量：触发历史来源（auto 调度器自动 / manual 立即运行）----
const (
	SopTriggerLogAuto   = "auto"
	SopTriggerLogManual = "manual"
)

// SopTriggerMinInterval 是 cron 模式允许的最短派发间隔（与 automation 同口径，
// 防止 “* * * * *” 这类表达式把调度器打成忙轮询）。
const SopTriggerMinInterval = 5 * time.Minute

// ErrInvalidSopTrigger 触发配置校验失败的哨兵错误；所有校验失败都以它包装。
var ErrInvalidSopTrigger = errors.New("sop trigger: invalid config")

// SopTriggerDefaultStatuses 是未显式指定时采用的候选状态（与迁移列默认值一致）。
var SopTriggerDefaultStatuses = []string{SopItemStatusBacklog, SopItemStatusTodo}

// sopTriggerCronParser 与 automation/trigger.go 的 standardParser 同款：五段（分钟在前）
// 或描述符。SOP 触发不需要秒级精度（最小间隔 5 分钟），故不接受六段表达式。
var sopTriggerCronParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

// SopTriggerConfig 项目级触发配置：每项目一条（project_id 唯一，Upsert 语义）。
//
// 扫描口径：状态 ∈ Statuses、来源符合 Sources（"all" 或逗号分隔的键值）、
// 且事项尚无运行记录（未跑过）；单轮最多派发 PerRunLimit 条，其余留给下一轮。
type SopTriggerConfig struct {
	ID                string `gorm:"type:varchar(36);primaryKey" json:"id"`
	TenantID          uint64 `gorm:"index" json:"-"`
	ProjectID         string `gorm:"type:varchar(36)" json:"project_id"`
	Mode              string `gorm:"type:varchar(16);default:interval" json:"mode"`
	IntervalKey       string `gorm:"type:varchar(16);default:daily9" json:"interval_key"`
	Cron              string `gorm:"type:varchar(100)" json:"cron"`
	Timezone          string `gorm:"type:varchar(64);default:Asia/Shanghai" json:"timezone"`
	DefaultTemplateID string `gorm:"type:varchar(36)" json:"default_template_id"`
	// PreferItemTemplate 为 true 时优先用事项自己绑定的 SOP 模板，缺失才回落默认模板。
	//
	// 有意不写 gorm `default:true`：若写了，GORM 会把零值 false 当成「未设置」替换成列默认
	// 值 TRUE，用户显式关掉该开关就永远存不进去。DDL 的 DEFAULT TRUE 只用于裸 SQL 插入。
	PreferItemTemplate bool `gorm:"not null" json:"prefer_item_template"`
	// Sources 是 "all" 或逗号分隔的来源键值（customer/internal/data/competitor/strategy/other）。
	Sources  string   `gorm:"type:varchar(16);default:all" json:"sources"`
	Statuses []string `gorm:"serializer:json" json:"statuses"`
	// PerRunLimit 单轮最多派发条数，合法区间 1–10。
	PerRunLimit int            `gorm:"not null;default:10" json:"per_run_limit"`
	Enabled     bool           `gorm:"not null;default:false" json:"enabled"`
	LastRunAt   *time.Time     `json:"last_run_at"`
	NextRunAt   *time.Time     `json:"next_run_at"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

func (SopTriggerConfig) TableName() string { return "sop_trigger_configs" }

// BeforeCreate 补主键、时间戳与默认值；Statuses 为 nil 时补默认候选状态，
// 避免 serializer 把 NULL 写进 NOT NULL 列。
func (c *SopTriggerConfig) BeforeCreate(_ *gorm.DB) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	now := time.Now()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = now
	}
	if c.Statuses == nil {
		c.Statuses = append([]string{}, SopTriggerDefaultStatuses...)
	}
	return nil
}

// NormalizeSopTriggerConfig 把空字段补成迁移列默认值（mode/interval_key/timezone/
// sources/statuses 未指定时与 DDL 默认一致），供 Upsert 前调用。
//
// 有意不补 per_run_limit：0 表示调用方漏填，必须由校验拒绝而不是静默改成 10。
func NormalizeSopTriggerConfig(c *SopTriggerConfig) {
	if c.Mode == "" {
		c.Mode = SopTriggerModeInterval
	}
	if c.IntervalKey == "" {
		c.IntervalKey = SopTriggerIntervalDaily9
	}
	if c.Timezone == "" {
		c.Timezone = "Asia/Shanghai"
	}
	if c.Sources == "" {
		c.Sources = "all"
	}
	if len(c.Statuses) == 0 {
		c.Statuses = append([]string{}, SopTriggerDefaultStatuses...)
	}
}

// ValidateSopTriggerConfig 校验触发配置：
//   - mode ∈ interval|cron；
//   - interval 模式：interval_key 必须落在固定档位清单；
//   - cron 模式：表达式非空、可解析（五段/描述符），且相邻两次触发间隔不小于 5 分钟；
//   - timezone 可解析；
//   - per_run_limit ∈ [1,10]；
//   - statuses 只能是 8 态事项状态，且不含终态 done/closed（配置了也永远不会被扫描到）；
//   - sources 为 "all" 或已知来源键值的逗号分隔列表。
//
// 失败一律以 ErrInvalidSopTrigger 包装，调用方用 errors.Is 判定即可映射 400。
func ValidateSopTriggerConfig(c *SopTriggerConfig) error {
	if c == nil {
		return invalidSopTrigger("config is required")
	}
	switch c.Mode {
	case SopTriggerModeInterval:
		if !containsString(SopTriggerIntervalKeys, c.IntervalKey) {
			return invalidSopTrigger("unknown interval_key %q (want one of %s)",
				c.IntervalKey, strings.Join(SopTriggerIntervalKeys, "/"))
		}
	case SopTriggerModeCron:
		if err := validateSopTriggerCron(c.Cron); err != nil {
			return err
		}
	default:
		return invalidSopTrigger("unknown mode %q (want interval/cron)", c.Mode)
	}
	if c.Timezone != "" {
		if _, err := time.LoadLocation(c.Timezone); err != nil {
			return invalidSopTrigger("unknown timezone %q", c.Timezone)
		}
	}
	if c.PerRunLimit < 1 || c.PerRunLimit > 10 {
		return invalidSopTrigger("per_run_limit must be between 1 and 10, got %d", c.PerRunLimit)
	}
	if err := validateSopTriggerStatuses(c.Statuses); err != nil {
		return err
	}
	if err := validateSopTriggerSources(c.Sources); err != nil {
		return err
	}
	return nil
}

// validateSopTriggerCron 解析 cron 表达式并检查相邻触发的间隔。
func validateSopTriggerCron(expr string) error {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return invalidSopTrigger("cron mode needs an expression")
	}
	if strings.HasPrefix(expr, "CRON_TZ=") || strings.HasPrefix(expr, "TZ=") {
		return invalidSopTrigger("set the timezone field instead of a TZ prefix")
	}
	sched, err := sopTriggerCronParser.Parse(expr)
	if err != nil {
		return invalidSopTrigger("cannot parse cron %q: %v", expr, err)
	}
	from := time.Now().UTC()
	previous := from
	for i := 0; i < 64; i++ {
		next := sched.Next(previous)
		if next.IsZero() {
			break
		}
		if gap := next.Sub(previous); gap < SopTriggerMinInterval && i > 0 {
			return invalidSopTrigger("cron %q fires every %s; the minimum is %s",
				expr, gap, SopTriggerMinInterval)
		}
		previous = next
	}
	return nil
}

// validateSopTriggerStatuses 只接受可被触发的 8 态状态，done/closed 直接拒绝。
func validateSopTriggerStatuses(statuses []string) error {
	for _, s := range statuses {
		if !IsSopItemStatus(s) {
			return invalidSopTrigger("unknown status %q", s)
		}
		if s == SopItemStatusDone || s == SopItemStatusClosed {
			return invalidSopTrigger("status %q is terminal and can never be triggered", s)
		}
	}
	return nil
}

// validateSopTriggerSources "all" 或已知来源键值的逗号分隔列表。
func validateSopTriggerSources(sources string) error {
	trimmed := strings.TrimSpace(sources)
	if trimmed == "" || trimmed == "all" {
		return nil
	}
	for _, s := range strings.Split(trimmed, ",") {
		s = strings.TrimSpace(s)
		if _, ok := sopSourceLabels[s]; !ok {
			return invalidSopTrigger("unknown source %q", s)
		}
	}
	return nil
}

func invalidSopTrigger(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidSopTrigger, fmt.Sprintf(format, args...))
}

func containsString(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}

// SopTriggerLog 触发历史：只增不改（每次扫描一行，scanned = fired + skipped）。
type SopTriggerLog struct {
	ID        string    `gorm:"type:varchar(36);primaryKey" json:"id"`
	TenantID  uint64    `gorm:"index" json:"-"`
	ConfigID  string    `gorm:"type:varchar(36)" json:"config_id"`
	Kind      string    `gorm:"type:varchar(16);default:auto" json:"kind"`
	Scanned   int       `gorm:"default:0" json:"scanned"`
	Fired     int       `gorm:"default:0" json:"fired"`
	Skipped   int       `gorm:"default:0" json:"skipped"`
	Note      string    `gorm:"type:text" json:"note"`
	CreatedAt time.Time `json:"created_at"`
}

func (SopTriggerLog) TableName() string { return "sop_trigger_logs" }

func (l *SopTriggerLog) BeforeCreate(_ *gorm.DB) error {
	if l.ID == "" {
		l.ID = uuid.NewString()
	}
	if l.CreatedAt.IsZero() {
		l.CreatedAt = time.Now()
	}
	if l.Kind == "" {
		l.Kind = SopTriggerLogAuto
	}
	return nil
}

// SopDispatchReservation 派发幂等预约：同一 (config_id, item_id, slot) 只能插入一次，
// 唯一索引 ux_sop_dispatch_slot 是幂等的唯一判据。
//
// 旁挂独立表而不是给 sop_trigger_logs 加唯一键：日志是「一轮扫描」的聚合，建议
// 保留全量历史；预约是「一事一槽」的占位，两者粒度不同。
type SopDispatchReservation struct {
	ID        string    `gorm:"type:varchar(36);primaryKey" json:"id"`
	ConfigID  string    `gorm:"type:varchar(36)" json:"config_id"`
	ItemID    string    `gorm:"type:varchar(36)" json:"item_id"`
	Slot      time.Time `json:"slot"`
	CreatedAt time.Time `json:"created_at"`
}

func (SopDispatchReservation) TableName() string { return "sop_dispatch_reservations" }

func (r *SopDispatchReservation) BeforeCreate(_ *gorm.DB) error {
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now()
	}
	return nil
}
