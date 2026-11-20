package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// 编译期断言：澄清/活动仓储实现必须满足接口契约。
var _ interfaces.SopItemExtraRepository = (*SopItemExtraRepo)(nil)

// ErrSopClarificationNotOpen 澄清已是 answered（或不存在可答的 open 行）：
// 回答只允许 open → answered 单向迁移，重复回答一律拒绝。
var ErrSopClarificationNotOpen = errors.New("sop clarification: not open")

// SopItemExtraRepo 是 SOP 事项澄清与活动日志的 GORM 实现。
//
// 只增不改不删的边界由方法面表达：这里没有 Update / Delete，
// 澄清唯一的状态变化是 AnswerClarification 的 open → answered。
// 所有方法都显式接收 tenantID 并写进 WHERE，写入前还在事务内校验
// item 归属（tenant_id, item_id），避免拿别的租户的事项挂澄清/活动。
type SopItemExtraRepo struct{ db *gorm.DB }

// NewSopItemExtraRepo 构造 SOP 事项澄清/活动仓储。
func NewSopItemExtraRepo(db *gorm.DB) *SopItemExtraRepo { return &SopItemExtraRepo{db: db} }

// AddClarification 新增澄清（初始 open）；事务内先确认事项属于该租户。
// 状态、答案与回答时间被强制清零：澄清不能生而 answered，否则可绕过
// open → answered 单向状态机（BeforeCreate 只兜空值，不管脏值）。
func (r *SopItemExtraRepo) AddClarification(ctx context.Context, c *types.SopItemClarification) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireSopItemInTenant(tx, c.TenantID, c.ItemID); err != nil {
			return err
		}
		c.Status = types.SopClarificationStatusOpen
		c.Answer = ""
		c.AnsweredAt = nil
		return tx.Create(c).Error
	})
}

// SeedAnswered 批量写入已答澄清（新建事项 AI 拆解问答 seeding，spec §3.9）。
// 入参里的 TenantID/ItemID 被显式以参数覆盖，避免调用方漏填导致跨租户写入。
func (r *SopItemExtraRepo) SeedAnswered(
	ctx context.Context, tenantID uint64, itemID string, qas []types.SopItemClarification,
) error {
	if len(qas) == 0 {
		return nil
	}
	now := time.Now()
	rows := make([]types.SopItemClarification, 0, len(qas))
	for _, qa := range qas {
		qa.TenantID = tenantID
		qa.ItemID = itemID
		qa.Status = types.SopClarificationStatusAnswered
		if qa.Source == "" {
			qa.Source = types.SopClarificationSourceAI
		}
		if qa.AnsweredAt == nil {
			answeredAt := now
			qa.AnsweredAt = &answeredAt
		}
		rows = append(rows, qa)
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireSopItemInTenant(tx, tenantID, itemID); err != nil {
			return err
		}
		return tx.Create(&rows).Error
	})
}

// ListClarifications 列某事项的澄清（按创建时间升序，软删自动排除）。
func (r *SopItemExtraRepo) ListClarifications(
	ctx context.Context, tenantID uint64, itemID string,
) ([]types.SopItemClarification, error) {
	var list []types.SopItemClarification
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND item_id = ?", tenantID, itemID).
		Order("created_at ASC").
		Find(&list).Error
	return list, err
}

// AnswerClarification 回答澄清：仅 open 可写。事务内读当前状态，
// 再以 status = 'open' 为条件落库，保证并发下也只有一次生效；
// 已是 answered 时返回 ErrSopClarificationNotOpen。
func (r *SopItemExtraRepo) AnswerClarification(ctx context.Context, tenantID uint64, id, answer string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var c types.SopItemClarification
		if err := tx.Where("tenant_id = ? AND id = ?", tenantID, id).First(&c).Error; err != nil {
			return err
		}
		if !types.CanAnswerSopClarification(c.Status) {
			return ErrSopClarificationNotOpen
		}
		now := time.Now()
		res := tx.Model(&types.SopItemClarification{}).
			Where("tenant_id = ? AND id = ? AND status = ?", tenantID, id, types.SopClarificationStatusOpen).
			UpdateColumns(map[string]any{
				"answer":      answer,
				"status":      types.SopClarificationStatusAnswered,
				"answered_at": now,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrSopClarificationNotOpen
		}
		return nil
	})
}

// AddActivity 追加活动日志；事务内先确认事项属于该租户。
func (r *SopItemExtraRepo) AddActivity(ctx context.Context, a *types.SopItemActivity) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireSopItemInTenant(tx, a.TenantID, a.ItemID); err != nil {
			return err
		}
		return tx.Create(a).Error
	})
}

// ListActivities 列某事项的活动（created_at 升序）；kind 为空表示不过滤。
func (r *SopItemExtraRepo) ListActivities(
	ctx context.Context, tenantID uint64, itemID, kind string,
) ([]types.SopItemActivity, error) {
	q := r.db.WithContext(ctx).Where("tenant_id = ? AND item_id = ?", tenantID, itemID)
	if kind != "" {
		q = q.Where("kind = ?", kind)
	}
	var list []types.SopItemActivity
	err := q.Order("created_at ASC").Find(&list).Error
	return list, err
}

// requireSopItemInTenant 校验事项在租户内存在（软删视为不存在）；
// 不满足时返回 gorm.ErrRecordNotFound，调用方据此映射 404。
func requireSopItemInTenant(tx *gorm.DB, tenantID uint64, itemID string) error {
	var n int64
	err := tx.Model(&types.SopItem{}).
		Where("tenant_id = ? AND id = ?", tenantID, itemID).
		Count(&n).Error
	if err != nil {
		return err
	}
	if n == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
