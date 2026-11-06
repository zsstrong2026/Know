// 工作台个人标签（P4-2）。
//
// 归属：tenant_id + owner_id（与条目/文件夹/模板一致）。Admin 与同 workspace 的其他成员
// 一律查不到、改不了——与「私有内容不可读」的产品口径保持一致。
//
// 三个约束：
//   1. 标签名去空白后 40 rune 以内，同一用户内唯一（软删后可重建同名）；
//   2. 颜色只允许白名单值，避免任意字符串流进前端样式；
//   3. 一个条目最多 20 个标签，够用且能挡住批量打标把卡片撑爆。
package workbench

import (
	"context"
	"errors"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ListTags 返回当前用户的全部标签（带条目数）。
func (s *Service) ListTags(ctx context.Context) ([]*types.WorkbenchTag, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListTags(ctx, tenantID, ownerID)
}

// CreateTag 新建标签。
func (s *Service) CreateTag(ctx context.Context, req *types.WorkbenchTagRequest) (*types.WorkbenchTag, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, apperrors.NewBadRequestError("request body is required")
	}
	req.Normalize()
	if err := req.Validate(); err != nil {
		return nil, mapTagError(err)
	}
	tag := &types.WorkbenchTag{
		ID:    uuid.NewString(),
		Name:  req.Name,
		Color: req.Color,
	}
	if err := s.repo.CreateTag(ctx, tenantID, ownerID, tag); err != nil {
		return nil, mapTagError(err)
	}
	return tag, nil
}

// UpdateTag 改名或改颜色；只提交了其中一个时另一个保持不变。
func (s *Service) UpdateTag(
	ctx context.Context, id string, req *types.WorkbenchTagRequest,
) (*types.WorkbenchTag, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, apperrors.NewBadRequestError("request body is required")
	}
	req.Normalize()
	// 名字与颜色各自独立：只传颜色时不校验名字，只改名时不改颜色
	fields := make(map[string]any, 2)
	if req.Name != "" {
		if actual := len([]rune(req.Name)); actual > types.WorkbenchTagNameMaxRunes {
			return nil, mapTagError(&types.WorkbenchAILengthError{
				Field:  "name",
				Limit:  types.WorkbenchTagNameMaxRunes,
				Actual: actual,
			})
		}
		fields["name"] = req.Name
	}
	if req.Color != "" {
		if !types.IsWorkbenchTagColor(req.Color) {
			return nil, mapTagError(types.ErrWorkbenchTagColor)
		}
		fields["color"] = req.Color
	}
	updated, err := s.repo.UpdateTag(ctx, tenantID, ownerID, id, fields)
	if err != nil {
		return nil, mapTagError(err)
	}
	return updated, nil
}

// DeleteTag 删除标签；条目本身与它的其它标签不受影响。
func (s *Service) DeleteTag(ctx context.Context, id string) error {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return err
	}
	return mapTagError(s.repo.DeleteTag(ctx, tenantID, ownerID, id))
}

// ListItemTags 某个条目上的标签。
//
// 先校验条目归属：否则拿别人的 item id 也能问出「这个条目有哪些标签」。
func (s *Service) ListItemTags(ctx context.Context, itemID string) ([]*types.WorkbenchTag, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.repo.GetItemMeta(ctx, tenantID, ownerID, itemID); err != nil {
		return nil, mapTagError(err)
	}
	return s.repo.ListTagsByItem(ctx, tenantID, ownerID, itemID)
}

// SetItemTags 覆盖式设置条目标签，返回写入后的标签列表。
func (s *Service) SetItemTags(
	ctx context.Context, itemID string, tagIDs []string,
) ([]*types.WorkbenchTag, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.repo.GetItemMeta(ctx, tenantID, ownerID, itemID); err != nil {
		return nil, mapTagError(err)
	}
	if err := s.repo.ReplaceItemTags(ctx, tenantID, ownerID, itemID, types.NormalizeItemTags(tagIDs)); err != nil {
		return nil, mapTagError(err)
	}
	return s.repo.ListTagsByItem(ctx, tenantID, ownerID, itemID)
}

// mapTagError 把仓储错误翻译成对外语义。
//
// 不存在一律 2400（不区分"没有"与"不是你的"），重名给 400 并给出可读原因。
func mapTagError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return notFound("workbench tag not found")
	case errors.Is(err, types.ErrWorkbenchTagNameDup):
		return apperrors.NewBadRequestError("tag name already exists")
	case errors.Is(err, types.ErrWorkbenchTagNameEmpty):
		return apperrors.NewBadRequestError("tag name is required")
	case errors.Is(err, types.ErrWorkbenchTagColor):
		return apperrors.NewBadRequestError("tag color is not allowed")
	}
	var lengthErr *types.WorkbenchAILengthError
	if errors.As(err, &lengthErr) {
		return apperrors.NewBadRequestError("tag name is too long")
	}
	return err
}
