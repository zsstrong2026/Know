package workbench

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// memNoticeRepo 只实现通知仓储；其余工作台方法由内嵌接口的 nil 兜底。
// 内嵌 interfaces.WorkbenchRepository 与 archive_test.go 的写法一致，
// 避免为一个测试写出 30 个空方法。
type memNoticeRepo struct {
	interfaces.WorkbenchRepository
	notices []*types.WorkbenchNotice
}

func (r *memNoticeRepo) InsertNotice(
	_ context.Context, notice *types.WorkbenchNotice,
) (bool, error) {
	if notice.DedupeKey != "" {
		for _, existing := range r.notices {
			if existing.UserID == notice.UserID && existing.DedupeKey == notice.DedupeKey {
				return false, nil
			}
		}
	}
	r.notices = append(r.notices, notice)
	return true, nil
}

func (r *memNoticeRepo) ListNotices(
	_ context.Context, userID string, unreadOnly bool, limit int,
) ([]*types.WorkbenchNotice, error) {
	rows := make([]*types.WorkbenchNotice, 0, len(r.notices))
	for i := len(r.notices) - 1; i >= 0; i-- {
		row := r.notices[i]
		if row.UserID != userID {
			continue
		}
		if unreadOnly && row.ReadAt != nil {
			continue
		}
		rows = append(rows, row)
		if len(rows) >= limit {
			break
		}
	}
	return rows, nil
}

func (r *memNoticeRepo) CountUnread(_ context.Context, userID string) (int, error) {
	count := 0
	for _, row := range r.notices {
		if row.UserID == userID && row.ReadAt == nil {
			count++
		}
	}
	return count, nil
}

func (r *memNoticeRepo) MarkRead(_ context.Context, userID, noticeID string) (int64, error) {
	now := time.Now().UTC()
	for _, row := range r.notices {
		if row.ID == noticeID && row.UserID == userID && row.ReadAt == nil {
			row.ReadAt = &now
			return 1, nil
		}
	}
	return 0, nil
}

func (r *memNoticeRepo) MarkAllRead(_ context.Context, userID string) (int64, error) {
	now := time.Now().UTC()
	var affected int64
	for _, row := range r.notices {
		if row.UserID == userID && row.ReadAt == nil {
			row.ReadAt = &now
			affected++
		}
	}
	return affected, nil
}

// seedNotice 插入一条通知并断言成功。
func seedNotice(t *testing.T, repo *memNoticeRepo, id, userID, kind, dedupe string) {
	t.Helper()
	_, err := repo.InsertNotice(context.Background(), &types.WorkbenchNotice{
		ID:        id,
		UserID:    userID,
		TenantID:  1,
		Kind:      kind,
		DedupeKey: dedupe,
		Payload:   []byte(`{}`),
		CreatedAt: time.Now().UTC(),
	})
	require.NoError(t, err)
}

// workbenchUserCtx 造一个只有用户的上下文（tenantless 通知路径）。
func workbenchUserCtx(userID string) context.Context {
	return context.WithValue(context.Background(), types.UserIDContextKey, userID)
}

// 未登录（无用户上下文）必须 401，不能返回空列表伪装成功。
func TestListMyNotices_RequiresUser(t *testing.T) {
	svc := NewService(&memNoticeRepo{})
	_, _, err := svc.ListMyNotices(context.Background(), false, 20)
	require.Error(t, err)
}

// limit 越界按默认值兜底而不是返回空。
func TestListMyNotices_ClampsLimit(t *testing.T) {
	repo := &memNoticeRepo{}
	seedNotice(t, repo, "n-1", "user-a", types.WorkbenchNoticeRemoved, "")
	seedNotice(t, repo, "n-2", "user-a", types.WorkbenchNoticeRestored, "")
	svc := NewService(repo)

	notices, unread, err := svc.ListMyNotices(workbenchUserCtx("user-a"), false, 0)
	require.NoError(t, err)
	assert.Len(t, notices, 2, "limit=0 应兜底为默认值")
	assert.Equal(t, 2, unread)

	notices, _, err = svc.ListMyNotices(workbenchUserCtx("user-a"), false, 5000)
	require.NoError(t, err)
	assert.Len(t, notices, 2, "limit 超上限时按上限处理，不报错")
}

// 标记他人通知返回 2400，且不改动未读数。
func TestMarkNoticeRead_OtherUsersNoticeIsNotFound(t *testing.T) {
	repo := &memNoticeRepo{}
	seedNotice(t, repo, "n-1", "user-a", types.WorkbenchNoticeRemoved, "")
	svc := NewService(repo)

	require.Error(t, svc.MarkNoticeRead(workbenchUserCtx("user-b"), "n-1"), "越权标记必须报错")

	unread, err := repo.CountUnread(context.Background(), "user-a")
	require.NoError(t, err)
	assert.Equal(t, 1, unread, "A 的通知仍应为未读")
}

// 全部已读只影响自己。
func TestMarkAllNoticesRead_IsolatesByUser(t *testing.T) {
	repo := &memNoticeRepo{}
	seedNotice(t, repo, "a-1", "user-a", types.WorkbenchNoticeCleanup, "")
	seedNotice(t, repo, "a-2", "user-a", types.WorkbenchNoticeCleanup, "")
	seedNotice(t, repo, "b-1", "user-b", types.WorkbenchNoticeCleanup, "")
	svc := NewService(repo)

	require.NoError(t, svc.MarkAllNoticesRead(workbenchUserCtx("user-a")))

	unreadA, err := repo.CountUnread(context.Background(), "user-a")
	require.NoError(t, err)
	assert.Zero(t, unreadA)

	unreadB, err := repo.CountUnread(context.Background(), "user-b")
	require.NoError(t, err)
	assert.Equal(t, 1, unreadB)
}

// 空 notice id 属于字段非法（2411），不应打到仓储。
func TestMarkNoticeRead_RejectsEmptyID(t *testing.T) {
	svc := NewService(&memNoticeRepo{})
	require.Error(t, svc.MarkNoticeRead(workbenchUserCtx("user-a"), ""))
}

// API Key 主体（合成用户）没有个人通知。
func TestListMyNotices_RejectsSyntheticUser(t *testing.T) {
	svc := NewService(&memNoticeRepo{})
	_, _, err := svc.ListMyNotices(workbenchUserCtx("system-10001"), false, 20)
	require.Error(t, err)
}
