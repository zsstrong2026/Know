package workbench

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// memAssetRepo 是资产链路的内存仓储替身，复刻 uq_wia_item_hash 的语义：
// 同 (tenant, owner, item, hash) 的 live 行最多一条。
type memAssetRepo struct {
	interfaces.WorkbenchRepository

	item  *types.WorkbenchItem
	rows  map[string]*types.WorkbenchAsset
	order []string
}

func newMemAssetRepo() *memAssetRepo {
	return &memAssetRepo{
		item: &types.WorkbenchItem{
			ID:       "item-1",
			TenantID: 1,
			OwnerID:  "user-a",
			Kind:     types.WorkbenchKindCanvas,
			Title:    "画布",
		},
		rows: map[string]*types.WorkbenchAsset{},
	}
}

func (m *memAssetRepo) GetItemMeta(
	_ context.Context, tenantID uint64, ownerID, id string,
) (*types.WorkbenchItem, error) {
	if m.item == nil || m.item.ID != id || m.item.TenantID != tenantID || m.item.OwnerID != ownerID {
		return nil, gorm.ErrRecordNotFound
	}
	return m.item, nil
}

func (m *memAssetRepo) CreateAsset(_ context.Context, asset *types.WorkbenchAsset) error {
	for _, existing := range m.rows {
		if existing.DeletedAt != (gorm.DeletedAt{}) {
			continue
		}
		if existing.TenantID == asset.TenantID && existing.OwnerID == asset.OwnerID &&
			existing.ItemID == asset.ItemID && existing.ContentHash == asset.ContentHash &&
			asset.ContentHash != "" {
			return errors.New("duplicate content hash")
		}
	}
	m.rows[asset.ID] = asset
	m.order = append(m.order, asset.ID)
	return nil
}

func (m *memAssetRepo) GetAsset(
	_ context.Context, tenantID uint64, ownerID, id string,
) (*types.WorkbenchAsset, error) {
	row, ok := m.rows[id]
	if !ok || row.TenantID != tenantID || row.OwnerID != ownerID ||
		row.DeletedAt != (gorm.DeletedAt{}) {
		return nil, nil
	}
	return row, nil
}

func (m *memAssetRepo) GetLiveAssetByHash(
	_ context.Context, tenantID uint64, ownerID, itemID, contentHash string,
) (*types.WorkbenchAsset, error) {
	for _, id := range m.order {
		row := m.rows[id]
		if row.TenantID != tenantID || row.OwnerID != ownerID || row.ItemID != itemID {
			continue
		}
		if row.ContentHash != contentHash || row.DeletedAt != (gorm.DeletedAt{}) {
			continue
		}
		return row, nil
	}
	return nil, nil
}

func (m *memAssetRepo) ListAssets(
	_ context.Context, tenantID uint64, ownerID, itemID string,
) ([]*types.WorkbenchAsset, error) {
	out := make([]*types.WorkbenchAsset, 0, len(m.order))
	for _, id := range m.order {
		row := m.rows[id]
		if row.TenantID == tenantID && row.OwnerID == ownerID && row.ItemID == itemID &&
			row.DeletedAt == (gorm.DeletedAt{}) {
			out = append(out, row)
		}
	}
	return out, nil
}

func (m *memAssetRepo) CountAssets(
	_ context.Context, tenantID uint64, ownerID, itemID string,
) (int64, error) {
	rows, err := m.ListAssets(context.Background(), tenantID, ownerID, itemID)
	return int64(len(rows)), err
}

func (m *memAssetRepo) TouchAsset(
	_ context.Context, tenantID uint64, ownerID, id string, seenAt time.Time,
) (int64, error) {
	row, ok := m.rows[id]
	if !ok || row.TenantID != tenantID || row.OwnerID != ownerID ||
		row.DeletedAt != (gorm.DeletedAt{}) {
		return 0, nil
	}
	row.LastSeenAt = &seenAt
	return 1, nil
}

