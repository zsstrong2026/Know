package workbench

import (
	"context"
	"time"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

// journalMonthMaxEntries 与 types.WorkbenchJournalMonthMaxEntries 对齐，
// 单月超过 500 条时截断并置 truncated（§7.11.1）。
const journalMonthMaxEntries = types.WorkbenchJournalMonthMaxEntries

// GetJournalsMonth 返回某月钉了日期的画布条目（§7.11.1）。
//
// month 为空时取服务器当前月；非法格式返回 2411，details.field=month。
// 归档状态批量填充，避免前端逐日调 archives/state。
func (s *Service) GetJournalsMonth(
	ctx context.Context, month string,
) (*types.WorkbenchJournalMonth, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	if month == "" {
		month = time.Now().UTC().Format("2006-01")
	}
	start, end, err := types.ParseMonth(month)
	if err != nil {
		return nil, apperrors.NewWorkbenchInvalidField("month", "YYYY-MM")
	}

	items, err := s.repo.ListByJournalMonth(ctx, tenantID, ownerID, start, end)
	if err != nil {
		return nil, err
	}

	entries := make([]*types.WorkbenchJournalEntry, 0, len(items))
	for _, item := range items {
		entries = append(entries, toJournalEntry(item))
	}
	truncated := false
	if len(entries) > journalMonthMaxEntries {
		entries = entries[:journalMonthMaxEntries]
		truncated = true
	}
	if err := s.fillJournalArchiveStatus(ctx, tenantID, ownerID, items, entries); err != nil {
		return nil, err
	}
	return &types.WorkbenchJournalMonth{
		Month:     month,
		Entries:   entries,
		Truncated: truncated,
	}, nil
}

// GetJournalDay 返回某天的 Journal（§7.11.4）。0 条是合法结果，不是 404。
func (s *Service) GetJournalDay(
	ctx context.Context, date string,
) (*types.WorkbenchJournalDay, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	parsed, err := types.ParseDateOnly(date)
	if err != nil {
		return nil, apperrors.NewWorkbenchInvalidField("date", "YYYY-MM-DD")
	}

	items, err := s.repo.ListByJournalMonth(ctx, tenantID, ownerID, parsed.Date, parsed.Date)
	if err != nil {
		return nil, err
	}
	entries := make([]*types.WorkbenchJournalEntry, 0, len(items))
	for _, item := range items {
		entries = append(entries, toJournalEntry(item))
	}
	if err := s.fillJournalArchiveStatus(ctx, tenantID, ownerID, items, entries); err != nil {
		return nil, err
	}
	return &types.WorkbenchJournalDay{Date: parsed, Entries: entries}, nil
}

// toJournalEntry 投影为月历条目。current_mode 已兜底，不向前端暴露 null。
func toJournalEntry(item *types.WorkbenchItem) *types.WorkbenchJournalEntry {
	mode := types.DefaultModeForKind(item.Kind)
	if item.CurrentMode != nil && *item.CurrentMode != "" {
		mode = *item.CurrentMode
	}
	return &types.WorkbenchJournalEntry{
		ID:          item.ID,
		Title:       item.Title,
		Icon:        item.Icon,
		JournalDate: item.JournalDate,
		CurrentMode: mode,
		UpdatedAt:   item.UpdatedAt,
	}
}

// fillJournalArchiveStatus 把归档状态批量填进 entries。
//
// items 与 entries 一一对应（entries 是 items 的投影，可能已被截断），
// 因此只对 entries 实际包含的 id 查一次映射表，避免为截断掉的行白跑查询。
func (s *Service) fillJournalArchiveStatus(
	ctx context.Context, tenantID uint64, ownerID string,
	items []*types.WorkbenchItem, entries []*types.WorkbenchJournalEntry,
) error {
	if len(entries) == 0 {
		return nil
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}
	grouped, err := s.repo.ListLiveBySources(ctx, tenantID, ownerID, types.WorkbenchSourceItem, ids)
	if err != nil {
		return err
	}
	byID := make(map[string]*types.WorkbenchItem, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	for _, entry := range entries {
		rows := grouped[entry.ID]
		if len(rows) == 0 {
			entry.ArchiveStatus = archiveStatusNone
			entry.ArchivedVersion = 0
			continue
		}
		current := 0
		if item := byID[entry.ID]; item != nil {
			current = item.SnapshotVersion
		}
		entry.ArchiveStatus = s.liveArchiveStatus(ctx, tenantID, rows, current)
		entry.ArchivedVersion = minSourceVersion(rows)
	}
	return nil
}
