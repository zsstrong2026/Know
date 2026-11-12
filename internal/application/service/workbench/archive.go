package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/application/access"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// 归档计算态（§6.1）。只有 pending/active/deleted 落库，
// 其余四种都由本文件的聚合逻辑实时算出，不新增数据库列。
const (
	archiveStatusNone          = "none"
	archiveStatusProcessing    = "processing"
	archiveStatusActive        = "active"
	archiveStatusStale         = "stale"
	archiveStatusPartial       = "partial"
	archiveStatusSourceDeleted = "source_deleted"
	archiveStatusDeleted       = "deleted"
)

// GetArchiveState 计算某个 source 的归档状态。
//
// sourceType='item' 时同时校验条目归属；条目不存在 → 2400。
// 条目已进回收站 → source_deleted（快照仍保留，这是 §6.7 的核心约定）。
// sourceType='doc' 时按章节文档任务解析当前版本（§6.2）。
func (s *Service) GetArchiveState(
	ctx context.Context, sourceType, sourceID string,
) (*types.WorkbenchArchiveState, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	switch sourceType {
	case types.WorkbenchSourceItem:
		return s.itemArchiveState(ctx, tenantID, ownerID, sourceID)
	case types.WorkbenchSourceDoc:
		return s.docArchiveState(ctx, tenantID, ownerID, sourceID)
	default:
		return nil, apperrors.NewBadRequestError("unsupported archive source type")
	}
}

// itemArchiveState 是 sourceType='item' 的状态计算。
func (s *Service) itemArchiveState(
	ctx context.Context, tenantID uint64, ownerID, sourceID string,
) (*types.WorkbenchArchiveState, error) {
	item, err := s.repo.GetItemMeta(ctx, tenantID, ownerID, sourceID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 条目在回收站：如果还有 live 映射，说明快照保留着。
			if trashed, tErr := s.repo.GetTrashedItem(ctx, tenantID, ownerID, sourceID); tErr == nil && trashed != nil {
				return s.snapshotState(ctx, tenantID, ownerID, types.WorkbenchSourceItem, sourceID, nil, true)
			}
			return nil, notFound("workbench item not found")
		}
		return nil, err
	}
	version := item.SnapshotVersion
	return s.snapshotState(ctx, tenantID, ownerID, types.WorkbenchSourceItem, sourceID, &version, false)
}

// snapshotState 用来源当前版本与 live 映射算出状态。
// currentVersion 为 nil 表示无法比对版本（例如文档已被删除），此时不会判 stale。
func (s *Service) snapshotState(
	ctx context.Context,
	tenantID uint64, ownerID, sourceType, sourceID string,
	currentVersion *int, sourceDeleted bool,
) (*types.WorkbenchArchiveState, error) {
	rows, err := s.repo.ListLiveBySource(ctx, tenantID, ownerID, sourceType, sourceID)
	if err != nil {
		return nil, err
	}
	state := &types.WorkbenchArchiveState{
		SourceType: sourceType,
		SourceID:   sourceID,
		Parts:      make([]*types.WorkbenchArchivePart, 0, len(rows)),
	}
	if currentVersion != nil {
		version := *currentVersion
		state.CurrentVersion = &version
	}
	if len(rows) == 0 {
		// 没有任何 live 映射时还要区分两种情形（§7.6）：
		// 从未归档 → none；曾经归档但已被删除/随源删除 → deleted。
		total, err := s.repo.CountAnyBySource(
			ctx, tenantID, ownerID, sourceType, sourceID)
		if err != nil {
			return nil, err
		}
		switch {
		case sourceDeleted:
			state.Status = archiveStatusSourceDeleted
		case total > 0:
			state.Status = archiveStatusDeleted
		default:
			state.Status = archiveStatusNone
		}
		return state, nil
	}

	state.KBID = rows[0].KBID
	minVersion := rows[0].SourceVersion
	anyPending := false
	for _, row := range rows {
		if row.SourceVersion < minVersion {
			minVersion = row.SourceVersion
		}
		if row.Status == types.WorkbenchArchivePending {
			anyPending = true
		}
		state.Parts = append(state.Parts, &types.WorkbenchArchivePart{
			PartNo:          row.PartNo,
			PartCount:       row.PartCount,
			Title:           row.Title,
			KnowledgeID:     row.KnowledgeID,
			SourceVersion:   row.SourceVersion,
			KnowledgeExists: true,
		})
	}
	archived := minVersion
	state.ArchivedVersion = &archived
	updated := rows[0].UpdatedAt
	state.UpdatedAt = &updated

	// 用一次批量查询补齐 parse_status / knowledge_exists，避免逐 part 查库。
	// 查不到解析状态只影响展示，不影响状态机判定，因此失败只记日志。
	parseStatus, missing := s.partKnowledgeInfo(ctx, tenantID, rows)
	anyMissing := false
	for _, part := range state.Parts {
		part.ParseStatus = parseStatus[part.KnowledgeID]
		if missing[part.KnowledgeID] {
			part.KnowledgeExists = false
			anyMissing = true
		}
	}

	// 优先级（§7.6）：源已删除 > 分片缺失(partial) > 仍在处理 > 版本前进(stale) > 正常。
	if sourceDeleted {
		state.Status = archiveStatusSourceDeleted
		return state, nil
	}
	if anyMissing {
		state.Status = archiveStatusPartial
		state.StaleReason = "knowledge_missing"
		return state, nil
	}
	if anyPending || containsParsing(parseStatus) {
		state.Status = archiveStatusProcessing
		return state, nil
	}
	if currentVersion != nil && minVersion < *currentVersion {
		state.Status = archiveStatusStale
		state.StaleReason = "source_newer"
		return state, nil
	}
	state.Status = archiveStatusActive
	return state, nil
}

