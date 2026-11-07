// 反向链接服务层（P4-2「页内提及」）。
//
// 关键取舍：提及解析是**派生数据**——解析失败只记日志，绝不让保存失败；
// 它跟随每次内容保存增量重算，不需要独立的维护入口。
package workbench

import (
	"context"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// ListBacklinks 谁引用了某个条目。
func (s *Service) ListBacklinks(ctx context.Context, itemID string) ([]*types.WorkbenchBacklink, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	// 先校验条目归属：否则拿别人的 item id 也能问出「谁引用了它」
	if _, err := s.repo.GetItemMeta(ctx, tenantID, ownerID, itemID); err != nil {
		return nil, mapItemError(err)
	}
	return s.repo.ListBacklinks(ctx, tenantID, ownerID, itemID, types.WorkbenchBacklinksMaxLimit)
}

// syncItemLinks 保存成功后重算该条目的引用关系。
//
// 规则（决策记录）：正文（text_content 投影）里出现另一个条目的标题即算引用。
// 空正文等价于「清空引用」。任何失败都只记日志——反向链接可以下次保存再补，
// 用户的保存不能因为派生数据写不进去而失败。
func (s *Service) syncItemLinks(ctx context.Context, tenantID uint64, ownerID, itemID, text string) {
	if text == "" {
		if err := s.repo.ReplaceItemLinks(ctx, tenantID, ownerID, itemID, nil); err != nil {
			logger.Warnf(ctx, "workbench backlink: failed to clear links for item %s: %v", itemID, err)
		}
		return
	}
	mentioned, err := s.repo.ListItemIDsMentioning(
		ctx, tenantID, ownerID, itemID, text, types.WorkbenchItemMentionsLimit,
	)
	if err != nil {
		logger.Warnf(ctx, "workbench backlink: mention scan failed for item %s: %v", itemID, err)
		return
	}
	targets := make([]types.WorkbenchLinkTarget, 0, len(mentioned))
	for _, id := range mentioned {
		targets = append(targets, types.WorkbenchLinkTarget{
			TargetType: types.WorkbenchLinkTargetItem,
			TargetID:   id,
		})
	}
	if err := s.repo.ReplaceItemLinks(ctx, tenantID, ownerID, itemID, targets); err != nil {
		logger.Warnf(ctx, "workbench backlink: failed to replace links for item %s: %v", itemID, err)
	}
}
