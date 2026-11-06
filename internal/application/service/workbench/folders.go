package workbench

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// workbenchFolderMaxDepth 是文件夹层级上限（§7.4）。超过后拒绝新建子文件夹。
const workbenchFolderMaxDepth = 5

// folderNameMaxRunes 是文件夹名上限；规格未单列，沿用条目标题的 200。
const folderNameMaxRunes = 200

// ListFolders 返回当前用户的全部文件夹。
func (s *Service) ListFolders(ctx context.Context) ([]*types.WorkbenchFolder, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	folders, err := s.repo.ListFolders(ctx, tenantID, ownerID)
	if err != nil {
		return nil, err
	}
	if folders == nil {
		folders = make([]*types.WorkbenchFolder, 0)
	}
	return folders, nil
}

// CreateFolder 新建文件夹。parent_id 为空表示根目录，否则必须属于同一 owner
// 且层级不超过 5 层。
func (s *Service) CreateFolder(
	ctx context.Context, req *types.WorkbenchFolderRequest,
) (*types.WorkbenchFolder, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, apperrors.NewBadRequestError("request body is required")
	}
	name, err := normalizeFolderName(req.Name)
	if err != nil {
		return nil, err
	}
	if err := s.requireFolderDepth(ctx, tenantID, ownerID, req.ParentID); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	folder := &types.WorkbenchFolder{
		ID:        uuid.NewString(),
		TenantID:  tenantID,
		OwnerID:   ownerID,
		Name:      name,
		ParentID:  req.ParentID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.repo.CreateFolder(ctx, tenantID, ownerID, folder); err != nil {
		return nil, err
	}
	return folder, nil
}

// UpdateFolder 改名或换父级。parent_id 只允许非空值（移出到根目录通过删除父文件夹实现）。
func (s *Service) UpdateFolder(
	ctx context.Context, id string, req *types.WorkbenchFolderRequest,
) (*types.WorkbenchFolder, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, apperrors.NewBadRequestError("request body is required")
	}
	current, err := s.repo.GetFolder(ctx, tenantID, ownerID, id)
	if err != nil {
		return nil, mapFolderError(err)
	}
	if req.ParentID != nil && *req.ParentID == id {
		return nil, apperrors.NewBadRequestError("folder cannot be its own parent")
	}

	fields := make(map[string]any, 2)
	if name := strings.TrimSpace(req.Name); name != "" {
		normalized, err := normalizeFolderName(name)
		if err != nil {
			return nil, err
		}
		fields["name"] = normalized
	}
	if req.ParentID != nil {
		if err := s.requireFolderDepth(ctx, tenantID, ownerID, req.ParentID); err != nil {
			return nil, err
		}
		if err := s.requireNotDescendant(ctx, tenantID, ownerID, id, *req.ParentID); err != nil {
			return nil, err
		}
		fields["parent_id"] = *req.ParentID
	}

	updated, err := s.repo.UpdateFolder(ctx, tenantID, ownerID, current.ID, fields)
	if err != nil {
		return nil, mapFolderError(err)
	}
	return updated, nil
}

// MoveFolder 把文件夹移动到新的父级与位置（P4-2 拖拽排序 / 跨层移动）。
//
// position 是「目标父级下、排除自己之后」的插入下标，越界会被夹到 [0, n]。
// 顺序用从 0 开始的连续整数整层重写：拖一次就把这一层归一化，
// 不会像「只改一个值」那样越拖越乱、也不会出现两条相同 sort_order。
func (s *Service) MoveFolder(
	ctx context.Context, id string, parentID *string, position int,
) (*types.WorkbenchFolder, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	current, err := s.repo.GetFolder(ctx, tenantID, ownerID, id)
	if err != nil {
		return nil, mapFolderError(err)
	}

	target := normalizeFolderParent(parentID)
	if target != nil {
		if *target == id {
			return nil, apperrors.NewBadRequestError("folder cannot be its own parent")
		}
		if err := s.requireFolderDepth(ctx, tenantID, ownerID, target); err != nil {
			return nil, err
		}
		if err := s.requireNotDescendant(ctx, tenantID, ownerID, id, *target); err != nil {
			return nil, err
		}
	}

	// 1) 先改父级：后面的重排必须发生在新的层级里
	if !sameFolderParent(current.ParentID, target) {
		var value any
		if target != nil {
			value = *target
		}
		if _, err := s.repo.UpdateFolder(ctx, tenantID, ownerID, id, map[string]any{"parent_id": value}); err != nil {
			return nil, mapFolderError(err)
		}
	}

	// 2) 重排目标层级：ListFolders 已按层级与 sort_order 稳定输出
	folders, err := s.repo.ListFolders(ctx, tenantID, ownerID)
	if err != nil {
		return nil, err
	}
	siblings := make([]string, 0, len(folders))
	for _, folder := range folders {
		if sameFolderParent(folder.ParentID, target) {
			siblings = append(siblings, folder.ID)
		}
	}
	if err := s.repo.ReorderFolders(ctx, tenantID, ownerID, target, planFolderOrder(siblings, id, position)); err != nil {
		return nil, err
	}
	return s.repo.GetFolder(ctx, tenantID, ownerID, id)
}

