package repository

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// IsUniqueViolation 同时覆盖 PostgreSQL 与 SQLite 的驱动错误文案：
// handler 用它把并发撞唯一索引翻译成 400「已被占用」，文案不匹配就会退回 500。
func TestIsUniqueViolationRecognizesBothDialects(t *testing.T) {
	pg := errors.New(`ERROR: duplicate key value violates unique constraint "ux_sop_projects_tenant_code" (SQLSTATE 23505)`)
	require.True(t, IsUniqueViolation(pg), "PostgreSQL 唯一冲突")

	sqlite := errors.New(`UNIQUE constraint failed: sop_projects.code`)
	require.True(t, IsUniqueViolation(sqlite), "SQLite 唯一冲突")

	require.False(t, IsUniqueViolation(nil))
	require.False(t, IsUniqueViolation(errors.New("connection refused")))
}
