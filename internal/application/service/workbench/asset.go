package workbench

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

// loggerWarnf 是资产路径的告警出口。
//
// 资产是“尽力而为”的附属资源：文件删失败只需要留痕，不能反过来让
// 主流程（上传/删除条目）失败，因此这里统一走一条降级日志。
func loggerWarnf(ctx context.Context, format string, args ...any) {
	log.Printf("[workbench-asset] "+format, args...)
}

// translateItemErr 把仓储错误翻译成 404；其它错误原样返回。
func translateItemErr(err error, message string) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return notFound(message)
	}
	return err
}

// AssetStore 是资产文件本体所需的最小存储能力面（§12.1）。
//
// 用窄接口而不是直接依赖 interfaces.FileService：存储实现只需这三个方法，
// 测试里可以换成内存实现，不必构造整个文件服务。
type AssetStore interface {
	// Save 落盘并返回存储路径与字节数；实现方负责按租户分目录。
	Save(
		ctx context.Context, tenantID uint64, namespace string, header *multipart.FileHeader,
	) (storagePath string, size int64, err error)
	// Open 按存储路径读取；路径由服务端从数据库行重新拼装，不接受前端传入。
	Open(ctx context.Context, storagePath string) (io.ReadCloser, error)
	// Delete 删除物理文件；文件已不存在时不应报错（清理任务需幂等）。
	Delete(ctx context.Context, storagePath string) error
}

// 允许内联展示的图片 MIME；其余一律按附件下载。
var assetInlineImageTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/webp": true,
	"image/gif":  true,
}

// 明确拒绝的类型：可执行文件与脚本。
var assetBlockedTypes = map[string]bool{
	"application/x-msdownload": true,
	"application/x-msdos-program": true,
	"application/x-sh":         true,
	"application/x-executable": true,
}

// WithAssetStore 注入资产存储，返回自身便于链式装配。
//
// 不注入时资产端点返回 503，与 WithArchiveServices 的取舍一致：
// 其余端点不受影响，P1 部署不必为了资产而必须配齐存储。
func (s *Service) WithAssetStore(store AssetStore) *Service {
	s.assetStore = store
	return s
}

// UploadAsset 上传一个文件作为条目资产（§12.1 上传流程）。
//
// 步骤固定：校验 item → 校验大小与类型 → 算哈希去重 → 落盘 → 写行。
// 任何一步失败都不留半行；落盘成功但写行失败时回删文件。
func (s *Service) UploadAsset(
	ctx context.Context, itemID string, header *multipart.FileHeader,
) (*types.WorkbenchAsset, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	if s.assetStore == nil {
		return nil, apperrors.NewServiceUnavailableError("asset storage is not configured")
	}
	if header == nil || header.Filename == "" {
		return nil, apperrors.NewValidationError("file is required")
	}
	// 1. 条目必须存在、未删除且属于当前用户。
	if _, err := s.repo.GetItemMeta(ctx, tenantID, ownerID, itemID); err != nil {
		return nil, translateItemErr(err, "workbench item not found")
	}
	// 2. 大小与类型。
	limit := int64(types.WorkbenchMaxAssetBytes)
	if header.Size > limit {
		return nil, apperrors.NewWorkbenchTooLarge(limit, header.Size)
	}
	originalName := sanitizeAssetName(header.Filename)
	contentType := detectAssetMime(header, originalName)
	if assetBlockedTypes[contentType] {
		return nil, apperrors.NewValidationError("this file type is not allowed")
	}
	// 3. 配额：单条目资产数上限。
	count, err := s.repo.CountAssets(ctx, tenantID, ownerID, itemID)
	if err != nil {
		return nil, err
	}
	if count >= int64(types.WorkbenchMaxAssetsPerItem) {
		return nil, apperrors.NewBadRequestError("asset count limit reached for this item")
	}
	// 4. 哈希去重：同一 item 下同内容只落一份。
	hash, err := hashMultipart(header)
	if err != nil {
		return nil, apperrors.NewValidationError("failed to read uploaded file").WithDetails(err.Error())
	}
	if existing, err := s.repo.GetLiveAssetByHash(ctx, tenantID, ownerID, itemID, hash); err != nil {
		return nil, err
	} else if existing != nil {
		// 命中既有行：刷新引用时间后直接返回，不重复落盘。
		_, _ = s.repo.TouchAsset(ctx, tenantID, ownerID, existing.ID, time.Now().UTC())
		return existing, nil
	}
	// 5. 落盘。namespace 用 itemID：不要用 KB ID，资产属于条目而不是知识库。
	storagePath, size, err := s.assetStore.Save(ctx, tenantID, itemID, header)
	if err != nil {
		return nil, apperrors.NewInternalServerError("failed to store asset").WithDetails(err.Error())
	}
	// 6. 写行；失败回删文件，避免留下无法追踪的孤儿。
	now := time.Now().UTC()
	asset := &types.WorkbenchAsset{
		ID:           uuid.NewString(),
		TenantID:     tenantID,
		OwnerID:      ownerID,
		ItemID:       itemID,
		StoragePath:  storagePath,
		OriginalName: originalName,
		MimeType:     contentType,
		ContentHash:  hash,
		SizeBytes:    size,
		LastSeenAt:   &now,
		CreatedAt:    now,
	}
	if err := s.repo.CreateAsset(ctx, asset); err != nil {
		if delErr := s.assetStore.Delete(ctx, storagePath); delErr != nil {
			loggerWarnf(ctx, "workbench asset: failed to rollback file %s: %v", storagePath, delErr)
		}
		// 并发上传同一文件：唯一索引冲突时读回既有行，对调用方等价于“上传成功”。
		if existing, getErr := s.repo.GetLiveAssetByHash(ctx, tenantID, ownerID, itemID, hash); getErr == nil && existing != nil {
			return existing, nil
		}
		return nil, err
	}
	return asset, nil
}