// partKnowledgeInfo 批量取 part 对应知识的存在性与解析状态。
// knowledgeExists 依赖知识服务的批量查询；未注入归档写依赖时退化为“全部存在”。
func (s *Service) partKnowledgeInfo(
	ctx context.Context, tenantID uint64, rows []*types.WorkbenchArchive,
) (map[string]string, map[string]bool) {
	status := make(map[string]string, len(rows))
	missing := make(map[string]bool, 0)
	if s.knowledge == nil {
		return status, missing
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.KnowledgeID != "" {
			ids = append(ids, row.KnowledgeID)
		}
	}
	if len(ids) == 0 {
		return status, missing
	}
	found, err := s.knowledge.GetKnowledgeBatch(ctx, tenantID, ids)
	if err != nil {
		logger.Warnf(ctx, "workbench: failed to load archive knowledge batch: %v", err)
		return status, missing
	}
	seen := make(map[string]struct{}, len(found))
	for _, row := range found {
		if row == nil {
			continue
		}
		seen[row.ID] = struct{}{}
		status[row.ID] = row.ParseStatus
	}
	for _, id := range ids {
		if _, ok := seen[id]; !ok {
			missing[id] = true
		}
	}
	return status, missing
}

// containsParsing 判断是否还有 part 在解析/切分/索引中。
func containsParsing(status map[string]string) bool {
	for _, value := range status {
		switch value {
		case types.ParseStatusPending, types.ParseStatusProcessing, types.ParseStatusFinalizing:
			return true
		}
	}
	return false
}

// Archive 显式归档：把 Markdown 按 H2 拆成 part 并写入工作知识库。
//
// 幂等性依赖「同 source 同 part 只留一条 live 映射」：重复调用不会产生
// 第二份知识；内容变化时才更新。
func (s *Service) Archive(
	ctx context.Context, req *types.WorkbenchArchiveRequest,
) (*types.WorkbenchArchiveState, error) {
	return s.archive(ctx, req, false)
}

// RefreshArchive 与 Archive 同路径，但强制覆盖（force=true）：
// 即使 source_version 与标题都没变，也重写知识内容。
func (s *Service) RefreshArchive(
	ctx context.Context, req *types.WorkbenchArchiveRequest,
) (*types.WorkbenchArchiveState, error) {
	return s.archive(ctx, req, true)
}

