package workbench

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// CreateItem 新建条目。
//
// 校验顺序：身份 → kind → title → folder 归属。任何一步失败都不落库。
func (s *Service) CreateItem(
	ctx context.Context, req *types.CreateWorkbenchItemRequest,
) (*types.WorkbenchItemSummary, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, apperrors.NewBadRequestError("request body is required")
	}
	kind := strings.TrimSpace(req.Kind)
	if kind != types.WorkbenchKindCanvas && kind != types.WorkbenchKindDatabase {
		return nil, apperrors.NewBadRequestError("kind must be canvas or database")
	}
	title, err := normalizeTitle(req.Title)
	if err != nil {
		return nil, err
	}
	if err := s.requireFolder(ctx, tenantID, ownerID, req.FolderID); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	item := &types.WorkbenchItem{
		ID:                  uuid.NewString(),
		TenantID:            tenantID,
		OwnerID:             ownerID,
		Kind:                kind,
		Title:               title,
		FolderID:            req.FolderID,
		SnapshotVersion:     types.WorkbenchSnapshotVersionInitial,
		EditorSchemaVersion: types.SupportedEditorSchemaVersion,
		Visibility:          types.WorkbenchVisibilityPrivate,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	// journal_date / current_mode 是可选的创建期字段：从日历某天新建 Journal 时，
	// 前端一次带上，省掉创建后立刻 PUT 一次。
	if err := applyJournalAndMode(item, req.JournalDate, req.CurrentMode); err != nil {
		return nil, err
	}
	if err := item.Validate(); err != nil {
		return nil, apperrors.NewBadRequestError(err.Error())
	}
	if err := s.repo.CreateItem(ctx, tenantID, ownerID, item); err != nil {
		return nil, err
	}
	return item.ToSummary(), nil
}

// applyJournalAndMode 校验并写入创建期的 journal_date / current_mode。
//
// 规则（§7.4 三态）：
//   - journal_date 缺省或 "" → 不钉日历（Valid=false，写 NULL）；
//   - "YYYY-MM-DD" → 钉到该日，必须是真实日历日，且 kind 必须是 canvas；
//   - current_mode 缺省 → 不写，读取时按 kind 兜底。
func applyJournalAndMode(item *types.WorkbenchItem, journalDate, currentMode *string) error {
	if journalDate != nil && *journalDate != "" {
		parsed, err := types.ParseDateOnly(*journalDate)
		if err != nil {
			return apperrors.NewWorkbenchInvalidField("journal_date", "YYYY-MM-DD")
		}
		// Journals 只包含画布条目（§3.5 不变量 12）。
		if item.Kind != types.WorkbenchKindCanvas {
			return apperrors.NewWorkbenchInvalidField("journal_date", "canvas items only")
		}
		item.JournalDate = parsed
	}
	if currentMode != nil && *currentMode != "" {
		if *currentMode != types.WorkbenchModePage && *currentMode != types.WorkbenchModeEdgeless {
			return apperrors.NewWorkbenchInvalidField("current_mode", "page | edgeless")
		}
		mode := *currentMode
		item.CurrentMode = &mode
	}
	return nil
}

// GetItem 读取条目元数据（不含 Y.Doc）。
func (s *Service) GetItem(ctx context.Context, id string) (*types.WorkbenchItemSummary, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	item, err := s.repo.GetItemMeta(ctx, tenantID, ownerID, id)
	if err != nil {
		return nil, mapItemError(err)
	}
	// 打开即记录 last_opened_at；失败不影响读取结果，也不阻塞响应。
	_ = s.repo.TouchLastOpened(ctx, tenantID, ownerID, id)
	return item.ToSummary(), nil
}

