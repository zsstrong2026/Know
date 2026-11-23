package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service/sop"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// newSOPItemTestRouter 组装带事项服务的 SOP 路由（真实仓储 + sqlite 内存库）。
func newSOPItemTestRouter(t *testing.T) (*gin.Engine, *sop.ItemService) {
	t.Helper()
	db := newSOPHandlerTestDB(t)
	lastSOPTestDB = db
	items := sop.NewItemService(
		repository.NewSopProjectRepo(db), repository.NewSopItemRepo(db),
		repository.NewSopItemExtraRepo(db), repository.NewSopRunRepo(db),
	)
	h := NewSOPHandler(SOPHandlerDeps{
		Projects: repository.NewSopProjectRepo(db),
		Runs:     repository.NewSopRunRepo(db),
		Items:    items,
	})
	return newSOPTestRouterWith(t, h), items
}

// createSOPItem 建一个事项并返回 id。
func createSOPItem(t *testing.T, items *sop.ItemService, projectID, title string) string {
	t.Helper()
	it, err := items.Create(context.Background(), 1, "u1", projectID, sop.CreateItemInput{Title: title})
	require.NoError(t, err)
	return it.ID
}

func TestSopItemCRUD(t *testing.T) {
	r, items := newSOPItemTestRouter(t)
	pid := createTestProject(t, r)

	// 创建：编号由服务端分配
	w := sopDoJSON(t, r, "POST", "/api/v1/sop/projects/"+pid+"/items", `{"title":"首个需求"}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var created struct {
		Data types.SopItem
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	require.Equal(t, "FX-1", created.Data.No)
	require.Equal(t, types.SopItemStatusBacklog, created.Data.Status)

	// 缺标题 400
	require.Equal(t, http.StatusBadRequest,
		sopDoJSON(t, r, "POST", "/api/v1/sop/projects/"+pid+"/items", `{}`).Code)

	// 详情
	require.Equal(t, http.StatusOK,
		sopDoJSON(t, r, "GET", "/api/v1/sop/items/"+created.Data.ID, "").Code)
	require.Equal(t, http.StatusNotFound,
		sopDoJSON(t, r, "GET", "/api/v1/sop/items/nope", "").Code)

	// 修改
	up := sopDoJSON(t, r, "PUT", "/api/v1/sop/items/"+created.Data.ID, `{"title":"改名","priority":"P0"}`)
	require.Equal(t, http.StatusOK, up.Code)
	require.Contains(t, up.Body.String(), "改名")

	// 列表 + 派生字段
	list := sopDoJSON(t, r, "GET", "/api/v1/sop/projects/"+pid+"/items", "")
	require.Equal(t, http.StatusOK, list.Code)
	require.Contains(t, list.Body.String(), `"exec_state":"none"`)

	// 单条状态：backlog → done 被拒
	bad := sopDoJSON(t, r, "PATCH", "/api/v1/sop/items/"+created.Data.ID+"/status", `{"status":"done"}`)
	require.Equal(t, http.StatusBadRequest, bad.Code)
	ok := sopDoJSON(t, r, "PATCH", "/api/v1/sop/items/"+created.Data.ID+"/status", `{"status":"todo"}`)
	require.Equal(t, http.StatusOK, ok.Code)
	require.Contains(t, ok.Body.String(), `"status":"todo"`)

	// 删除
	require.Equal(t, http.StatusOK,
		sopDoJSON(t, r, "DELETE", "/api/v1/sop/items/"+created.Data.ID, "").Code)
	require.Equal(t, http.StatusNotFound,
		sopDoJSON(t, r, "GET", "/api/v1/sop/items/"+created.Data.ID, "").Code)

	_ = items
}

// 批量迁移：执行中的事项跳过并在 skipped 里回传。
func TestSopItemBulkStatusSkipsRunning(t *testing.T) {
	r, items := newSOPItemTestRouter(t)
	db := itemsDBOf(t, r)
	pid := createTestProject(t, r)
	todo := createSOPItem(t, items, pid, "待办")
	running := createSOPItem(t, items, pid, "在跑")
	require.NoError(t, repository.NewSopRunRepo(db).CreateRun(context.Background(), &types.SopRun{
		TenantID: 1, ProjectID: pid, ItemID: running, Status: types.SopRunRunning,
	}))

	w := sopDoJSON(t, r, "POST", "/api/v1/sop/items/bulk-status",
		`{"ids":["`+todo+`","`+running+`"],"status":"cancelled"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var res struct {
		Data sop.BulkResult
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	require.Equal(t, []string{todo}, res.Data.Updated)
	require.Equal(t, []string{running}, res.Data.Skipped)

	// 空 ids 400
	require.Equal(t, http.StatusBadRequest,
		sopDoJSON(t, r, "POST", "/api/v1/sop/items/bulk-status", `{"ids":[],"status":"todo"}`).Code)
}

// 附件：上传 201、列表不回传正文。
func TestSopItemAttachmentUploadAndList(t *testing.T) {
	r, items := newSOPItemTestRouter(t)
	pid := createTestProject(t, r)
	id := createSOPItem(t, items, pid, "带附件")

	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	part, err := mw.CreateFormFile("file", "说明.md")
	require.NoError(t, err)
	_, err = part.Write([]byte("# 正文内容"))
	require.NoError(t, err)
	require.NoError(t, mw.Close())

	req := httptest.NewRequest("POST", "/api/v1/sop/items/"+id+"/attachments", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = withSOPTestIdentity(req)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), "data\":\"", "响应不得回传文件正文")

	list := sopDoJSON(t, r, "GET", "/api/v1/sop/items/"+id+"/attachments", "")
	require.Equal(t, http.StatusOK, list.Code)
	require.Contains(t, list.Body.String(), "说明.md")

	// 不支持的类型 400
	body2 := &bytes.Buffer{}
	mw2 := multipart.NewWriter(body2)
	p2, _ := mw2.CreateFormFile("file", "evil.exe")
	_, _ = p2.Write([]byte("x"))
	require.NoError(t, mw2.Close())
	req2 := httptest.NewRequest("POST", "/api/v1/sop/items/"+id+"/attachments", body2)
	req2.Header.Set("Content-Type", mw2.FormDataContentType())
	req2 = withSOPTestIdentity(req2)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusBadRequest, w2.Code)
}

// 澄清：二次答复 400（单向迁移）。
func TestSopItemClarificationOneWay(t *testing.T) {
	r, items := newSOPItemTestRouter(t)
	pid := createTestProject(t, r)
	id := createSOPItem(t, items, pid, "带澄清")

	created := sopDoJSON(t, r, "POST", "/api/v1/sop/items/"+id+"/clarifications", `{"question":"为什么做？"}`)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var cl struct {
		Data types.SopItemClarification
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &cl))
	require.Equal(t, types.SopClarificationStatusOpen, cl.Data.Status)
	require.Equal(t, "ai", cl.Data.Source)

	ans := sopDoJSON(t, r, "PATCH", "/api/v1/sop/items/"+id+"/clarifications/"+cl.Data.ID+"/answer",
		`{"answer":"因为要上线"}`)
	require.Equal(t, http.StatusOK, ans.Code, ans.Body.String())

	again := sopDoJSON(t, r, "PATCH", "/api/v1/sop/items/"+id+"/clarifications/"+cl.Data.ID+"/answer",
		`{"answer":"再答"}`)
	require.Equal(t, http.StatusBadRequest, again.Code, "二次答复必须 400")

	list := sopDoJSON(t, r, "GET", "/api/v1/sop/items/"+id+"/clarifications", "")
	require.Contains(t, list.Body.String(), `"status":"answered"`)
}