func (s *Service) archive(
	ctx context.Context, req *types.WorkbenchArchiveRequest, force bool,
) (*types.WorkbenchArchiveState, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, apperrors.NewBadRequestError("request body is required")
	}
	// 章节文档：正文由服务端渲染，前端只传 source_type/source_id（§6.2）。
	if req.SourceType == types.WorkbenchSourceDoc {
		if req.SourceID == "" {
			return nil, apperrors.NewBadRequestError("source_id is required")
		}
		return s.archiveDoc(ctx, tenantID, ownerID, req.SourceID, force || req.Force)
	}
	if req.SourceType != types.WorkbenchSourceItem {
		return nil, apperrors.NewBadRequestError("unsupported archive source type")
	}
	// 画布/数据库的导出文本由前端生成，服务端只校验并存储。
	if req.Markdown == "" {
		return nil, apperrors.NewBadRequestError("markdown is required for item archives")
	}
	if len(req.Markdown) > types.WorkbenchMaxArchiveBytes {
		return nil, apperrors.NewWorkbenchTooLarge(
			types.WorkbenchMaxArchiveBytes, int64(len(req.Markdown)))
	}

	item, err := s.repo.GetItemMeta(ctx, tenantID, ownerID, req.SourceID)
	if err != nil {
		return nil, mapItemError(err)
	}
	// 归档基于确定的内容版本：基线过期直接 409，避免把旧文本写进知识库。
	if req.BaseVersion != item.SnapshotVersion {
		return nil, apperrors.NewWorkbenchArchiveVersionStale(item.SnapshotVersion)
	}

	parts, err := splitArchiveMarkdown(item.Title, req.Markdown)
	if err != nil {
		return nil, err
	}
	src := &archiveSource{
		TenantID:      item.TenantID,
		OwnerID:       item.OwnerID,
		SourceType:    types.WorkbenchSourceItem,
		SourceID:      item.ID,
		Title:         item.Title,
		SourceVersion: item.SnapshotVersion,
	}
	// body 里的 force 与 refresh 端点等价：两者都表示“即使没变也重写”。
	return s.writeParts(ctx, src, parts, force || req.Force)
}

// archiveSource 是一次归档的确定快照：谁的内容、什么版本、叫什么。
//
// 条目与章节文档共用同一套写入算法（§6.4），差别只在来源标识与标题：
// item 用 workbench_items.snapshot_version，doc 用 doc_documents.version。
type archiveSource struct {
	TenantID      uint64
	OwnerID       string
	SourceType    string
	SourceID      string
	Title         string
	SourceVersion int
}

// writeParts 逐 part 写入映射与知识；单 part 失败不影响其他 part（§6.6），
// 但会把失败的分片号一起带出去，用户可重试，已成功的 part 不会重复建。
func (s *Service) writeParts(
	ctx context.Context,
	src *archiveSource,
	parts []archivePart,
	force bool,
) (*types.WorkbenchArchiveState, error) {
	if s.kbService == nil || s.knowledge == nil {
		return nil, apperrors.NewServiceUnavailableError(
			"workbench archive writer is not configured")
	}
	kb, err := s.kbService.EnsureWorkKnowledgeBase(ctx, src.TenantID, src.OwnerID)
	if err != nil {
		return nil, err
	}
	// 归档写入的所有者为工作 KB 的 creator；这里显式复核一次可见性，
	// 再构造 KB 级写授权，确保后续 UpdateManualKnowledge/DeleteKnowledge 能过 ACL。
	if !access.CanSeeKB(ctx, kb) {
		return nil, apperrors.NewForbiddenError("work bench knowledge base is not writable")
	}
	if ctx, err = access.WithKBTaskWrite(ctx, kb, src.TenantID); err != nil {
		return nil, apperrors.NewForbiddenError("work bench knowledge base is not writable")
	}

	total := len(parts)
	retain := make(map[int]struct{}, total)
	failed := make([]int, 0)
	for _, part := range parts {
		retain[part.No] = struct{}{}
		if err := s.archivePart(ctx, kb, src, part, total, force); err != nil {
			// 并发冲突说明另一请求正在处理整组归档，继续写只会制造更多 pending；
			// 立即中止并原样返回 2404，让前端提示「正在归档，稍后重试」。
			if appErr, ok := apperrors.IsAppError(err); ok &&
				appErr.Code == apperrors.ErrWorkbenchArchiveBusy {
				return nil, appErr
			}
			logger.Warnf(ctx, "workbench: archive part %d of %s %s failed: %v",
				part.No, src.SourceType, src.SourceID, err)
			failed = append(failed, part.No)
		}
	}
	// 新集合里不再存在的旧 part：标 deleted 并删掉对应知识（用户明确刷新，
	// 旧内容已不存在，保留只会产生重复搜索结果，§6.6）。
	if err := s.pruneStaleParts(ctx, src, retain); err != nil {
		return nil, err
	}
	if len(failed) > 0 {
		// 归档分片失败：写一条用户级通知，让用户在工作台顶部看到“归档未完成”，
		// 而不是等到下次打开条目才发现。通知失败不影响既有错误返回。
		s.NotifyArchiveFailed(ctx, src, failed)
		return nil, apperrors.NewInternalServerError("partial workbench archive failed").
			WithDetails(map[string]any{"failed_part_nos": failed})
	}
	version := src.SourceVersion
	return s.snapshotState(ctx, src.TenantID, src.OwnerID, src.SourceType, src.SourceID, &version, false)
}

