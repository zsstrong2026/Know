package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
)

// newSOPTestRouter 用真实仓储 + sqlite 内存库装配最小 SOP 路由：
// 租户 1 / 用户 u1 由测试中间件注入，错误经生产 ErrorHandler 渲染包络。
func newSOPTestRouter(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	db := newSOPHandlerTestDB(t)
	h := NewSOPHandler(SOPHandlerDeps{
		Projects: repository.NewSopProjectRepo(db),
		Runs:     repository.NewSopRunRepo(db),
	})
	return newSOPTestRouterWith(t, h), db
}

func newSOPTestRouterWith(t *testing.T, h *SOPHandler) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(1))
		ctx = context.WithValue(ctx, types.UserIDContextKey, "u1")
		// 租户从 ctx 读（与生产中间件同口径）。
		req := c.Request.WithContext(ctx)
		c.Request = req
		c.Next()
	})
	sop := r.Group("/api/v1/sop")
	h.RegisterSOPRoutes(sop)
	h.RegisterItemRoutes(sop)
	h.RegisterBriefRoutes(sop)
	return r
}

func newSOPHandlerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(
		&types.SopProject{}, &types.SopItem{}, &types.SopItemAttachment{},
		&types.SopItemClarification{}, &types.SopItemActivity{},
		&types.SopRun{}, &types.SopNodeRun{},
		&types.SopProjectNote{},
	))
	require.NoError(t, db.Exec(
		"CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_notes_project_seq "+
			"ON sop_project_notes(project_id, seq) WHERE deleted_at IS NULL",
	).Error)
	return db
}