// UpdateItem 更新条目的元数据（标题/图标/收藏/文件夹）。
// 不做乐观锁：snapshot_version 只保护 Y.Doc 内容。
func (s *Service) UpdateItem(
	ctx context.Context, id string, req *types.UpdateWorkbenchItemRequest,
) (*types.WorkbenchItemSummary, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, apperrors.NewBadRequestError("request body is required")
	}
	// 先用 GetItemMeta 判存在性：回收站里的条目对 PUT 而言就是 404/2400。
	if _, err := s.repo.GetItemMeta(ctx, tenantID, ownerID, id); err != nil {
		return nil, mapItemError(err)
	}

	fields := make(map[string]any, 4)
	if req.Title != nil {
		title, err := normalizeTitle(*req.Title)
		if err != nil {
			return nil, err
		}
		fields["title"] = title
	}
	if req.Icon != nil {
		fields["icon"] = *req.Icon
	}
	if req.IsFavorite != nil {
		fields["is_favorite"] = *req.IsFavorite
	}
	// move_to_root 优先：同时传 folder_id 时按“移出文件夹”处理。
	if !req.MoveToRoot && req.FolderID != nil && *req.FolderID != "" {
		if err := s.requireFolder(ctx, tenantID, ownerID, req.FolderID); err != nil {
			return nil, err
		}
		fields["folder_id"] = *req.FolderID
	}
	// journal_date 三态：nil 不改；"" 取消钉（写 NULL，由仓储换成显式 NULL）；
	// "YYYY-MM-DD" 钉/改期，必须校验格式与 kind。
	if req.JournalDate != nil {
		if *req.JournalDate == "" {
			fields["journal_date"] = nil
		} else {
			parsed, err := types.ParseDateOnly(*req.JournalDate)
			if err != nil {
				return nil, apperrors.NewWorkbenchInvalidField("journal_date", "YYYY-MM-DD")
			}
			fields["journal_date"] = parsed.Date
		}
	}
	// current_mode 两态：nil 不改；合法值直接写。不提供“清回 NULL”。
	if req.CurrentMode != nil && *req.CurrentMode != "" {
		if *req.CurrentMode != types.WorkbenchModePage && *req.CurrentMode != types.WorkbenchModeEdgeless {
			return nil, apperrors.NewWorkbenchInvalidField("current_mode", "page | edgeless")
		}
		fields["current_mode"] = *req.CurrentMode
	}

	item, err := s.repo.UpdateItem(ctx, tenantID, ownerID, id, fields, req.MoveToRoot)
	if err != nil {
		return nil, mapItemError(err)
	}
	return item.ToSummary(), nil
}

// DeleteItem 删除条目。
//
// purge=false：进回收站；已在回收站返回 400（避免刷新 deleted_at 变相延长保留期）。
// purge=true：只允许删回收站里的条目，防止误传参数直接抹掉正在编辑的画布。
// 两种情况都不删知识库里的知识（归档映射的处理属 P1-4）。
func (s *Service) DeleteItem(ctx context.Context, id string, purge bool) error {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return err
	}
	if purge {
		if _, err := s.repo.GetTrashedItem(ctx, tenantID, ownerID, id); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				// 回收站里没有：若条目还活着，是「状态不允许」（400）；
				// 若彻底不存在或不属于该 owner，一律 2400，不泄露存在性。
				if _, liveErr := s.repo.GetItemMeta(ctx, tenantID, ownerID, id); liveErr == nil {
					return apperrors.NewBadRequestError("workbench item is not in trash")
				}
				return notFound("workbench item not found")
			}
			return err
		}
		// 先取资产路径再 PurgeItem：PurgeItem 会连带删掉 asset 行，
		// 之后查不到 storage_path，物理文件就永久孤儿了。
		s.purgeItemAssets(ctx, tenantID, ownerID, id)
		return mapItemError(s.repo.PurgeItem(ctx, tenantID, ownerID, id))
	}

	if _, err := s.repo.GetItemMeta(ctx, tenantID, ownerID, id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 区分「已在回收站」与「根本不存在」：前者用 400 表达状态冲突。
			if _, trashedErr := s.repo.GetTrashedItem(ctx, tenantID, ownerID, id); trashedErr == nil {
				return apperrors.NewBadRequestError("workbench item is already in trash")
			}
			return notFound("workbench item not found")
		}
		return err
	}
	return mapItemError(s.repo.TrashItem(ctx, tenantID, ownerID, id))
}

// RestoreItem 从回收站恢复。
func (s *Service) RestoreItem(ctx context.Context, id string) (*types.WorkbenchItemSummary, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	item, err := s.repo.RestoreItem(ctx, tenantID, ownerID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 能查到未删除行说明条目本来就没进回收站 → 400；否则是 2400。
			if _, liveErr := s.repo.GetItemMeta(ctx, tenantID, ownerID, id); liveErr == nil {
				return nil, apperrors.NewBadRequestError("workbench item is not in trash")
			}
			return nil, notFound("workbench item not found")
		}
		return nil, err
	}
	return item.ToSummary(), nil
}

