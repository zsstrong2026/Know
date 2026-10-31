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
