package workbench

import (
	"context"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

// callerUserID 只取用户、不要求租户（§5.7）。
//
// 通知是用户级资源：被移出最后一个 workspace 后请求上下文里没有租户，
// 但用户仍需要读到“数据保留 30 天”的提醒，因此不能复用 callerScope。
func callerUserID(ctx context.Context) (string, error) {
	userID := types.CallerFromContext(ctx).UserID
	if userID == "" {
		return "", apperrors.NewUnauthorizedError("user context is required")
	}
	if types.IsSyntheticUserID(userID) {
		// API Key 主体没有个人通知。
		return "", apperrors.NewUnauthorizedError("workbench requires a real user")
	}
	return userID, nil
}

// ListMyNotices 返回当前用户的通知与未读数（§5.7）。
//
// limit 越界时按 20 兜底，上限 100；不返回任何正文，payload 只有数量与时间。
func (s *Service) ListMyNotices(
	ctx context.Context, unreadOnly bool, limit int,
) ([]*types.WorkbenchNotice, int, error) {
	userID, err := callerUserID(ctx)
	if err != nil {
		return nil, 0, err
	}
	if limit <= 0 || limit > types.WorkbenchNoticesMaxLimit {
		limit = types.WorkbenchNoticesDefaultLimit
	}
	notices, err := s.repo.ListNotices(ctx, userID, unreadOnly, limit)
	if err != nil {
		return nil, 0, err
	}
	unread, err := s.repo.CountUnread(ctx, userID)
	if err != nil {
		return nil, 0, err
	}
	return notices, unread, nil
}

// MarkNoticeRead 标记单条已读。
//
// RowsAffected=0 时按 2400 处理：不区分“不存在”与“属于别人”，
// 否则该接口就成了探测他人通知是否存在的旁路。
func (s *Service) MarkNoticeRead(ctx context.Context, noticeID string) error {
	userID, err := callerUserID(ctx)
	if err != nil {
		return err
	}
	if noticeID == "" {
		return apperrors.NewWorkbenchInvalidField("id", "notice id")
	}
	affected, err := s.repo.MarkRead(ctx, userID, noticeID)
	if err != nil {
		return err
	}
	if affected == 0 {
		return notFound("workbench notice not found")
	}
	return nil
}

// MarkAllNoticesRead 标记全部已读；只影响自己，已经全读时返回 nil。
func (s *Service) MarkAllNoticesRead(ctx context.Context) error {
	userID, err := callerUserID(ctx)
	if err != nil {
		return err
	}
	_, err = s.repo.MarkAllRead(ctx, userID)
	return err
}

// CountMyUnreadNotices 供 Bootstrap 填充 unread_notices；失败时调用方按 0 处理。
func (s *Service) CountMyUnreadNotices(ctx context.Context, userID string) (int, error) {
	return s.repo.CountUnread(ctx, userID)
}