// ListItems 分页列表，返回 (items, nextCursor)。
func (s *Service) ListItems(
	ctx context.Context, filter types.WorkbenchItemFilter,
) (*types.WorkbenchItemPage, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	items, hasMore, err := s.repo.ListItems(ctx, tenantID, ownerID, filter)
	if err != nil {
		if errors.Is(err, types.ErrWorkbenchInvalidCursor) {
			return nil, apperrors.NewBadRequestError("invalid cursor")
		}
		return nil, err
	}
	// 列表批量填充归档状态（§7.2）：一次查询取回整页映射，避免 N+1。
	// 填充失败只影响徽标展示，因此按“无归档”降级而不是让整个列表 500。
	if err := s.fillArchiveStatus(ctx, tenantID, ownerID, items); err != nil {
		logger.Warnf(ctx, "workbench: failed to fill archive status: %v", err)
	}

	page := &types.WorkbenchItemPage{Items: items}
	if page.Items == nil {
		page.Items = make([]*types.WorkbenchItem, 0)
	}
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		page.NextCursor = encodeCursor(last.UpdatedAt, last.ID)
	}
	return page, nil
}

// fillArchiveStatus 给一页条目批量填 archive_status / archived_version。
//
// 复用 snapshotState 的计算口径（含 partial / processing 判定），但只做只读聚合：
// 不写映射、不删知识，状态随时间自然收敛。
func (s *Service) fillArchiveStatus(
	ctx context.Context, tenantID uint64, ownerID string, items []*types.WorkbenchItem,
) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	grouped, err := s.repo.ListLiveBySources(ctx, tenantID, ownerID, types.WorkbenchSourceItem, ids)
	if err != nil {
		return err
	}
	for _, item := range items {
		rows := grouped[item.ID]
		if len(rows) == 0 {
			item.ArchiveStatus = archiveStatusNone
			item.ArchivedVersion = 0
			continue
		}
		item.ArchiveStatus = s.liveArchiveStatus(ctx, tenantID, rows, item.SnapshotVersion)
		item.ArchivedVersion = minSourceVersion(rows)
	}
	return nil
}

// liveArchiveStatus 与 snapshotState 同口径，但入参已经是 live 映射。
//
// currentVersion 是源内容的当前版本：条目传 snapshot_version，章节文档传
// doc_documents.version。传 0 表示没有可比对的当前版本，因此永远不会判 stale。
func (s *Service) liveArchiveStatus(
	ctx context.Context, tenantID uint64, rows []*types.WorkbenchArchive, currentVersion int,
) string {
	anyPending := false
	for _, row := range rows {
		if row.Status == types.WorkbenchArchivePending {
			anyPending = true
			break
		}
	}
	// 解析状态与知识存在性只在非 pending 时才需要查；pending 已经能确定 processing。
	if !anyPending {
		parseStatus, missing := s.partKnowledgeInfo(ctx, tenantID, rows)
		if len(missing) > 0 {
			return archiveStatusPartial
		}
		if containsParsing(parseStatus) {
			return archiveStatusProcessing
		}
	}
	if anyPending {
		return archiveStatusProcessing
	}
	if currentVersion > 0 && minSourceVersion(rows) < currentVersion {
		return archiveStatusStale
	}
	return archiveStatusActive
}

// minSourceVersion 取所有分片 source_version 的最小值（归档完成度以最落后的分片为准）。
func minSourceVersion(rows []*types.WorkbenchArchive) int {
	if len(rows) == 0 {
		return 0
	}
	min := rows[0].SourceVersion
	for _, row := range rows[1:] {
		if row.SourceVersion < min {
			min = row.SourceVersion
		}
	}
	return min
}

// requireFolder 校验文件夹属于同一 owner；不存在与越权都按 2400，不透露存在性。
func (s *Service) requireFolder(
	ctx context.Context, tenantID uint64, ownerID string, folderID *string,
) error {
	if folderID == nil || *folderID == "" {
		return nil
	}
	if _, err := s.repo.GetFolder(ctx, tenantID, ownerID, *folderID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return notFound("workbench folder not found")
		}
		return err
	}
	return nil
}

// normalizeTitle 去掉首尾空白后校验 1..200 rune。
func normalizeTitle(raw string) (string, error) {
	title := strings.TrimSpace(raw)
	if title == "" {
		return "", apperrors.NewBadRequestError("title is required")
	}
	if utf8.RuneCountInString(title) > types.WorkbenchItemTitleMaxRunes {
		return "", apperrors.NewBadRequestError("title exceeds 200 characters")
	}
	return title, nil
}

// mapItemError 把仓储错误翻译成对外错误码。
func mapItemError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return notFound("workbench item not found")
	}
	return err
}
