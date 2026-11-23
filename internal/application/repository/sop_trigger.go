package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 编译期断言：触发仓储实现必须满足接口契约（T18 管理 API、T35 扫描派发的消费面）。
var _ interfaces.SopTriggerRepository = (*SopTriggerRepo)(nil)

// SopTriggerRepo 是触发配置、触发历史与派发预约的 GORM 实现。
//
// 不变式：
//   - 每条查询都带 tenant_id（调度器跨租户的 ListEnabledConfigs 属 T35 追加，不在本任务）；
//   - 触发配置每项目唯一（ux_sop_trigger_project，软删行不占位），Upsert 是
//     「同项目更新原行、否则插入」；
//   - ReserveDispatch 以唯一索引 ux_sop_dispatch_slot 判幂等：同 (config,item,slot)
//     第二次插入冲突即返回 (false,nil)，绝不把冲突当错误上抛——冲突是正常竞态。
type SopTriggerRepo struct{ db *gorm.DB }

// NewSopTriggerRepo 构造 SOP 触发仓储。
func NewSopTriggerRepo(db *gorm.DB) *SopTriggerRepo { return &SopTriggerRepo{db: db} }

// sopTriggerConfigUpdateColumns 是 Upsert 命中已有行时允许改写的列白名单。
//
// id/tenant_id/project_id/created_at/deleted_at 是归属与生命周期列：换项目应新建
// 配置，跨租户改写必须被拦住（条件里同时拼 tenant_id）。
var sopTriggerConfigUpdateColumns = []string{
	"mode", "interval_key", "cron", "timezone", "default_template_id",
	"prefer_item_template", "sources", "statuses", "per_run_limit", "enabled",
	"last_run_at", "next_run_at", "updated_at",
}

// UpsertConfig 按 (租户, 项目) 落触发配置：同项目更新原行，否则插入。
//
// 校验先行：非法配置（未知 mode/interval_key、cron 间隔过密、per_run_limit 越界、
// statuses 含终态等）直接返回 ErrInvalidSopTrigger 包裹的错误且不落库——Upsert 是
// 唯一的配置写入口，校验放在这里调用方不可能绕过。
//
// 布尔列（prefer_item_template / enabled）按结构体传值落库，不做「零值回落列默认」：
// 显式 false 必须能存进去（关闭开关）。省略字段时的语义差异由调用方 DTO 补全负责。
func (r *SopTriggerRepo) UpsertConfig(ctx context.Context, c *types.SopTriggerConfig) error {
	if c == nil {
		return fmt.Errorf("%w: config is required", types.ErrInvalidSopTrigger)
	}
	types.NormalizeSopTriggerConfig(c)
	if err := types.ValidateSopTriggerConfig(c); err != nil {
		return err
	}

	var existing types.SopTriggerConfig
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND project_id = ?", c.TenantID, c.ProjectID).
		First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.UpdatedAt = time.Now()
		return r.db.WithContext(ctx).Create(c).Error
	}
	if err != nil {
		return err
	}

	// 归属列以库内行为准：调用方传零值不得把配置改挂到别的项目/租户下。
	c.ID = existing.ID
	c.CreatedAt = existing.CreatedAt
	c.UpdatedAt = time.Now()
	return r.db.WithContext(ctx).Model(&types.SopTriggerConfig{}).
		Where("id = ? AND tenant_id = ?", existing.ID, c.TenantID).
		Select(sopTriggerConfigUpdateColumns).
		Updates(c).Error
}

// GetConfig 按 (租户, 项目) 取触发配置；查不到返回 gorm.ErrRecordNotFound。
func (r *SopTriggerRepo) GetConfig(
	ctx context.Context, tenantID uint64, projectID string,
) (*types.SopTriggerConfig, error) {
	var c types.SopTriggerConfig
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND project_id = ?", tenantID, projectID).
		First(&c).Error
	return &c, err
}

// ListEnabledConfigs 列全部启用中的触发配置（跨租户，调度器内部用）。
//
// 有意不带租户条件：调度循环本身没有租户上下文（与引擎扫 waiting 运行同款），
// 返回的行仍带真实 TenantID，后续写路径照常按租户收口。
func (r *SopTriggerRepo) ListEnabledConfigs(ctx context.Context) ([]types.SopTriggerConfig, error) {
	var list []types.SopTriggerConfig
	err := r.db.WithContext(ctx).
		Where("enabled = ? AND deleted_at IS NULL", true).
		Order("created_at ASC").
		Find(&list).Error
	return list, err
}

// AddLog 追加一条触发历史（只增不改；id/created_at/kind 由模型 BeforeCreate 补齐）。
func (r *SopTriggerRepo) AddLog(ctx context.Context, l *types.SopTriggerLog) error {
	return r.db.WithContext(ctx).Create(l).Error
}

// ListLogs 列某配置的触发历史，created_at 倒序分页；pageSize<=0 不分页。
func (r *SopTriggerRepo) ListLogs(
	ctx context.Context, tenantID uint64, configID string, page, pageSize int,
) ([]types.SopTriggerLog, error) {
	q := r.db.WithContext(ctx).
		Where("tenant_id = ? AND config_id = ?", tenantID, configID)
	if pageSize > 0 {
		if page <= 0 {
			page = 1
		}
		q = q.Offset((page - 1) * pageSize).Limit(pageSize)
	}
	var list []types.SopTriggerLog
	err := q.Order("created_at DESC, id DESC").Find(&list).Error
	return list, err
}

// ReserveDispatch 预约「配置 × 事项 × 调度槽」的一次派发，返回是否由本次调用占位。
//
// 幂等判据是唯一索引 ux_sop_dispatch_slot(config_id, item_id, slot)：
//   - PostgreSQL / SQLite 都用 ON CONFLICT DO NOTHING，然后看 RowsAffected：
//     1 = 本次拿到，0 = 该槽已被别人（或本配置上一轮）派发过，返回 (false,nil)；
//   - 不把冲突当错误：并发调度器抢同一槽是正常竞态，不是故障。
func (r *SopTriggerRepo) ReserveDispatch(
	ctx context.Context, configID, itemID string, slot time.Time,
) (bool, error) {
	if configID == "" || itemID == "" {
		return false, fmt.Errorf("%w: config_id and item_id are required", types.ErrInvalidSopTrigger)
	}
	reservation := &types.SopDispatchReservation{
		ConfigID: configID,
		ItemID:   itemID,
		Slot:     slot.UTC(),
	}
	res := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(reservation)
	if res.Error != nil {
		// ON CONFLICT 未生效的极端情况（索引缺失等）：冲突仍按「已派发」处理，
		// 其余错误照常上抛。
		if isUniqueViolation(res.Error) {
			return false, nil
		}
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}
