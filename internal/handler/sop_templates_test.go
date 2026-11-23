package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service/sop"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// newSOPTemplateTestRouter 装配带模板服务的 SOP 路由。
func newSOPTemplateTestRouter(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	db := newSOPHandlerTestDB(t)
	lastSOPTestDB = db
	require.NoError(t, db.AutoMigrate(&types.SopTemplate{}, &types.SopTemplateRevision{}))
	tpls := sop.NewTemplateService(repository.NewSopTemplateRepo(db), repository.NewSopProjectRepo(db))
	h := NewSOPHandler(SOPHandlerDeps{
		Projects:  repository.NewSopProjectRepo(db),
		Runs:      repository.NewSopRunRepo(db),
		Templates: tpls,
	})
	// newSOPTestRouterWith 已挂项目组与事项组；这里只补模板组（同路径重复注册会 panic）。
	r := newSOPTestRouterWith(t, h)
	h.RegisterTemplateRoutes(r.Group("/api/v1/sop"))
	return r, db
}

// createTemplate 建一个可用模板并返回 id（节点配齐执行器，便于发布）。
func createTemplate(t *testing.T, r *gin.Engine, name string) string {
	t.Helper()
	body := `{"name":"` + name + `","definition":{"nodes":[{"id":"n1","name":"步骤","type":"agent",` +
		`"executor":{"kind":"platform_agent","agent_id":"a1"}}]}}`
	w := sopDoJSON(t, r, "POST", "/api/v1/sop/templates", body)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var resp struct {
		Data types.SopTemplate
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp.Data.ID
}

func TestSopTemplateCRUDAndPublish(t *testing.T) {
	r, _ := newSOPTemplateTestRouter(t)
	id := createTemplate(t, r, "标准流程")

	// 列表 + 详情
	require.Equal(t, http.StatusOK, sopDoJSON(t, r, "GET", "/api/v1/sop/templates", "").Code)
	require.Equal(t, http.StatusOK, sopDoJSON(t, r, "GET", "/api/v1/sop/templates/"+id, "").Code)

	// 保存草稿
	up := sopDoJSON(t, r, "PUT", "/api/v1/sop/templates/"+id, `{"description":"更新描述"}`)
	require.Equal(t, http.StatusOK, up.Code, up.Body.String())
	require.Contains(t, up.Body.String(), "更新描述")

	// 发布 → revision 1
	pub := sopDoJSON(t, r, "POST", "/api/v1/sop/templates/"+id+"/publish", "")
	require.Equal(t, http.StatusOK, pub.Code, pub.Body.String())
	var rev struct {
		Data types.SopTemplateRevision
	}
	require.NoError(t, json.Unmarshal(pub.Body.Bytes(), &rev))
	require.Equal(t, 1, rev.Data.Revision)

	// 发布历史 + 单条快照
	revs := sopDoJSON(t, r, "GET", "/api/v1/sop/templates/"+id+"/revisions", "")
	require.Equal(t, http.StatusOK, revs.Code)
	require.Contains(t, revs.Body.String(), `"revision":1`)
	one := sopDoJSON(t, r, "GET", "/api/v1/sop/revisions/"+rev.Data.ID, "")
	require.Equal(t, http.StatusOK, one.Code)

	// 删除
	require.Equal(t, http.StatusOK, sopDoJSON(t, r, "DELETE", "/api/v1/sop/templates/"+id, "").Code)
	require.Equal(t, http.StatusNotFound, sopDoJSON(t, r, "GET", "/api/v1/sop/templates/"+id, "").Code)
}

// 发布校验失败：400 且逐条白话（前端直接展示）。
func TestSopTemplatePublishErrorsArePlainWords(t *testing.T) {
	r, _ := newSOPTemplateTestRouter(t)
	body := `{"name":"坏流程","definition":{"nodes":[{"id":"n1","name":"实现","type":"agent",` +
		`"on_failure":"reject"}]}}`
	w := sopDoJSON(t, r, "POST", "/api/v1/sop/templates", body)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var resp struct {
		Data types.SopTemplate
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	pub := sopDoJSON(t, r, "POST", "/api/v1/sop/templates/"+resp.Data.ID+"/publish", "")
	require.Equal(t, http.StatusBadRequest, pub.Code, pub.Body.String())
	require.Contains(t, pub.Body.String(), "打回哪一步")
}

// 内置模板只读：PUT/DELETE 400 含「只读」，copy 得到非内置草稿。
func TestSopTemplateBuiltinReadonlyAndCopy(t *testing.T) {
	r, db := newSOPTemplateTestRouter(t)
	builtin := &types.SopTemplate{TenantID: 1, Name: "内置模板", IsBuiltin: true,
		Definition: types.SopDefinition{Nodes: []types.SopNode{{
			ID: "n1", Name: "内置步骤", Type: "agent",
			Executor: types.SopNodeExecutor{Kind: "platform_agent"},
		}}}}
	require.NoError(t, db.WithContext(context.Background()).Create(builtin).Error)

	put := sopDoJSON(t, r, "PUT", "/api/v1/sop/templates/"+builtin.ID, `{"name":"改名"}`)
	require.Equal(t, http.StatusBadRequest, put.Code, put.Body.String())
	require.Contains(t, put.Body.String(), "只读")

	del := sopDoJSON(t, r, "DELETE", "/api/v1/sop/templates/"+builtin.ID, "")
	require.Equal(t, http.StatusBadRequest, del.Code)
	require.Contains(t, del.Body.String(), "只读")

	cp := sopDoJSON(t, r, "POST", "/api/v1/sop/templates/"+builtin.ID+"/copy", `{}`)
	require.Equal(t, http.StatusCreated, cp.Code, cp.Body.String())
	var copied struct {
		Data types.SopTemplate
	}
	require.NoError(t, json.Unmarshal(cp.Body.Bytes(), &copied))
	require.NotEqual(t, builtin.ID, copied.Data.ID)
	require.False(t, copied.Data.IsBuiltin)
	require.Equal(t, types.SopTemplateStatusDraft, copied.Data.Status)
}

// 保存草稿时门禁命令按定义重算（不信任传入值）。
func TestSopTemplateSaveRefreshesGateCommands(t *testing.T) {
	r, db := newSOPTemplateTestRouter(t)
	id := createTemplate(t, r, "门禁流程")

	body := `{"definition":{"nodes":[{"id":"n1","name":"写产物","type":"agent",` +
		`"executor":{"kind":"platform_agent","agent_id":"a1"},` +
		`"gate_commands":["AI 乱写的"],` +
		`"artifact_outputs":[{"name":"r","path":"out/a.md","check":{"type":"exists"}}]}]}}`
	w := sopDoJSON(t, r, "PUT", "/api/v1/sop/templates/"+id, body)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	got, err := repository.NewSopTemplateRepo(db).GetByID(context.Background(), 1, id)
	require.NoError(t, err)
	require.Len(t, got.Definition.Nodes[0].GateCommands, 1)
	require.NotContains(t, got.Definition.Nodes[0].GateCommands[0], "AI 乱写的")
}
