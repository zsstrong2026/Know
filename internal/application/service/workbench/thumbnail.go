package workbench

import (
	"context"
	"net/http"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

// SaveItemThumbnail 保存列表卡片封面。
//
// 图片由前端在编辑器就绪/保存后截取并压缩上传，服务端只做体积与类型校验；
// version 是截取时的快照版本，旧版本不会覆盖新封面（仓储用 <= 过滤）。
func (s *Service) SaveItemThumbnail(
	ctx context.Context, itemID string, image []byte, version int,
) (int, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return 0, err
	}
	if len(image) == 0 {
		return 0, apperrors.NewValidationError("thumbnail image is required")
	}
	if len(image) > types.WorkbenchMaxThumbnailBytes {
		return 0, apperrors.NewValidationError("thumbnail image is too large")
	}
	item, err := s.repo.GetItemMeta(ctx, tenantID, ownerID, itemID)
	if err != nil {
		return 0, mapItemError(err)
	}
	// 图片只可能对应已落盘的内容；客户端传入超前版本时收敛到当前快照，
	// 避免出现“封面版本高于正文”的脏数据。
	if version < 0 || version > item.SnapshotVersion {
		version = item.SnapshotVersion
	}
	contentType := http.DetectContentType(image)
	switch contentType {
	case "image/jpeg", "image/png", "image/webp":
	default:
		return 0, apperrors.NewValidationError("unsupported thumbnail image type")
	}
	if _, err := s.repo.SaveItemThumbnail(ctx, tenantID, ownerID, itemID, image, contentType, version); err != nil {
		return 0, mapItemError(err)
	}
	return version, nil
}

// GetItemThumbnail 读取封面；没有封面时返回空字节，由 handler 转 404 让前端回退到骨架图。
func (s *Service) GetItemThumbnail(ctx context.Context, itemID string) ([]byte, string, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, "", err
	}
	image, contentType, err := s.repo.GetItemThumbnail(ctx, tenantID, ownerID, itemID)
	if err != nil {
		return nil, "", mapItemError(err)
	}
	return image, contentType, nil
}
