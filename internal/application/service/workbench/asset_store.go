package workbench

import (
	"context"
	"errors"
	"io"
	"mime/multipart"
	"os"
	"strings"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// fileServiceAssetStore 用现有 FileService 实现 AssetStore（§12.1）。
//
// 复用现有存储而不是新造一套：
//   - 落盘走 SaveFile，namespace 传 itemID，沿用 FileService 的租户分目录；
//   - 读取走 GetFile，路径由服务端从数据库行拼装，前端拿不到；
//   - 删除走 DeleteFile，并把「文件已不存在」吞掉——清理任务必须幂等。
type fileServiceAssetStore struct {
	files interfaces.FileService
}

// NewFileServiceAssetStore 把 FileService 适配成工作台资产存储。
func NewFileServiceAssetStore(files interfaces.FileService) AssetStore {
	return &fileServiceAssetStore{files: files}
}

// Save 落盘并返回存储路径与字节数。
//
// FileService 的 SaveFile 不返回字节数，这里用上传头里的 Size：
// 它与实际写入量在 gin 的 multipart 解析下一致，且避免了二次读流。
func (s *fileServiceAssetStore) Save(
	ctx context.Context, tenantID uint64, namespace string, header *multipart.FileHeader,
) (string, int64, error) {
	path, err := s.files.SaveFile(ctx, header, tenantID, namespace)
	if err != nil {
		return "", 0, err
	}
	return path, header.Size, nil
}

// Open 按存储路径读取文件。
func (s *fileServiceAssetStore) Open(ctx context.Context, storagePath string) (io.ReadCloser, error) {
	return s.files.GetFile(ctx, storagePath)
}

// Delete 删除物理文件；文件已不存在时不报错（清理任务与回滚都需要幂等）。
func (s *fileServiceAssetStore) Delete(ctx context.Context, storagePath string) error {
	if storagePath == "" {
		return nil
	}
	err := s.files.DeleteFile(ctx, storagePath)
	// 各存储实现的“文件不存在”都不是同一个 sentinel：
	// 本地后端直接返回 os.ErrNotExist 包装，对象存储多为 404 文本。
	// 这里统一按“删除成功”处理，避免重复清理时永远报错。
	if err != nil && (errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "no such file")) {
		return nil
	}
	return err
}
