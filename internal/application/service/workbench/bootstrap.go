package workbench

import (
	"context"
	"time"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

// Bootstrap 返回工作台首屏需要的聚合信息（§7.2、§9.1）。
//
// 职责只有四件：确保工作知识库存在、统计条目/文件夹/归档分片、判断是否需要建议语义索引。
// 不做任何写操作（除工作库懒创建），也不返回正文。
func (s *Service) Bootstrap(ctx context.Context) (*types.WorkbenchBootstrap, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	if s.kbService == nil {
		return nil, apperrors.NewServiceUnavailableError(
			"workbench knowledge base service is not configured")
	}
	kb, err := s.kbService.EnsureWorkKnowledgeBase(ctx, tenantID, ownerID)
	if err != nil {
		return nil, err
	}

	items, err := s.repo.CountItems(ctx, tenantID, ownerID)
	if err != nil {
		return nil, err
	}
	folders, err := s.repo.CountFolders(ctx, tenantID, ownerID)
	if err != nil {
		return nil, err
	}
	parts, err := s.repo.CountArchiveParts(ctx, tenantID, ownerID)
	if err != nil {
		return nil, err
	}
	// 章节文档与未读通知都是“附加信息”：任一失败都不能让首屏 500，
	// 因此失败时保持 0，由前端显示为“暂无/0”。
	var documents int64
	if s.docTasks != nil {
		if tasks, listErr := s.docTasks.ListTasks(ctx); listErr == nil {
			documents = int64(len(tasks))
		}
	}
	unread, err := s.CountMyUnreadNotices(ctx, ownerID)
	if err != nil {
		unread = 0
	}

	return &types.WorkbenchBootstrap{
		WorkKB: types.WorkbenchKbSummary{
			ID:               kb.ID,
			Name:             kb.Name,
			IsSystem:         kb.IsSystem,
			SystemKey:        kb.SystemKey,
			Visibility:       kb.Visibility,
			EmbeddingModelID: kb.EmbeddingModelID,
			Capabilities: types.WorkbenchKbCapabilities{
				Vector:  kb.IndexingStrategy.VectorEnabled,
				Keyword: kb.IndexingStrategy.KeywordEnabled,
			},
		},
		// 建议语义索引 = 工作库还没有可用的向量检索能力，且空间里有可用的 embedding 模型。
		// 没有模型时为 false：前端应引导“先去配置模型”而不是显示重建按钮（§7.3）。
		NeedsSemanticIndex: s.NeedsSemanticIndex(ctx, kb),
		Counts: types.WorkbenchBootstrapCounts{
			Items:         items,
			Folders:       folders,
			Documents:     documents,
			ArchiveParts:  parts,
		},
		UnreadNotices: unread,
		Features:      s.features,
		Limits: types.WorkbenchBootstrapLimits{
			ItemContentBytes:  types.WorkbenchMaxYDocBytes,
			ArchiveTotalBytes: types.WorkbenchMaxArchiveBytes,
			ArchiveParts:      types.WorkbenchMaxArchiveParts,
		},
		ServerTime: time.Now().UTC(),
	}, nil
}
