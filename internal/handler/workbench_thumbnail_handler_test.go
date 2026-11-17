package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/service/workbench"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// thumbnailRepo 只实现缩略图读写，其余组合接口方法保持 nil。
type thumbnailRepo struct {
	interfaces.WorkbenchRepository

	item            *types.WorkbenchItem
	savedImage      []byte
	savedType       string
	savedVersion    int
	returnedContent []byte
}

func (r *thumbnailRepo) GetItemMeta(
	context.Context, uint64, string, string,
) (*types.WorkbenchItem, error) {
	return r.item, nil
}

func (r *thumbnailRepo) SaveItemThumbnail(
	_ context.Context, _ uint64, _, _ string, image []byte, contentType string, version int,
) (bool, error) {
	r.savedImage = image
	r.savedType = contentType
	r.savedVersion = version
	return true, nil
}

func (r *thumbnailRepo) GetItemThumbnail(
	context.Context, uint64, string, string,
) ([]byte, string, error) {
	return r.returnedContent, "image/jpeg", nil
}

func newThumbnailEngine(repo interfaces.WorkbenchRepository) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		ctx := types.WithCaller(c.Request.Context(), types.Caller{
			TenantID: 1, UserID: "user-a", Role: types.TenantRoleOwner,
		})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	h := NewWorkbenchItemHandler(workbench.NewService(repo))
	r.GET("/items/:id/thumbnail", h.GetThumbnail)
	r.PUT("/items/:id/thumbnail", h.SaveThumbnail)
	return r
}

// JPEG 头足够让 http.DetectContentType 识别类型，不必构造完整图片。
var jpegMagic = []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F'}

// 超前版本收敛到当前 snapshot_version，并按嗅探出的类型落库。
func TestWorkbenchSaveThumbnailHandler_ClampsVersion(t *testing.T) {
	repo := &thumbnailRepo{item: &types.WorkbenchItem{ID: "item-1", SnapshotVersion: 3}}
	engine := newThumbnailEngine(repo)

	req := httptest.NewRequest(
		http.MethodPut, "/items/item-1/thumbnail?version=9", bytes.NewReader(jpegMagic))
	req.Header.Set("Content-Type", "image/jpeg")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.Equal(t, 3, repo.savedVersion, "封面版本不得高于正文快照")
	assert.Equal(t, "image/jpeg", repo.savedType)
	assert.Equal(t, jpegMagic, repo.savedImage)
}

// 没有封面时返回 404，前端据此回退骨架封面。
func TestWorkbenchGetThumbnailHandler_EmptyIsNotFound(t *testing.T) {
	engine := newThumbnailEngine(&thumbnailRepo{})

	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/items/item-1/thumbnail", nil))

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// 有封面时按原始字节返回，并带嗅探出的 Content-Type。
func TestWorkbenchGetThumbnailHandler_ReturnsImage(t *testing.T) {
	engine := newThumbnailEngine(&thumbnailRepo{returnedContent: jpegMagic})

	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/items/item-1/thumbnail", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "image/jpeg", rec.Header().Get("Content-Type"))
	assert.Equal(t, jpegMagic, rec.Body.Bytes())
}
