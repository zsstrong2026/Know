package repository

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm/clause"
)

// workbenchNoticeColumns 显式列；payload 是 JSON 原文，直接按 []byte 读出。
const workbenchNoticeColumns = `id, user_id, tenant_id, kind, dedupe_key, payload, read_at, created_at`

// workbenchNoticeRow 是扫描用的中间结构。
//
// 不能直接扫进 types.WorkbenchNotice：SQLite 把 payload 存成 TEXT，
// database/sql 不支持 string → *json.RawMessage 的 Scan；用 string 承接，
// 再转成 json.RawMessage 对 PG 的 jsonb 同样有效（[]byte → string 是允许的）。
type workbenchNoticeRow struct {
	ID        string     `gorm:"column:id"`
	UserID    string     `gorm:"column:user_id"`
	TenantID  uint64     `gorm:"column:tenant_id"`
	Kind      string     `gorm:"column:kind"`
	DedupeKey string     `gorm:"column:dedupe_key"`
	Payload   string     `gorm:"column:payload"`
	ReadAt    *time.Time `gorm:"column:read_at"`
	CreatedAt time.Time  `gorm:"column:created_at"`
}

// toNotice 把中间结构转成对外的通知实体。
func (row *workbenchNoticeRow) toNotice() *types.WorkbenchNotice {
	return &types.WorkbenchNotice{
		ID:        row.ID,
		UserID:    row.UserID,
		TenantID:  row.TenantID,
		Kind:      row.Kind,
		DedupeKey: row.DedupeKey,
		Payload:   json.RawMessage(row.Payload),
		ReadAt:    row.ReadAt,
		CreatedAt: row.CreatedAt,
	}
}

// InsertNotice 插入用户级通知。
//
// 去重依赖部分唯一索引 uq_wn_dedupe (user_id, dedupe_key) WHERE dedupe_key <> ''：
// 同一事件（例如同一次被移出）只留一条。dedupe_key 为空时不做去重，
// 因为空串被索引条件排除。
func (r *workbenchRepository) InsertNotice(
	ctx context.Context, notice *types.WorkbenchNotice,
) (bool, error) {
	row := map[string]any{
		"id":         notice.ID,
		"user_id":    notice.UserID,
		"tenant_id":  notice.TenantID,
		"kind":       notice.Kind,
		"dedupe_key": notice.DedupeKey,
		"payload":    string(notice.Payload),
		"read_at":    notice.ReadAt,
		"created_at": notice.CreatedAt,
	}
	result := r.db.WithContext(ctx).
		Table("workbench_notices").
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(row)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// ListNotices 取该用户的通知，created_at 倒序。
//
// 刻意不带 tenant_id：被移出 workspace 后仍要能读到“数据保留 30 天”的提醒。
func (r *workbenchRepository) ListNotices(
	ctx context.Context, userID string, unreadOnly bool, limit int,
) ([]*types.WorkbenchNotice, error) {
	query := r.db.WithContext(ctx).
		Table("workbench_notices").
		Select(workbenchNoticeColumns).
		Where("user_id = ?", userID)
	if unreadOnly {
		query = query.Where("read_at IS NULL")
	}
	var rows []*workbenchNoticeRow
	if err := query.Order("created_at DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	notices := make([]*types.WorkbenchNotice, 0, len(rows))
	for _, row := range rows {
		notices = append(notices, row.toNotice())
	}
	return notices, nil
}

// CountUnread 未读数。
func (r *workbenchRepository) CountUnread(ctx context.Context, userID string) (int, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Table("workbench_notices").
		Where("user_id = ? AND read_at IS NULL", userID).
		Count(&count).Error
	if err != nil {
		return 0, err
	}
	return int(count), nil
}

// MarkRead 标记单条已读；必须带 user_id，否则可以标记他人通知。
func (r *workbenchRepository) MarkRead(
	ctx context.Context, userID, noticeID string,
) (int64, error) {
	result := r.db.WithContext(ctx).
		Table("workbench_notices").
		Where("id = ? AND user_id = ? AND read_at IS NULL", noticeID, userID).
		Update("read_at", time.Now().UTC())
	return result.RowsAffected, result.Error
}

// MarkAllRead 标记该用户全部未读。
func (r *workbenchRepository) MarkAllRead(ctx context.Context, userID string) (int64, error) {
	result := r.db.WithContext(ctx).
		Table("workbench_notices").
		Where("user_id = ? AND read_at IS NULL", userID).
		Update("read_at", time.Now().UTC())
	return result.RowsAffected, result.Error
}