// 活动流按 kind 过滤。
func TestSopItemActivitiesKindFilter(t *testing.T) {
	r, items := newSOPItemTestRouter(t)
	pid := createTestProject(t, r)
	id := createSOPItem(t, items, pid, "带活动")

	business := sopDoJSON(t, r, "GET", "/api/v1/sop/items/"+id+"/activities?kind=business", "")
	require.Equal(t, http.StatusOK, business.Code)
	require.Contains(t, business.Body.String(), `"action":"create"`)

	audit := sopDoJSON(t, r, "GET", "/api/v1/sop/items/"+id+"/activities?kind=audit", "")
	require.Equal(t, http.StatusOK, audit.Code)
	require.NotContains(t, audit.Body.String(), `"action":"create"`)
}

// lastSOPTestDB 记住最近一次装配用的内存库（ItemService 不外露仓储，
// 个别用例要直接造运行记录）。
var lastSOPTestDB *gorm.DB

// itemsDBOf 取当前用例的内存库。
func itemsDBOf(t *testing.T, r *gin.Engine) *gorm.DB {
	t.Helper()
	require.NotNil(t, r)
	require.NotNil(t, lastSOPTestDB, "测试路由必须先经 newSOPItemTestRouter 装配")
	return lastSOPTestDB
}

// withSOPTestIdentity 给 multipart 请求补租户/用户上下文（与路由中间件同口径）。
func withSOPTestIdentity(req *http.Request) *http.Request {
	ctx := context.WithValue(req.Context(), types.TenantIDContextKey, uint64(1))
	ctx = context.WithValue(ctx, types.UserIDContextKey, "u1")
	return req.WithContext(ctx)
}