// normalizeFolderParent 把空串与 nil 统一成「根层级」，避免上层传 "" 时写出空外键。
func normalizeFolderParent(parentID *string) *string {
	if parentID == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*parentID)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// sameFolderParent 比较两个父级引用，nil 与 nil 视为相同。
func sameFolderParent(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// planFolderOrder 生成插入后的完整顺序（纯函数，便于单测）。
//
// 先把 moving 从既有顺序里摘掉，再夹下标，最后插入：
//   - position < 0 表示「追加到末尾」，供「只移动层级、不关心顺序」的调用方使用；
//   - 否则夹到 [0, len(rest)]，越界即贴边。
// 这样「拖到自己原位」「拖到列表末尾」「跨层拖进空层」都是安全的。
func planFolderOrder(siblings []string, moving string, position int) []string {
	rest := make([]string, 0, len(siblings)+1)
	for _, id := range siblings {
		if id != moving {
			rest = append(rest, id)
		}
	}
	if position < 0 {
		position = len(rest)
	}
	if position > len(rest) {
		position = len(rest)
	}
	out := make([]string, 0, len(rest)+1)
	out = append(out, rest[:position]...)
	out = append(out, moving)
	out = append(out, rest[position:]...)
	return out
}

// DeleteFolder 删除文件夹：子文件夹与条目上移到父级，不级联删除。
func (s *Service) DeleteFolder(ctx context.Context, id string) error {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return err
	}
	current, err := s.repo.GetFolder(ctx, tenantID, ownerID, id)
	if err != nil {
		return mapFolderError(err)
	}
	// 先上移内容再删文件夹：顺序反了会让条目短暂指向已删文件夹。
	if err := s.repo.ReparentChildren(ctx, tenantID, ownerID, id, current.ParentID); err != nil {
		return err
	}
	if err := s.repo.MoveItemsToFolder(ctx, tenantID, ownerID, id, current.ParentID); err != nil {
		return err
	}
	return mapFolderError(s.repo.DeleteFolder(ctx, tenantID, ownerID, id))
}

// requireFolderDepth 校验目标父级存在，且新文件夹落在第 5 层以内。
func (s *Service) requireFolderDepth(
	ctx context.Context, tenantID uint64, ownerID string, parentID *string,
) error {
	if parentID == nil || *parentID == "" {
		return nil
	}
	depth, err := s.folderDepth(ctx, tenantID, ownerID, *parentID)
	if err != nil {
		return mapFolderError(err)
	}
	// 父级在第 depth 层，新建的子文件夹是第 depth+1 层。
	if depth >= workbenchFolderMaxDepth {
		return apperrors.NewBadRequestError("folder depth exceeds 5 levels")
	}
	return nil
}

// folderDepth 自底向上数层数（根为 1）。用循环而不是递归，顺带挡住脏数据造成的环。
func (s *Service) folderDepth(
	ctx context.Context, tenantID uint64, ownerID, folderID string,
) (int, error) {
	depth := 0
	seen := make(map[string]bool, workbenchFolderMaxDepth+1)
	next := folderID
	for next != "" {
		if seen[next] {
			return 0, apperrors.NewBadRequestError("folder hierarchy contains a cycle")
		}
		seen[next] = true
		folder, err := s.repo.GetFolder(ctx, tenantID, ownerID, next)
		if err != nil {
			return 0, err
		}
		depth += 1
		if depth > workbenchFolderMaxDepth {
			// 超过上限直接判失败，不必继续向上找。
			return depth, nil
		}
		if folder.ParentID == nil {
			break
		}
		next = *folder.ParentID
	}
	return depth, nil
}

// requireNotDescendant 挡住「把文件夹挂到自己或自己的子孙下面」造成的环。
func (s *Service) requireNotDescendant(
	ctx context.Context, tenantID uint64, ownerID, folderID, candidateParentID string,
) error {
	next := candidateParentID
	seen := make(map[string]bool, workbenchFolderMaxDepth+1)
	for next != "" {
		if next == folderID {
			return apperrors.NewBadRequestError("folder cannot move into its own descendant")
		}
		if seen[next] {
			return apperrors.NewBadRequestError("folder hierarchy contains a cycle")
		}
		seen[next] = true
		folder, err := s.repo.GetFolder(ctx, tenantID, ownerID, next)
		if err != nil {
			return mapFolderError(err)
		}
		if folder.ParentID == nil {
			return nil
		}
		next = *folder.ParentID
	}
	return nil
}

// normalizeFolderName 去掉首尾空白后校验 1..200 rune。
func normalizeFolderName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", apperrors.NewBadRequestError("folder name is required")
	}
	if utf8.RuneCountInString(name) > folderNameMaxRunes {
		return "", apperrors.NewBadRequestError("folder name exceeds 200 characters")
	}
	return name, nil
}

// mapFolderError 把仓储错误翻译成 2400；文件夹与条目共用“不存在”语义。
func mapFolderError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return notFound("workbench folder not found")
	}
	return err
}