// NotifyArchiveFailed 写 archive_failed 通知（§5.7）。
//
// dedupe 用来源 id + 失败分片号 + 版本：同一版本反复重试同一分片只提醒一次，
// 内容再变化（版本前进）才会再次提醒。payload 只放数量与定位信息，不含正文。
func (s *Service) NotifyArchiveFailed(
	ctx context.Context, src *archiveSource, failed []int,
) {
	if src == nil || len(failed) == 0 {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"item_count":     1,
		"failed_parts":   failed,
		"source_type":    src.SourceType,
		"source_id":      src.SourceID,
		"source_version": src.SourceVersion,
	})
	// 章节文档的通知要能直达文档：payload 带上 task id，前端据此跳转。
	if src.SourceType == types.WorkbenchSourceDoc {
		payload, _ = json.Marshal(map[string]any{
			"item_count":     1,
			"failed_parts":   failed,
			"source_type":    src.SourceType,
			"source_id":      src.SourceID,
			"source_version": src.SourceVersion,
			"task_id":        src.SourceID,
		})
	}
	dedupe := fmt.Sprintf("archive:%s:%s:%d:%v",
		src.SourceType, src.SourceID, src.SourceVersion, failed)
	if err := s.insertNotice(ctx, src.OwnerID, src.TenantID,
		types.WorkbenchNoticeArchive, dedupe, string(payload)); err != nil {
		logger.Warnf(ctx, "workbench: failed to record archive_failed notice for %s: %v",
			src.SourceID, err)
	}
}

// archiveChannel 是归档知识的采集渠道标识，便于在知识库里区分来源。
const archiveChannel = "workbench"

// archivePart 按 §6.4 的四分支写入单个分片。
//
// 顺序固定：查 live 行 → 无则先插 pending 再建知识 → 有则幂等判定后更新 →
// 知识已被删则重建。任何一步失败都不会把旧 active 行改成新版本。
func (s *Service) archivePart(
	ctx context.Context,
	kb *types.KnowledgeBase,
	src *archiveSource,
	part archivePart,
	partCount int,
	force bool,
) error {
	tenantID, ownerID := src.TenantID, src.OwnerID
	sourceVersion := src.SourceVersion
	payload := &types.ManualKnowledgePayload{
		Title:   part.Title,
		Content: part.Markdown,
		Status:  types.ManualKnowledgeStatusPublish,
	}

	row, err := s.repo.GetLiveByPart(
		ctx, tenantID, ownerID, src.SourceType, src.SourceID, part.No)
	if err != nil {
		return err
	}
	if row == nil {
		return s.createArchivePart(ctx, kb, src, part, partCount, payload)
	}
	// pending 行说明另一个请求正在为同一 part 建知识：此时既不能更新也不能
	// 重建（知识 id 已绑定但可能还没落库），直接让调用方稍后重试。
	if row.Status == types.WorkbenchArchivePending {
		return apperrors.NewWorkbenchArchiveInProgress(2 * time.Second)
	}

	exists, err := s.knowledgeExists(ctx, tenantID, row.KnowledgeID)
	if err != nil {
		return err
	}
	// 幂等：版本与标题都没变且不是强制刷新，直接复用，不重复建知识。
	if exists && row.SourceVersion == sourceVersion && row.Title == part.Title && !force {
		return nil
	}
	if exists {
		if _, err := s.knowledge.UpdateManualKnowledge(ctx, row.KnowledgeID, payload); err != nil {
			return err
		}
		return s.repo.ActivatePart(ctx, row.ID, row.KnowledgeID, sourceVersion, part.Title)
	}

	// 知识已被用户删除：显式归档/更新归档允许重建，沿用同一条映射行换绑新知识。
	newKnowledgeID := uuid.NewString()
	if _, err := s.knowledge.CreateKnowledgeFromManualWithID(
		ctx, kb.ID, newKnowledgeID, payload, archiveChannel); err != nil {
		return err
	}
	return s.repo.RebindKnowledge(ctx, row.ID, newKnowledgeID, sourceVersion, part.Title)
}

