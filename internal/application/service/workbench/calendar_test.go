package workbench

import (
	"context"
	"testing"
	"time"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// journalRepo 只实现 Journals 需要的两个查询。
type journalRepo struct {
	interfaces.WorkbenchRepository

	items []*types.WorkbenchItem
	// 记录最后一次查询的 (start, end)，用于断言月份边界换算。
	lastStart string
	lastEnd   string
	rows      map[string][]*types.WorkbenchArchive
	err       error
}

func (j *journalRepo) ListByJournalMonth(
	_ context.Context, _ uint64, _, start, end string,
) ([]*types.WorkbenchItem, error) {
	j.lastStart, j.lastEnd = start, end
	if j.err != nil {
		return nil, j.err
	}
	return j.items, nil
}

func (j *journalRepo) ListLiveBySources(
	_ context.Context, _ uint64, _, _ string, ids []string,
) (map[string][]*types.WorkbenchArchive, error) {
	want := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		want[id] = struct{}{}
	}
	out := make(map[string][]*types.WorkbenchArchive, len(ids))
	for id, rows := range j.rows {
		if _, ok := want[id]; ok {
			out[id] = rows
		}
	}
	return out, nil
}

func journalItem(id, date string, mode *string) *types.WorkbenchItem {
	return &types.WorkbenchItem{
		ID:              id,
		TenantID:        1,
		OwnerID:         "user-a",
		Kind:            types.WorkbenchKindCanvas,
		Title:           "日记 " + id,
		JournalDate:     types.NewDateOnly(date),
		CurrentMode:     mode,
		SnapshotVersion: 2,
		UpdatedAt:       time.Date(2026, 10, 30, 1, 0, 0, 0, time.UTC),
	}
}

// 月视图：月份换算成首末日，条目按 DefaultModeForKind 兜底 current_mode。
func TestGetJournalsMonthProjectsEntries(t *testing.T) {
	pageMode := types.WorkbenchModePage
	repo := &journalRepo{items: []*types.WorkbenchItem{
		journalItem("j1", "2026-10-01", &pageMode),
		journalItem("j2", "2026-10-31", nil), // 未设置形态 → 兜底 edgeless
	}}
	svc := NewService(repo)

	result, err := svc.GetJournalsMonth(newTestContext(), "2026-02")
	require.NoError(t, err)

	assert.Equal(t, "2026-02-01", repo.lastStart, "月初必须是 01")
	assert.Equal(t, "2026-02-28", repo.lastEnd, "月末按日历计算，不能写死 31")
	assert.Equal(t, "2026-02", result.Month)
	require.Len(t, result.Entries, 2)
	assert.Equal(t, types.WorkbenchModePage, result.Entries[0].CurrentMode)
	assert.Equal(t, types.WorkbenchModeEdgeless, result.Entries[1].CurrentMode, "current_mode 不得返回空串")
	assert.False(t, result.Truncated)
}

// 闰年 2 月的月末是 29 号。
func TestGetJournalsMonthLeapYear(t *testing.T) {
	repo := &journalRepo{}
	svc := NewService(repo)

	_, err := svc.GetJournalsMonth(newTestContext(), "2024-02")
	require.NoError(t, err)
	assert.Equal(t, "2024-02-29", repo.lastEnd)
}

// month 缺省时取当前 UTC 月，而不是报错。
func TestGetJournalsMonthDefaultsToCurrentMonth(t *testing.T) {
	repo := &journalRepo{}
	svc := NewService(repo)

	result, err := svc.GetJournalsMonth(newTestContext(), "")
	require.NoError(t, err)
	assert.Equal(t, time.Now().UTC().Format("2006-01"), result.Month)
}