// doJSON 发一次请求并返回 recorder（空 body 时不设 Content-Type）。
func sopDoJSON(t *testing.T, r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// createTestProject 建一个项目并返回 id（测试夹具）。
func createTestProject(t *testing.T, r *gin.Engine) string {
	t.Helper()
	w := sopDoJSON(t, r, "POST", "/api/v1/sop/projects", `{"name":"夹具项目","code":"FX"}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var resp struct {
		Data types.SopProject
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp.Data.ID
}

func TestProjectCRUDAndCodeValidation(t *testing.T) {
	r, _ := newSOPTestRouter(t)
	w := sopDoJSON(t, r, "POST", "/api/v1/sop/projects",
		`{"name":"测试项目","code":"TP","repos":[{"url":"https://github.com/a/b.git"}]}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	var resp struct {
		Success bool
		Data    types.SopProject
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.True(t, resp.Success)
	require.Len(t, resp.Data.Repos, 1)
	require.Equal(t, "b", resp.Data.Repos[0].Name, "仓库名取 URL basename 去 .git")
	require.True(t, resp.Data.Repos[0].Default, "第一个仓库强制为默认")
	require.Equal(t, "u1", resp.Data.OwnerUserID, "归属人取当前用户")

	// 非法编号前缀
	w2 := sopDoJSON(t, r, "POST", "/api/v1/sop/projects", `{"name":"x","code":"1bad"}`)
	require.Equal(t, http.StatusBadRequest, w2.Code)
	require.Contains(t, w2.Body.String(), "编号前缀")

	// 缺名称
	w3 := sopDoJSON(t, r, "POST", "/api/v1/sop/projects", `{"code":"OK"}`)
	require.Equal(t, http.StatusBadRequest, w3.Code)

	// 同租户编号重复
	w4 := sopDoJSON(t, r, "POST", "/api/v1/sop/projects", `{"name":"另一个","code":"TP"}`)
	require.Equal(t, http.StatusBadRequest, w4.Code)
	require.Contains(t, w4.Body.String(), "TP")

	// 列表与详情
	list := sopDoJSON(t, r, "GET", "/api/v1/sop/projects", "")
	require.Equal(t, http.StatusOK, list.Code)
	require.Contains(t, list.Body.String(), "测试项目")
	one := sopDoJSON(t, r, "GET", "/api/v1/sop/projects/"+resp.Data.ID, "")
	require.Equal(t, http.StatusOK, one.Code)

	// 更新名称（code 不可改）
	up := sopDoJSON(t, r, "PUT", "/api/v1/sop/projects/"+resp.Data.ID, `{"name":"改名后"}`)
	require.Equal(t, http.StatusOK, up.Code)
	require.Contains(t, up.Body.String(), "改名后")
	changed := sopDoJSON(t, r, "PUT", "/api/v1/sop/projects/"+resp.Data.ID, `{"code":"XX"}`)
	require.Equal(t, http.StatusBadRequest, changed.Code, "编号创建后不可改")

	// 不存在
	missing := sopDoJSON(t, r, "GET", "/api/v1/sop/projects/nope", "")
	require.Equal(t, http.StatusNotFound, missing.Code)
}

func TestProjectDeleteGuardActiveRuns(t *testing.T) {
	r, db := newSOPTestRouter(t)
	pid := createTestProject(t, r)
	require.NoError(t, repository.NewSopRunRepo(db).CreateRun(context.Background(), &types.SopRun{
		TenantID: 1, ProjectID: pid, Status: types.SopRunRunning,
	}))

	w := sopDoJSON(t, r, "DELETE", "/api/v1/sop/projects/"+pid, "")
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "进行中")

	// 运行结束后可删除
	require.NoError(t, db.Model(&types.SopRun{}).Where("project_id = ?", pid).
		Update("status", types.SopRunSucceeded).Error)
	w = sopDoJSON(t, r, "DELETE", "/api/v1/sop/projects/"+pid, "")
	require.Equal(t, http.StatusOK, w.Code)
	gone := sopDoJSON(t, r, "GET", "/api/v1/sop/projects/"+pid, "")
	require.Equal(t, http.StatusNotFound, gone.Code)
}

func TestRepoAddRemoveDefaultImmediate(t *testing.T) {
	r, _ := newSOPTestRouter(t)
	pid := createTestProject(t, r)

	// 第一个仓库：立即可见，且自动成为默认
	first := sopDoJSON(t, r, "POST", "/api/v1/sop/projects/"+pid+"/repos",
		`{"url":"https://github.com/a/b.git"}`)
	require.Equal(t, http.StatusCreated, first.Code, first.Body.String())
	var added struct {
		Data types.SopProjectRepo
	}
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &added))
	require.True(t, added.Data.Default)

	// 第二个仓库：立即可见、不抢默认
	add := sopDoJSON(t, r, "POST", "/api/v1/sop/projects/"+pid+"/repos",
		`{"url":"git@github.com:a/c.git","branch":"dev"}`)
	require.Equal(t, http.StatusCreated, add.Code, add.Body.String())

	// 设默认：默认唯一且排首
	setDef := sopDoJSON(t, r, "PUT", "/api/v1/sop/projects/"+pid+"/repos/default", `{"name":"c"}`)
	require.Equal(t, http.StatusOK, setDef.Code, setDef.Body.String())

	got := sopDoJSON(t, r, "GET", "/api/v1/sop/projects/"+pid, "")
	var resp struct {
		Data types.SopProject
	}
	require.NoError(t, json.Unmarshal(got.Body.Bytes(), &resp))
	require.Len(t, resp.Data.Repos, 2)
	require.Equal(t, "c", resp.Data.Repos[0].Name, "默认仓库排首位")
	require.True(t, resp.Data.Repos[0].Default)
	require.False(t, resp.Data.Repos[1].Default, "默认必须唯一")

	// 删除默认：默认顺延到剩余第一个
	del := sopDoJSON(t, r, "DELETE", "/api/v1/sop/projects/"+pid+"/repos/"+resp.Data.Repos[0].ID, "")
	require.Equal(t, http.StatusOK, del.Code, del.Body.String())
	got = sopDoJSON(t, r, "GET", "/api/v1/sop/projects/"+pid, "")
	require.NoError(t, json.Unmarshal(got.Body.Bytes(), &resp))
	require.Len(t, resp.Data.Repos, 1)
	require.True(t, resp.Data.Repos[0].Default)

	// 重复绑定同一 URL（b 仍在列表里）
	dup := sopDoJSON(t, r, "POST", "/api/v1/sop/projects/"+pid+"/repos",
		`{"url":"https://github.com/a/b.git"}`)
	require.Equal(t, http.StatusBadRequest, dup.Code)

	// 非法 URL
	bad := sopDoJSON(t, r, "POST", "/api/v1/sop/projects/"+pid+"/repos", `{"url":"not a url"}`)
	require.Equal(t, http.StatusBadRequest, bad.Code)

	// 默认仓库不存在
	missing := sopDoJSON(t, r, "PUT", "/api/v1/sop/projects/"+pid+"/repos/default", `{"name":"nope"}`)
	require.Equal(t, http.StatusNotFound, missing.Code)
}

// 未解析出租户时必须 400：仓储把 tenantID==0 当跨租户直通，HTTP 入口绝不能转发 0。
func TestSOPRejectsMissingTenant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newSOPHandlerTestDB(t)
	h := NewSOPHandler(SOPHandlerDeps{
		Projects: repository.NewSopProjectRepo(db),
		Runs:     repository.NewSopRunRepo(db),
	})
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	h.RegisterSOPRoutes(r.Group("/api/v1/sop"))
	h.RegisterItemRoutes(r.Group("/api/v1/sop"))

	w := sopDoJSON(t, r, "GET", "/api/v1/sop/projects", "")
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "租户")
}

// 同一租户内项目列表只回自己的；别的租户看不到。
func TestProjectListScopedByTenant(t *testing.T) {
	r, db := newSOPTestRouter(t)
	createTestProject(t, r)
	require.NoError(t, db.Create(&types.SopProject{
		TenantID: 2, Name: "别家项目", Code: "OT", OwnerUserID: "u2",
	}).Error)

	w := sopDoJSON(t, r, "GET", "/api/v1/sop/projects", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "夹具项目")
	require.NotContains(t, w.Body.String(), "别家项目")
	_ = errors.NewBadRequestError // 保持 errors 包的显式依赖（包络由 ErrorHandler 渲染）
}
