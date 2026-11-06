package workbench

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
)

// 保留策略常量（§5.6）。
const (
	// orphanRetentionDays 成员被移出后数据的保留天数，也是回收站条目的保留期。
	orphanRetentionDays = 30
	// orphanWarnAheadDays 清理前的预警窗口。
	orphanWarnAheadDays = 7
	// pendingArchiveTTL 超过这个时长且知识已存在的 pending 映射收敛为 active。
	pendingArchiveTTL = time.Hour
	// cleanupBatchSize 每批处理行数；单批失败不阻塞后续批次。
	cleanupBatchSize = 100
	// versionKeepPerItem 每个条目保留的版本数下限（最近 N 个永不过期）。
	versionKeepPerItem = 50
	// versionRetentionDays 版本在 keep 之外还需保留的天数。
	versionRetentionDays = 30
	// assetRetentionDays 资产在“最后一次被引用”之后保留的天数。
	//
	// 删 block 不会立即删资产（服务端不解析 Y.Doc），因此给 30 天缓冲：
	// 用户撤销删除、或从历史版本恢复时仍能拿到文件（§12.2 第 5 条）。
	assetRetentionDays = 30
)

// workKBLifecycle 是清理路径所需的额外知识库能力：
// 查询（不需要成员关系）与硬删。
//
// 不复用 EnsureWorkKnowledgeBase：孤儿用户已不是成员，ensure 会先做成员校验并失败。
type workKBLifecycle interface {
	GetWorkKnowledgeBase(
		ctx context.Context, tenantID uint64, userID string,
	) (*types.KnowledgeBase, error)
	PurgeWorkKnowledgeBase(ctx context.Context, kbID string) error
}