// ListAssets 返回条目的资产元数据（不含存储路径）。
func (s *Service) ListAssets(
	ctx context.Context, itemID string,
) ([]*types.WorkbenchAsset, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.repo.GetItemMeta(ctx, tenantID, ownerID, itemID); err != nil {
		return nil, translateItemErr(err, "workbench item not found")
	}
	return s.repo.ListAssets(ctx, tenantID, ownerID, itemID)
}

// OpenAsset 读取资产内容供下载；返回 (元数据, 内容, error)。
func (s *Service) OpenAsset(
	ctx context.Context, assetID string,
) (*types.WorkbenchAsset, io.ReadCloser, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, nil, err
	}
	if s.assetStore == nil {
		return nil, nil, apperrors.NewServiceUnavailableError("asset storage is not configured")
	}
	asset, err := s.repo.GetAsset(ctx, tenantID, ownerID, assetID)
	if err != nil {
		return nil, nil, err
	}
	if asset == nil {
		return nil, nil, notFound("workbench asset not found")
	}
	// 路径只从数据库行取，绝不接受前端传入，防目录穿越。
	reader, err := s.assetStore.Open(ctx, asset.StoragePath)
	if err != nil {
		return nil, nil, apperrors.NewInternalServerError("failed to read asset").WithDetails(err.Error())
	}
	// 下载即视为引用，刷新 last_seen_at，避免被清理任务误删。
	_, _ = s.repo.TouchAsset(ctx, tenantID, ownerID, asset.ID, time.Now().UTC())
	return asset, reader, nil
}

// DeleteAsset 软删资产行（§12.1）；物理文件随 item purge 或清理任务回收。
func (s *Service) DeleteAsset(ctx context.Context, itemID, assetID string) error {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return err
	}
	asset, err := s.repo.GetAsset(ctx, tenantID, ownerID, assetID)
	if err != nil {
		return err
	}
	// 资产必须属于该 item：路径里的 itemID 与行不一致时按不存在处理，
	// 避免用 A 条目的 URL 删掉 B 条目的资产。
	if asset == nil || asset.ItemID != itemID {
		return notFound("workbench asset not found")
	}
	affected, err := s.repo.SoftDeleteAsset(ctx, tenantID, ownerID, assetID)
	if err != nil {
		return err
	}
	if affected == 0 {
		return notFound("workbench asset not found")
	}
	return nil
}

// TouchAssets 批量刷新引用时间（前端打开文档后上报实际引用的 asset）。
//
// 返回实际更新的行数，调用方可忽略：刷新失败不影响编辑与保存。
func (s *Service) TouchAssets(ctx context.Context, itemID string, assetIDs []string) (int, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return 0, err
	}
	if len(assetIDs) == 0 {
		return 0, nil
	}
	if len(assetIDs) > types.WorkbenchMaxAssetsPerItem {
		return 0, apperrors.NewBadRequestError("too many asset ids in one request")
	}
	now := time.Now().UTC()
	touched := 0
	for _, assetID := range assetIDs {
		asset, err := s.repo.GetAsset(ctx, tenantID, ownerID, assetID)
		if err != nil {
			return touched, err
		}
		if asset == nil || asset.ItemID != itemID {
			continue
		}
		affected, err := s.repo.TouchAsset(ctx, tenantID, ownerID, assetID, now)
		if err != nil {
			return touched, err
		}
		touched += int(affected)
	}
	return touched, nil
}

// purgeItemAssets 在硬删条目**之前**取路径并删除物理文件。
//
// PurgeItem 会连带删掉 asset 行，因此必须先在服务层把路径取出来；
// 顺序反过来就再也拿不到 storage_path，文件永久孤儿。
// 返回删掉的文件数，失败只记日志（条目本身仍要被删掉）。
func (s *Service) purgeItemAssets(
	ctx context.Context, tenantID uint64, ownerID, itemID string,
) int {
	if s.assetStore == nil {
		return 0
	}
	paths, err := s.repo.ListAssetPathsByItem(ctx, tenantID, ownerID, itemID)
	if err != nil {
		loggerWarnf(ctx, "workbench asset: failed to list paths for item %s: %v", itemID, err)
		return 0
	}
	deleted := 0
	for _, path := range paths {
		if err := s.assetStore.Delete(ctx, path); err != nil {
			// 文件删除失败不影响条目已删的事实：留给清理任务的孤儿扫描兜底。
			loggerWarnf(ctx, "workbench asset: failed to delete file %s: %v", path, err)
			continue
		}
		deleted++
	}
	return deleted
}