// createArchivePart 处理“该 part 还没有 live 映射”的分支。
// 先落 pending 行再建知识：唯一索引挡住并发重复，失败时把 pending 行标 deleted。
func (s *Service) createArchivePart(
	ctx context.Context,
	kb *types.KnowledgeBase,
	src *archiveSource,
	part archivePart,
	partCount int,
	payload *types.ManualKnowledgePayload,
) error {
	now := time.Now().UTC()
	pending := &types.WorkbenchArchive{
		ID:            uuid.NewString(),
		TenantID:      src.TenantID,
		OwnerID:       src.OwnerID,
		KBID:          kb.ID,
		KnowledgeID:   uuid.NewString(),
		SourceType:    src.SourceType,
		SourceID:      src.SourceID,
		SourceVersion: src.SourceVersion,
		PartNo:        part.No,
		PartCount:     partCount,
		Title:         part.Title,
		Status:        types.WorkbenchArchivePending,
		ArchivedAt:    now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	created, err := s.repo.CreatePending(ctx, pending)
	if err != nil {
		if errors.Is(err, types.ErrArchiveInProgress) {
			return apperrors.NewWorkbenchArchiveInProgress(2 * time.Second)
		}
		return err
	}
	if _, err := s.knowledge.CreateKnowledgeFromManualWithID(
		ctx, kb.ID, pending.KnowledgeID, payload, archiveChannel); err != nil {
		// 知识没建成：把 pending 行标 deleted，避免留下指向空知识的映射。
		if _, markErr := s.repo.MarkDeletedBySourcePart(
			ctx, src.TenantID, src.OwnerID, src.SourceType, src.SourceID, part.No); markErr != nil {
			logger.Warnf(ctx, "workbench: failed to roll back pending archive part %d: %v", part.No, markErr)
		}
		return err
	}
	return s.repo.ActivatePart(ctx, created.ID, pending.KnowledgeID, src.SourceVersion, part.Title)
}

// pruneStaleParts 把新集合里已不存在的旧 part 标 deleted 并删除对应知识。
func (s *Service) pruneStaleParts(
	ctx context.Context,
	src *archiveSource,
	retain map[int]struct{},
) error {
	rows, err := s.repo.ListLiveBySource(
		ctx, src.TenantID, src.OwnerID, src.SourceType, src.SourceID)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if _, ok := retain[row.PartNo]; ok {
			continue
		}
		// 先标映射再删知识：即使删知识失败，state 也不会把它算成 live，
		// 对账任务同样会收敛，不会出现“映射 active 但知识已删”的长期不一致。
		if _, err := s.repo.MarkDeletedBySourcePart(
			ctx, src.TenantID, src.OwnerID, src.SourceType, src.SourceID, row.PartNo); err != nil {
			return err
		}
		if err := s.knowledge.DeleteKnowledge(ctx, row.KnowledgeID); err != nil {
			logger.Warnf(ctx, "workbench: failed to delete stale archive knowledge %s: %v", row.KnowledgeID, err)
		}
	}
	return nil
}

// knowledgeExists 判断映射指向的知识是否仍然存在（被用户单独删除时不存在）。
func (s *Service) knowledgeExists(
	ctx context.Context, tenantID uint64, knowledgeID string,
) (bool, error) {
	if knowledgeID == "" {
		return false, nil
	}
	rows, err := s.knowledge.GetKnowledgeBatch(ctx, tenantID, []string{knowledgeID})
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if row != nil && row.ID == knowledgeID {
			return true, nil
		}
	}
	return false, nil
}

