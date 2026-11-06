package repository

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// ListDocumentsByOwner 全局文档列表（P2-5）。
//
// 与 ListByTask 的区别：跨任务、按 owner 过滤、带分页与标题搜索，只返回摘要。
// 标题来自 doc_generation_tasks.theme —— doc_documents 自身没有标题列，
// 而且任务被删时文档已被级联删除，因此 INNER JOIN 不会漏行。
//
// latest_only=true 时用相关子查询取每个任务的最大 version：一条 SQL 完成，
// 不做「先取任务再逐任务查版本」的 N+1；idx_doc_documents_task_id (task_id, version)
// 正好覆盖这个子查询。
func (r *DocDocumentRepository) ListDocumentsByOwner(
	ctx context.Context, tenantID uint64, ownerID string, filter types.DocDocumentFilter,
) ([]*types.DocDocumentSummary, bool, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = types.DocDocumentsDefaultLimit
	}
	if limit > types.DocDocumentsMaxLimit {
		limit = types.DocDocumentsMaxLimit
	}

	var sb strings.Builder
	args := make([]any, 0, 8)
	sb.WriteString(`SELECT d.id, d.task_id, d.version, d.status, d.created_at, t.theme AS title
		FROM doc_documents d
		JOIN doc_generation_tasks t
		  ON t.id = d.task_id AND t.tenant_id = d.tenant_id AND t.owner_id = d.owner_id
		WHERE d.tenant_id = ? AND d.owner_id = ?`)
	args = append(args, tenantID, ownerID)

	if filter.LatestOnly {
		sb.WriteString(` AND d.version = (
			SELECT MAX(d2.version) FROM doc_documents d2
			WHERE d2.task_id = d.task_id AND d2.tenant_id = d.tenant_id AND d2.owner_id = d.owner_id)`)
	}
	if filter.Query != "" {
		// 与工作台列表同口径：标题 LIKE，转义通配符避免 % 变成全表匹配。
		sb.WriteString(` AND t.theme LIKE ? ESCAPE '\'`)
		args = append(args, "%"+escapeLike(filter.Query)+"%")
	}

	cursor, err := decodeItemsCursor(filter.Cursor)
	if err != nil {
		return nil, false, err
	}
	if cursor != nil {
		// 与 ORDER BY 一一对应：created_at 降序，同刻再按 id 降序。
		sb.WriteString(` AND (d.created_at < ? OR (d.created_at = ? AND d.id < ?))`)
		args = append(args, cursor.updatedAt, cursor.updatedAt, cursor.id)
	}

	sb.WriteString(` ORDER BY d.created_at DESC, d.id DESC LIMIT ?`)
	// 多取一条判断是否还有下一页。
	args = append(args, limit+1)

	rows := make([]*types.DocDocumentSummary, 0, limit+1)
	if err := r.db.WithContext(ctx).Raw(sb.String(), args...).Scan(&rows).Error; err != nil {
		return nil, false, fmt.Errorf("list doc documents: %w", err)
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	return rows, hasMore, nil
}
