package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service/workbench"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// conflictWorkbenchRepo 恒返回版本冲突，用来验证 HTTP 层 409 的响应体契约。
// 内嵌组合接口：本用例只走 UpdateItemContent，其余方法保持 nil。
type conflictWorkbenchRepo struct {
	interfaces.WorkbenchRepository

	currentVersion int
	updatedAt      time.Time
}

func (c conflictWorkbenchRepo) UpdateItemContent(
	context.Context, uint64, string, string, int, bool, []byte, string, int,
) (int, time.Time, error) {
	return c.currentVersion, c.updatedAt, types.ErrWorkbenchVersionConflict
}

func newWorkbenchHandlerEngine(repo interfaces.WorkbenchRepository) *gin.Engine {
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
	r.POST("/items/:id/content", h.SaveContent)
	return r
}

// 保存冲突必须渲染成 HTTP 409 + code 2401 + details.current_version。
func TestWorkbenchSaveContentHandler_ConflictRenders409(t *testing.T) {
	updatedAt := time.Date(2026, 10, 29, 1, 2, 3, 0, time.UTC)
	engine := newWorkbenchHandlerEngine(conflictWorkbenchRepo{currentVersion: 7, updatedAt: updatedAt})

	body, err := json.Marshal(types.SaveWorkbenchContentRequest{
		BaseVersion:         3,
		EditorSchemaVersion: 1,
		YDoc:                "AAECAw==",
		TextContent:         "x",
		ClientID:            "tab-b",
	})
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/items/item-1/content", bytes.NewReader(body)))

	require.Equal(t, http.StatusConflict, rec.Code, "body=%s", rec.Body.String())

	var resp struct {
		Success bool `json:"success"`
		Error   struct {
			Code    int            `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.False(t, resp.Success)
	assert.Equal(t, 2401, resp.Error.Code)
	assert.EqualValues(t, 7, resp.Error.Details["current_version"], "前端靠 current_version 恢复")
	assert.NotEmpty(t, resp.Error.Details["updated_at"])
}