func (m *memAssetRepo) SoftDeleteAsset(
	_ context.Context, tenantID uint64, ownerID, id string,
) (int64, error) {
	row, ok := m.rows[id]
	if !ok || row.TenantID != tenantID || row.OwnerID != ownerID ||
		row.DeletedAt != (gorm.DeletedAt{}) {
		return 0, nil
	}
	row.DeletedAt = gorm.DeletedAt{Time: time.Now().UTC(), Valid: true}
	return 1, nil
}

func (m *memAssetRepo) ListStaleAssets(
	_ context.Context, cutoff time.Time, limit int,
) ([]*types.WorkbenchAsset, error) {
	out := make([]*types.WorkbenchAsset, 0, len(m.order))
	for _, id := range m.order {
		row := m.rows[id]
		if row.DeletedAt != (gorm.DeletedAt{}) {
			continue
		}
		seen := row.CreatedAt
		if row.LastSeenAt != nil {
			seen = *row.LastSeenAt
		}
		if seen.Before(cutoff) {
			out = append(out, row)
		}
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (m *memAssetRepo) ListAssetPathsByItem(
	_ context.Context, tenantID uint64, ownerID, itemID string,
) ([]string, error) {
	paths := make([]string, 0, len(m.order))
	for _, id := range m.order {
		row := m.rows[id]
		if row.TenantID == tenantID && row.OwnerID == ownerID && row.ItemID == itemID {
			paths = append(paths, row.StoragePath)
		}
	}
	return paths, nil
}

// memAssetStore 是内存存储：只记录路径，不落真实文件。
type memAssetStore struct {
	saved     map[string][]byte
	deleted   []string
	failSave  bool
	pathSeq   int
	openError error
}

func newMemAssetStore() *memAssetStore {
	return &memAssetStore{saved: map[string][]byte{}}
}

func (m *memAssetStore) Save(
	_ context.Context, tenantID uint64, namespace string, header *multipart.FileHeader,
) (string, int64, error) {
	if m.failSave {
		return "", 0, errors.New("storage down")
	}
	file, err := header.Open()
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return "", 0, err
	}
	m.pathSeq++
	path := "local://" + namespace + "/" + header.Filename
	m.saved[path] = data
	return path, int64(len(data)), nil
}

func (m *memAssetStore) Open(_ context.Context, storagePath string) (io.ReadCloser, error) {
	if m.openError != nil {
		return nil, m.openError
	}
	data, ok := m.saved[storagePath]
	if !ok {
		return nil, errors.New("not found")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *memAssetStore) Delete(_ context.Context, storagePath string) error {
	m.deleted = append(m.deleted, storagePath)
	delete(m.saved, storagePath)
	return nil
}

// newAssetFile 构造一个 multipart.FileHeader（内含真实内容）。
func newAssetFile(t *testing.T, name, contentType, body string) *multipart.FileHeader {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	headers := textproto.MIMEHeader{}
	headers.Set("Content-Disposition", `form-data; name="file"; filename="`+name+`"`)
	if contentType != "" {
		headers.Set("Content-Type", contentType)
	}
	part, err := writer.CreatePart(headers)
	require.NoError(t, err)
	_, err = part.Write([]byte(body))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	reader := multipart.NewReader(&buf, writer.Boundary())
	form, err := reader.ReadForm(int64(buf.Len()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = form.RemoveAll() })
	files := form.File["file"]
	require.Len(t, files, 1)
	return files[0]
}

func newAssetService(repo interfaces.WorkbenchRepository, store AssetStore) *Service {
	return NewService(repo).WithAssetStore(store)
}

func TestUploadAssetStoresAndDedupes(t *testing.T) {
	repo := newMemAssetRepo()
	store := newMemAssetStore()
	svc := newAssetService(repo, store)
	ctx := newTestContext()

	file := newAssetFile(t, "note.txt", "text/plain", "hello")
	first, err := svc.UploadAsset(ctx, "item-1", file)
	require.NoError(t, err)
	require.NotNil(t, first)
	assert.Equal(t, "note.txt", first.OriginalName)
	assert.Equal(t, "text/plain", first.MimeType)
	assert.EqualValues(t, 5, first.SizeBytes)
	require.NotNil(t, first.LastSeenAt, "上传即视为引用")

	// 同内容再传一次：命中哈希去重，不重复落盘、不新增行。
	second, err := svc.UploadAsset(ctx, "item-1", newAssetFile(t, "copy.txt", "text/plain", "hello"))
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID)
	assert.Len(t, store.saved, 1, "同内容只落一份")
}

func TestUploadAssetRejectsOversizedAndBlockedTypes(t *testing.T) {
	repo := newMemAssetRepo()
	svc := newAssetService(repo, newMemAssetStore())
	ctx := newTestContext()

	oversized := newAssetFile(t, "big.bin", "application/octet-stream", "x")
	oversized.Size = types.WorkbenchMaxAssetBytes + 1
	_, err := svc.UploadAsset(ctx, "item-1", oversized)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "size limit")

	_, err = svc.UploadAsset(ctx, "item-1", newAssetFile(t, "x.exe", "application/x-msdownload", "MZ"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not allowed")
}

func TestUploadAssetRollsBackFileWhenInsertFails(t *testing.T) {
	repo := newMemAssetRepo()
	store := newMemAssetStore()
	svc := newAssetService(repo, store)
	ctx := newTestContext()

	// 第一次成功。
	_, err := svc.UploadAsset(ctx, "item-1", newAssetFile(t, "a.txt", "text/plain", "same"))
	require.NoError(t, err)

	// 让 CreateAsset 必失败：把 hash 相同的旧行改成不同内容会走不到这里，
	// 因此直接用只读仓储模拟“写行失败”。
	breaking := &failingCreateRepo{memAssetRepo: repo}
	brokenSvc := newAssetService(breaking, store)
	_, err = brokenSvc.UploadAsset(ctx, "item-1", newAssetFile(t, "b.txt", "text/plain", "other"))
	require.Error(t, err)
	assert.Contains(t, store.deleted, "local://item-1/b.txt", "写行失败必须回删文件")
}

// failingCreateRepo 让 CreateAsset 恒失败，用于验证落盘回滚。
type failingCreateRepo struct {
	*memAssetRepo
}

func (f *failingCreateRepo) CreateAsset(context.Context, *types.WorkbenchAsset) error {
	return errors.New("insert failed")
}

func TestUploadAssetRequiresStore(t *testing.T) {
	svc := NewService(newMemAssetRepo())
	_, err := svc.UploadAsset(newTestContext(), "item-1",
		newAssetFile(t, "a.txt", "text/plain", "x"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

func TestOpenAssetTouchesAndSetsHeaders(t *testing.T) {
	repo := newMemAssetRepo()
	store := newMemAssetStore()
	svc := newAssetService(repo, store)
	ctx := newTestContext()

	asset, err := svc.UploadAsset(ctx, "item-1", newAssetFile(t, "a.png", "image/png", "PNGDATA"))
	require.NoError(t, err)
	// 清掉上传时写入的引用时间，验证下载会刷新。
	repo.rows[asset.ID].LastSeenAt = nil

	meta, reader, err := svc.OpenAsset(ctx, asset.ID)
	require.NoError(t, err)
	defer reader.Close()
	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, "PNGDATA", string(body))
	assert.NotNil(t, repo.rows[asset.ID].LastSeenAt, "下载即视为引用")

	// 图片 inline，文件名按 RFC 5987 传递。
	assert.Equal(t, "image/png", AssetContentTypeForHeader(meta))
	assert.True(t, strings.HasPrefix(AssetDispositionForHeader(meta), "inline"),
		"图片必须 inline 才能被 BlockSuite 直接渲染")
}

func TestOpenAssetCrossOwnerIsNotFound(t *testing.T) {
	repo := newMemAssetRepo()
	svc := newAssetService(repo, newMemAssetStore())
	asset, err := svc.UploadAsset(newTestContext(), "item-1",
		newAssetFile(t, "a.txt", "text/plain", "x"))
	require.NoError(t, err)

	otherCtx := types.WithCaller(context.Background(), types.Caller{
		TenantID: 1, UserID: "user-b", Role: types.TenantRoleOwner,
	})
	_, _, err = svc.OpenAsset(otherCtx, asset.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestDeleteAssetRequiresMatchingItem(t *testing.T) {
	repo := newMemAssetRepo()
	svc := newAssetService(repo, newMemAssetStore())
	ctx := newTestContext()
	asset, err := svc.UploadAsset(ctx, "item-1", newAssetFile(t, "a.txt", "text/plain", "x"))
	require.NoError(t, err)

	// 用别的 item 路径删除：按不存在处理，避免跨条目误删。
	err = svc.DeleteAsset(ctx, "item-2", asset.ID)
	require.Error(t, err)

	require.NoError(t, svc.DeleteAsset(ctx, "item-1", asset.ID))
	rows, err := repo.ListAssets(ctx, 1, "user-a", "item-1")
	require.NoError(t, err)
	assert.Empty(t, rows)
}

func TestTouchAssetsOnlyAffectsOwnItem(t *testing.T) {
	repo := newMemAssetRepo()
	svc := newAssetService(repo, newMemAssetStore())
	ctx := newTestContext()
	asset, err := svc.UploadAsset(ctx, "item-1", newAssetFile(t, "a.txt", "text/plain", "x"))
	require.NoError(t, err)

	touched, err := svc.TouchAssets(ctx, "item-2", []string{asset.ID})
	require.NoError(t, err)
	assert.Equal(t, 0, touched, "跨条目上报必须被忽略")

	touched, err = svc.TouchAssets(ctx, "item-1", []string{asset.ID})
	require.NoError(t, err)
	assert.Equal(t, 1, touched)
}

func TestPurgeItemAssetsDeletesFiles(t *testing.T) {
	repo := newMemAssetRepo()
	store := newMemAssetStore()
	svc := newAssetService(repo, store)
	ctx := newTestContext()
	asset, err := svc.UploadAsset(ctx, "item-1", newAssetFile(t, "a.txt", "text/plain", "x"))
	require.NoError(t, err)

	deleted := svc.purgeItemAssets(ctx, 1, "user-a", "item-1")
	assert.Equal(t, 1, deleted)
	assert.Contains(t, store.deleted, asset.StoragePath)
}

func TestCleanupStaleAssetsSoftDeletesAndRemovesFiles(t *testing.T) {
	repo := newMemAssetRepo()
	store := newMemAssetStore()
	svc := newAssetService(repo, store)
	ctx := newTestContext()

	stale, err := svc.UploadAsset(ctx, "item-1", newAssetFile(t, "old.txt", "text/plain", "old"))
	require.NoError(t, err)
	fresh, err := svc.UploadAsset(ctx, "item-1", newAssetFile(t, "new.txt", "text/plain", "new"))
	require.NoError(t, err)
	// 让 stale 早于保留期，fresh 保持当前时间。
	old := time.Now().UTC().AddDate(0, 0, -60)
	repo.rows[stale.ID].LastSeenAt = &old

	removed := svc.cleanupStaleAssets(ctx, time.Now().UTC().AddDate(0, 0, -30), 10)
	assert.Equal(t, 1, removed)
	assert.Contains(t, store.deleted, stale.StoragePath)
	assert.NotContains(t, store.deleted, fresh.StoragePath)
	// 行被软删，不再出现在 list 里。
	rows, err := repo.ListAssets(ctx, 1, "user-a", "item-1")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, fresh.ID, rows[0].ID)
}