// DeleteArchive 删除归档（§7.6）。
//
// 幂等：没有 live 映射时返回 status=none 的成功结果，不报 404。
// delete_knowledge=true 时顺序固定为「先删知识、全部成功后才标映射 deleted」：
// 反过来的话第二次重试会因查不到 live 映射而直接成功，残留知识只能人工清理。
func (s *Service) DeleteArchive(
	ctx context.Context, req *types.WorkbenchArchiveDeleteRequest,
) (*types.WorkbenchArchiveState, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, apperrors.NewBadRequestError("request body is required")
	}
	if req.SourceType != types.WorkbenchSourceItem &&
		req.SourceType != types.WorkbenchSourceDoc {
		return nil, apperrors.NewBadRequestError("unsupported archive source type")
	}
	empty := func(status string) *types.WorkbenchArchiveState {
		return &types.WorkbenchArchiveState{
			SourceType: req.SourceType,
			SourceID:   req.SourceID,
			Status:     status,
			Parts:      make([]*types.WorkbenchArchivePart, 0),
		}
	}
	if req.SourceID == "" {
		return nil, apperrors.NewBadRequestError("source_id is required")
	}

	rows, err := s.repo.ListLiveBySource(ctx, tenantID, ownerID, req.SourceType, req.SourceID)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		// 幂等：没有 live 映射就直接成功，data 用 status=none（§7.6）。
		return empty(archiveStatusNone), nil
	}

	if req.DeleteKnowledge {
		if s.knowledge == nil {
			return nil, apperrors.NewServiceUnavailableError(
				"workbench archive writer is not configured")
		}
		failed := make([]int, 0)
		for _, row := range rows {
			if err := s.deleteArchiveKnowledge(ctx, tenantID, ownerID, row); err != nil {
				logger.Warnf(ctx, "workbench: delete archive knowledge %s failed: %v", row.KnowledgeID, err)
				failed = append(failed, row.PartNo)
			}
		}
		if len(failed) > 0 {
			// 映射保持原状，用户重试同一接口即可；已删掉的 part 由 state 查询收敛。
			return nil, apperrors.NewInternalServerError("failed to delete archived knowledge").
				WithDetails(map[string]any{"failed_part_nos": failed})
		}
	}

	if _, err := s.repo.MarkDeletedBySource(ctx, tenantID, ownerID, req.SourceType, req.SourceID); err != nil {
		return nil, err
	}
	return empty(archiveStatusDeleted), nil
}

// deleteArchiveKnowledge 删除单个 part 的知识。知识已被用户删掉时视为成功，
// 让「删除归档」保持幂等，不因历史残留而失败。
func (s *Service) deleteArchiveKnowledge(
	ctx context.Context, tenantID uint64, ownerID string, row *types.WorkbenchArchive,
) error {
	exists, err := s.knowledgeExists(ctx, tenantID, row.KnowledgeID)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	// 归档知识都在用户自己的工作库里，用 KB 级任务授权通过 ACL；
	// 不用 RequireKBWrite（那要求调用方持有 KB 授权上下文，归档路径没有）。
	kb, err := s.kbService.EnsureWorkKnowledgeBase(ctx, tenantID, ownerID)
	if err != nil {
		return err
	}
	if !access.CanSeeKB(ctx, kb) {
		return apperrors.NewForbiddenError("work bench knowledge base is not writable")
	}
	scoped, err := access.WithKBTaskWrite(ctx, kb, tenantID)
	if err != nil {
		return apperrors.NewForbiddenError("work bench knowledge base is not writable")
	}
	return s.knowledge.DeleteKnowledge(scoped, row.KnowledgeID)
}

// MarkAllDeletedForKB 在用户清空工作知识库后同步把映射标 deleted（§6.7）。
//
// 只按 (tenant, owner, kb) 收敛映射，不删知识、不删源内容、不写通知：
// 知识已由清空流程删除，这里只负责让 state 不再显示 active。
func (s *Service) MarkAllDeletedForKB(ctx context.Context, kbID string) (int64, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return 0, err
	}
	if kbID == "" {
		return 0, apperrors.NewBadRequestError("knowledge base id is required")
	}
	return s.repo.MarkDeletedByKB(ctx, tenantID, ownerID, kbID)
}

// MarkSourceDeleted 把某个 source 的归档映射标 deleted 但仍保留快照（§6.8）。
//
// 删除画布进回收站时调用：知识继续留在工作 KB 里可搜索，state 变成 source_deleted。
// 要连知识一起删必须显式走 DeleteArchive(delete_knowledge=true)。
func (s *Service) MarkSourceDeleted(
	ctx context.Context, sourceType, sourceID string,
) (int64, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return 0, err
	}
	if sourceType != types.WorkbenchSourceItem && sourceType != types.WorkbenchSourceDoc {
		return 0, apperrors.NewBadRequestError("unsupported archive source type")
	}
	if sourceID == "" {
		return 0, apperrors.NewBadRequestError("source id is required")
	}
	return s.repo.MarkDeletedBySource(ctx, tenantID, ownerID, sourceType, sourceID)
}
