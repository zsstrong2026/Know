package workbench

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// FillDocArchiveStatus 给章节文档列表批量填 archive_status / archived_version（P2-5）。
//
// 与条目列表同一口径：一次 ListLiveBySources 取回整页映射，再复用
// liveArchiveStatus 计算，不做逐条查询。章节文档的 source 是 doc 任务 id，
// 当前版本取 doc_documents.version（每次编辑都会新增版本行）。
//
// 填充失败只影响徽标展示，调用方按「无归档」降级即可，因此这里只返回错误、
// 不写入任何状态。
func (s *Service) FillDocArchiveStatus(
	ctx context.Context, docs []*types.DocDocumentSummary,
) error {
	if len(docs) == 0 {
		return nil
	}
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(docs))
	for _, doc := range docs {
		ids = append(ids, doc.TaskID)
	}
	grouped, err := s.repo.ListLiveBySources(ctx, tenantID, ownerID, types.WorkbenchSourceDoc, ids)
	if err != nil {
		return err
	}
	for _, doc := range docs {
		rows := grouped[doc.TaskID]
		if len(rows) == 0 {
			doc.ArchiveStatus = archiveStatusNone
			doc.ArchivedVersion = 0
			continue
		}
		doc.ArchiveStatus = s.liveArchiveStatus(ctx, tenantID, rows, doc.Version)
		doc.ArchivedVersion = minSourceVersion(rows)
	}
	return nil
}