// OnMemberRemoved 记录“成员被移出/主动离开”的 30 天保留标记并写通知（§5.5）。
//
// 只对在该 workspace 内有工作台数据的用户落标记，避免污染孤儿表。
// 调用方应把失败当作可忽略：成员关系已写完，钩子不能反向让移除操作失败。
func (s *Service) OnMemberRemoved(ctx context.Context, tenantID uint64, userID, reason string) error {
	if tenantID == 0 || userID == "" || types.IsSyntheticUserID(userID) {
		return nil
	}
	items, err := s.repo.CountItems(ctx, tenantID, userID)
	if err != nil {
		return err
	}
	parts, err := s.repo.CountArchiveParts(ctx, tenantID, userID)
	if err != nil {
		return err
	}
	// 没有任何工作台数据时不写标记：避免“来了又走”的访客在孤儿表里堆垃圾。
	if items == 0 && parts == 0 {
		return nil
	}

	now := time.Now().UTC()
	purgeAfter := now.AddDate(0, 0, orphanRetentionDays)
	if reason == "" {
		reason = types.WorkbenchOrphanReasonRemoved
	}
	if err := s.repo.UpsertOrphan(ctx, &types.WorkbenchOrphan{
		ID:          uuid.NewString(),
		TenantID:    tenantID,
		UserID:      userID,
		Reason:      reason,
		SuspendedAt: now,
		PurgeAfter:  purgeAfter,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		return err
	}

	payload, _ := json.Marshal(map[string]any{
		"item_count":      items,
		"knowledge_count": parts,
		"purge_after":     purgeAfter.Format(time.RFC3339),
	})
	// 同一次被移除只提醒一次：dedupe 里带秒级时间戳。
	return s.insertNotice(ctx, userID, tenantID, types.WorkbenchNoticeRemoved,
		fmt.Sprintf("removed:%d:%d", tenantID, now.Unix()), string(payload))
}

// OnMemberAdded 在成员重新加入时解除保留标记并写恢复通知（§5.5）。
//
// 顺序必须是“成员关系已成功 → 再删标记”，否则会让数据提前复活。
func (s *Service) OnMemberAdded(ctx context.Context, tenantID uint64, userID string) error {
	if tenantID == 0 || userID == "" || types.IsSyntheticUserID(userID) {
		return nil
	}
	orphan, err := s.repo.GetOrphan(ctx, tenantID, userID)
	if err != nil {
		return err
	}
	if orphan == nil {
		// 没有标记说明从未被保留，不需要任何动作。
		return nil
	}
	if _, err := s.repo.DeleteOrphan(ctx, tenantID, userID); err != nil {
		return err
	}
	// 恢复后的真实数量：此刻数据已可见，直接取当前值而不是回填标记里的旧值。
	items, _ := s.repo.CountItems(ctx, tenantID, userID)
	parts, _ := s.repo.CountArchiveParts(ctx, tenantID, userID)
	payload, _ := json.Marshal(map[string]any{
		"item_count":      items,
		"knowledge_count": parts,
	})
	return s.insertNotice(ctx, userID, tenantID, types.WorkbenchNoticeRestored,
		fmt.Sprintf("restored:%d:%d", tenantID, time.Now().UTC().Unix()), string(payload))
}

// RunCleanup 执行一轮清理（§5.6）。
//
// 步骤顺序固定：回收站 → 到期孤儿 → 7 天预警 → 归档对账。
// 单步失败只记日志并继续后续步骤；返回首个错误供调用方判断是否整体成功。
func (s *Service) RunCleanup(ctx context.Context) (types.WorkbenchCleanupStats, error) {
	stats := types.WorkbenchCleanupStats{}
	now := time.Now().UTC()
	var firstErr error
	recordErr := func(step string, err error) {
		if err == nil {
			return
		}
		logger.Warnf(ctx, "workbench cleanup: %s failed: %v", step, err)
		if firstErr == nil {
			firstErr = err
		}
	}

	// 1. 回收站：硬删超过保留期的条目（含从属行）。
	trashCutoff := now.AddDate(0, 0, -orphanRetentionDays)
	purged, err := s.repo.PurgeTrashedBefore(ctx, trashCutoff, cleanupBatchSize)
	recordErr("purge trashed", err)
	stats.TrashPurged = int(purged)

	// 2. 到期孤儿：销毁数据与工作知识库，最后删标记。
	due, err := s.repo.ListOrphansDue(ctx, now, cleanupBatchSize)
	recordErr("list due orphans", err)
	for _, orphan := range due {
		if purgeErr := s.purgeOrphan(ctx, orphan); purgeErr != nil {
			recordErr("purge orphan "+orphan.ID, purgeErr)
			continue
		}
		stats.OrphansPurged++
	}

	// 3. 7 天预警：同一孤儿只提醒一次。
	expiring, err := s.repo.ListOrphansExpiring(
		ctx, now, now.AddDate(0, 0, orphanWarnAheadDays), cleanupBatchSize)
	recordErr("list expiring orphans", err)
	for _, orphan := range expiring {
		created, warnErr := s.warnOrphanExpiring(ctx, orphan)
		if warnErr != nil {
			recordErr("warn orphan "+orphan.ID, warnErr)
			continue
		}
		if created {
			stats.NoticesCreated++
		}
	}

	// 4. 版本保留：每条目保留最近 50 个，且 30 天内的版本不删（当前 ydoc 不在版本表里，不受影响）。
	if deleted, err := s.repo.PruneExpiredVersions(
		ctx, now.AddDate(0, 0, -versionRetentionDays), versionKeepPerItem, cleanupBatchSize); err != nil {
		recordErr("prune versions", err)
	} else {
		stats.VersionsDeleted = int(deleted)
	}

	// 5. 归档对账：映射指向的知识不存在 → deleted；pending 超时且知识存在 → active。
	_, err = s.reconcileArchives(ctx)
	recordErr("reconcile archives", err)

	// 6. 资产回收：超过保留期仍未被引用的文件软删 + 删物理文件。
	//    放在最后：条目/版本先收敛，被连带删除的资产行在这一步之前就已不存在。
	if removed := s.cleanupStaleAssets(
		ctx, now.AddDate(0, 0, -assetRetentionDays), cleanupBatchSize); removed > 0 {
		stats.AssetsPurged = removed
	}

	return stats, firstErr
}

// purgeOrphan 销毁单个孤儿的全部数据，最后删标记。
//
// 标记放在最后删：中途失败时留着重试锚点，下一轮还能继续。
func (s *Service) purgeOrphan(ctx context.Context, orphan *types.WorkbenchOrphan) error {
	if orphan == nil {
		return nil
	}
	// 先删工作知识库（知识/chunk/文件）；失败就直接返回，让标记留到下一轮。
	if lifecycle, ok := s.kbService.(workKBLifecycle); ok && lifecycle != nil {
		kb, err := lifecycle.GetWorkKnowledgeBase(ctx, orphan.TenantID, orphan.UserID)
		if err != nil {
			return err
		}
		if kb != nil {
			if err := lifecycle.PurgeWorkKnowledgeBase(ctx, kb.ID); err != nil {
				return err
			}
		}
	}
	if err := s.repo.PurgeOwnerData(ctx, orphan.TenantID, orphan.UserID); err != nil {
		return err
	}
	_, err := s.repo.DeleteOrphan(ctx, orphan.TenantID, orphan.UserID)
	return err
}

// warnOrphanExpiring 在清理前 7 天写一条预警；dedupe 命中时不重复插入。
func (s *Service) warnOrphanExpiring(
	ctx context.Context, orphan *types.WorkbenchOrphan,
) (bool, error) {
	if orphan == nil {
		return false, nil
	}
	payload, _ := json.Marshal(map[string]any{
		"purge_after": orphan.PurgeAfter.Format(time.RFC3339),
	})
	return s.repo.InsertNotice(ctx, &types.WorkbenchNotice{
		ID:        uuid.NewString(),
		UserID:    orphan.UserID,
		TenantID:  orphan.TenantID,
		Kind:      types.WorkbenchNoticeCleanup,
		DedupeKey: "cleanup:" + orphan.ID + ":7d",
		Payload:   payload,
		CreatedAt: time.Now().UTC(),
	})
}

// reconcileArchives 逐批对账 live 归档映射（§5.6 第 3 条）。
//
// 两种收敛：
//   - 知识已不存在 → 映射标 deleted，state 不再显示 active；
//   - pending 超过 1 小时且知识存在 → 视为“知识已建成但状态没推进”，改回 active。
func (s *Service) reconcileArchives(ctx context.Context) (int, error) {
	if s.knowledge == nil {
		// 没有知识服务时无法判断存在性，直接跳过而不是误判。
		return 0, nil
	}
	rows, err := s.repo.ListLiveArchives(ctx, cleanupBatchSize)
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, row := range rows {
		exists, err := s.knowledgeExists(ctx, row.TenantID, row.KnowledgeID)
		if err != nil {
			logger.Warnf(ctx, "workbench cleanup: check archive knowledge %s failed: %v",
				row.KnowledgeID, err)
			continue
		}
		if !exists {
			// 只收敛这一个 part：其它 part 的知识仍可能存在，不能整组标 deleted。
			if _, err := s.repo.MarkDeletedBySourcePart(
				ctx, row.TenantID, row.OwnerID, row.SourceType, row.SourceID, row.PartNo); err != nil {
				return changed, err
			}
			changed++
			continue
		}
		if row.Status == types.WorkbenchArchivePending && time.Since(row.UpdatedAt) > pendingArchiveTTL {
			if _, err := s.repo.ActivatePending(ctx, row.ID); err != nil {
				return changed, err
			}
			changed++
		}
	}
	return changed, nil
}
