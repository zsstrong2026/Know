package repository

import (
	"context"
	"strings"

	"gorm.io/gorm"
)

// pmUpdateByID 统一更新入口：
//  1. 强制拼接 tenant_id 条件，防跨租户越权；
//  2. Select("*") 保证零值字段也能落库（例如清空关联版本 ID）；
//  3. 影响行数为 0 视为目标不存在，返回各仓储自己的 notFound 错误。
func pmUpdateByID(ctx context.Context, db *gorm.DB, model any, id string, tenantID uint64, notFound error) error {
	res := db.WithContext(ctx).Model(model).
		Where("id = ? AND tenant_id = ?", id, tenantID).
		Select("*").
		Omit("id", "tenant_id", "created_at", "deleted_at").
		Updates(model)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return notFound
	}
	return nil
}

// IsUniqueViolation 兼容 PostgreSQL 与 SQLite 的唯一约束错误识别。
// 导出给 handler：并发撞唯一索引时把驱动错误翻译成 400「已被占用」。
func IsUniqueViolation(err error) bool { return isUniqueViolation(err) }

// isUniqueViolation 兼容 PostgreSQL 与 SQLite 的唯一约束错误识别
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate key") ||
		strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "unique violation")
}
