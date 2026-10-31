package workbench

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
)

// leavePurgeAfterDays 与 §5.6 的 30 天保留期一致。
const leavePurgeAfterDays = 30

// LeavePreview 返回“退出 workspace 会失去什么”的只读统计（§7.7、§9.10）。
//
// 只做 COUNT，不加载正文；失败由调用方退化为通用确认，不阻断退出。
// kb_count 恒为 1：工作知识库是每用户唯一的私有库，懒创建由 Bootstrap 负责，
// 这里刻意不触发创建，避免“点了退出却先建了一个库”。
func (s *Service) LeavePreview(ctx context.Context) (*types.WorkbenchLeavePreview, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}

	items, err := s.repo.CountItems(ctx, tenantID, ownerID)
	if err != nil {
		return nil, err
	}
	// 归档分片数近似等于工作知识库里的知识条数（每个 part 对应一条知识）。
	knowledge, err := s.repo.CountArchiveParts(ctx, tenantID, ownerID)
	if err != nil {
		return nil, err
	}

	var docCount int64
	if s.docTasks != nil {
		if tasks, listErr := s.docTasks.ListTasks(ctx); listErr == nil {
			docCount = int64(len(tasks))
		}
	}

	return &types.WorkbenchLeavePreview{
		KBCount:        1,
		KnowledgeCount: knowledge,
		ItemCount:      items,
		DocCount:       docCount,
		PurgeAfterDays: leavePurgeAfterDays,
		// 导出入口是产品入口；P1 只给现有单文档导出，批量导出属 P2。
		ExportLinks: []*types.WorkbenchExportLink{
			{Label: "workbench.leave.exportDocs", Href: "/platform/doc-assistant/tasks"},
		},
	}, nil
}

// insertNotice 写用户级通知；归档失败等场景复用。
// dedupeKey 为空表示不去重（部分唯一索引排除了空串）。
func (s *Service) insertNotice(
	ctx context.Context, userID string, tenantID uint64, kind, dedupeKey, payload string,
) error {
	if payload == "" {
		payload = "{}"
	}
	_, err := s.repo.InsertNotice(ctx, &types.WorkbenchNotice{
		ID:        uuid.NewString(),
		UserID:    userID,
		TenantID:  tenantID,
		Kind:      kind,
		DedupeKey: dedupeKey,
		Payload:   []byte(payload),
		CreatedAt: time.Now().UTC(),
	})
	return err
}