// cleanupStaleAssets 回收长期未被引用的资产（清理任务的一步）。
//
// 只软删行并删物理文件，不动 item 本身。保留期默认 30 天，
// 与“删 block 不立即删 asset”的产品规则一致（§12.2 第 5 条）。
func (s *Service) cleanupStaleAssets(ctx context.Context, cutoff time.Time, limit int) int {
	rows, err := s.repo.ListStaleAssets(ctx, cutoff, limit)
	if err != nil {
		loggerWarnf(ctx, "workbench asset: list stale failed: %v", err)
		return 0
	}
	if len(rows) == 0 {
		return 0
	}
	removed := 0
	for _, row := range rows {
		affected, err := s.repo.SoftDeleteAsset(ctx, row.TenantID, row.OwnerID, row.ID)
		if err != nil {
			loggerWarnf(ctx, "workbench asset: soft delete %s failed: %v", row.ID, err)
			continue
		}
		if affected == 0 {
			continue
		}
		// 行已标记删除后才删文件：文件删失败只是浪费空间，
		// 反过来先删文件再标记失败会导致“行还在但文件没了”的破图。
		if s.assetStore != nil && row.StoragePath != "" {
			if err := s.assetStore.Delete(ctx, row.StoragePath); err != nil {
				loggerWarnf(ctx, "workbench asset: delete file %s failed: %v", row.StoragePath, err)
			}
		}
		removed++
	}
	return removed
}

// hashMultipart 计算文件内容的 SHA-256（十六进制）。
func hashMultipart(header *multipart.FileHeader) (string, error) {
	file, err := header.Open()
	if err != nil {
		return "", err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// sanitizeAssetName 只保留基础文件名，去掉路径与空字节。
func sanitizeAssetName(name string) string {
	name = strings.ReplaceAll(name, "\x00", "")
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return "file"
	}
	// 限制长度，避免超长文件名把 storage_path 撑爆。
	if len(name) > 200 {
		name = name[len(name)-200:]
	}
	return name
}

// detectAssetMime 推断 MIME：优先用 multipart 声明的类型，缺失时按扩展名，
// 两者都拿不到就落到 application/octet-stream（按附件处理）。
func detectAssetMime(header *multipart.FileHeader, name string) string {
	declared := strings.TrimSpace(header.Header.Get("Content-Type"))
	if declared != "" {
		if parsed, _, err := mime.ParseMediaType(declared); err == nil && parsed != "" {
			return parsed
		}
	}
	if byExt := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); byExt != "" {
		if parsed, _, err := mime.ParseMediaType(byExt); err == nil && parsed != "" {
			return parsed
		}
	}
	return "application/octet-stream"
}

// assetDisposition 生成 Content-Disposition：图片 inline，其余 attachment。
//
// 用 RFC 5987 的 filename* 传原始文件名，避免中文名在部分浏览器乱码。
func assetDisposition(asset *types.WorkbenchAsset) string {
	disposition := "attachment"
	if assetInlineImageTypes[asset.MimeType] {
		disposition = "inline"
	}
	return mime.FormatMediaType(disposition, map[string]string{"filename": asset.OriginalName})
}

// assetContentType 兜底：MIME 为空或不可内联时统一按二进制流，并禁止嗅探。
func assetContentType(asset *types.WorkbenchAsset) string {
	if allowedAssetContentType(asset.MimeType) {
		return asset.MimeType
	}
	return "application/octet-stream"
}

// AssetContentTypeForHeader 返回下载响应的 Content-Type（不可原样回传时降级为二进制流）。
func AssetContentTypeForHeader(asset *types.WorkbenchAsset) string {
	return assetContentType(asset)
}

// AssetDispositionForHeader 返回 Content-Disposition：图片 inline，其余 attachment。
func AssetDispositionForHeader(asset *types.WorkbenchAsset) string {
	return assetDisposition(asset)
}

// allowedAssetContentType 判断该 MIME 是否可以原样回给浏览器。
//
// 不原样回 text/html：它会在同源下被浏览器当页面执行，等于给上传者一个 XSS 入口。
func allowedAssetContentType(contentType string) bool {
	if contentType == "" {
		return false
	}
	if assetInlineImageTypes[contentType] {
		return true
	}
	if strings.HasPrefix(contentType, "text/html") ||
		strings.HasPrefix(contentType, "application/xhtml") ||
		strings.HasPrefix(contentType, "image/svg") {
		return false
	}
	return !assetBlockedTypes[contentType]
}