// 非法 month → 2411，details.field=month。
func TestGetJournalsMonthRejectsInvalidMonth(t *testing.T) {
	svc := NewService(&journalRepo{})

	for _, month := range []string{"2026-13", "202610", "2026-1", "abc"} {
		t.Run(month, func(t *testing.T) {
			_, err := svc.GetJournalsMonth(newTestContext(), month)
			appErr, ok := apperrors.IsAppError(err)
			require.True(t, ok, "必须是 AppError，got %v", err)
			assert.Equal(t, int(apperrors.ErrWorkbenchInvalidField), int(appErr.Code))
			details, _ := appErr.Details.(map[string]any)
			assert.Equal(t, "month", details["field"])
		})
	}
}

// 超过 500 条时截断并置 truncated。
func TestGetJournalsMonthTruncates(t *testing.T) {
	items := make([]*types.WorkbenchItem, 0, journalMonthMaxEntries+5)
	for i := 0; i < journalMonthMaxEntries+5; i++ {
		items = append(items, journalItem(
			"j"+time.Duration(i).String(), "2026-10-01", nil))
	}
	svc := NewService(&journalRepo{items: items})

	result, err := svc.GetJournalsMonth(newTestContext(), "2026-10")
	require.NoError(t, err)
	assert.Len(t, result.Entries, journalMonthMaxEntries)
	assert.True(t, result.Truncated)
}

// 某天：0 条是合法结果，不返回 404。
func TestGetJournalDayEmptyIsOK(t *testing.T) {
	repo := &journalRepo{items: nil}
	svc := NewService(repo)

	result, err := svc.GetJournalDay(newTestContext(), "2026-10-30")
	require.NoError(t, err)
	assert.Equal(t, "2026-10-30", result.Date.Date)
	assert.Empty(t, result.Entries)
	assert.Equal(t, "2026-10-30", repo.lastStart)
	assert.Equal(t, "2026-10-30", repo.lastEnd)
}

// 非法日期 → 2411，details.field=date。
func TestGetJournalDayRejectsInvalidDate(t *testing.T) {
	svc := NewService(&journalRepo{})
	for _, date := range []string{"2026-02-30", "2026-1-1", "20261030"} {
		t.Run(date, func(t *testing.T) {
			_, err := svc.GetJournalDay(newTestContext(), date)
			appErr, ok := apperrors.IsAppError(err)
			require.True(t, ok)
			assert.Equal(t, int(apperrors.ErrWorkbenchInvalidField), int(appErr.Code))
			details, _ := appErr.Details.(map[string]any)
			assert.Equal(t, "date", details["field"])
		})
	}
}

// 月视图批量填充归档状态：无归档 → none；有 live 映射 → 带版本。
func TestGetJournalsMonthFillsArchiveStatus(t *testing.T) {
	repo := &journalRepo{
		items: []*types.WorkbenchItem{
			journalItem("j1", "2026-10-01", nil),
			journalItem("j2", "2026-10-02", nil),
		},
		rows: map[string][]*types.WorkbenchArchive{
			"j2": {{
				ID: "row-j2", SourceID: "j2", SourceVersion: 2, PartNo: 1, PartCount: 1,
				KnowledgeID: "k-j2", Status: types.WorkbenchArchiveActive,
			}},
		},
	}
	svc := NewService(repo)

	result, err := svc.GetJournalsMonth(newTestContext(), "2026-10")
	require.NoError(t, err)
	require.Len(t, result.Entries, 2)
	// 未注入知识服务时 knowledgeExists 退化为“全部存在”，因此 j2 判为 active。
	assert.Equal(t, archiveStatusNone, result.Entries[0].ArchiveStatus)
	assert.Equal(t, archiveStatusActive, result.Entries[1].ArchiveStatus)
	assert.Equal(t, 2, result.Entries[1].ArchivedVersion)
}

// 未登录时直接 401，不查库。
func TestJournalsRequireCaller(t *testing.T) {
	svc := NewService(&journalRepo{})
	_, err := svc.GetJournalsMonth(context.Background(), "2026-10")
	require.Error(t, err)
	_, err = svc.GetJournalDay(context.Background(), "2026-10-30")
	require.Error(t, err)
}
