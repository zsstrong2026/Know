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
)

// stubAIRunner 按序返回预置文本。
type stubAIRunner struct {
	texts []string
	calls []sop.AIRequest
}

func (s *stubAIRunner) Ask(_ context.Context, req sop.AIRequest) (string, string, error) {
	s.calls = append(s.calls, req)
	idx := len(s.calls) - 1
	if idx >= len(s.texts) {
		idx = len(s.texts) - 1
	}
	return s.texts[idx], "sess-ai", nil
}

// newSOPAITestRouter 装配 AI 路由（真实事项服务 + stub 问答执行器）。
func newSOPAITestRouter(t *testing.T, texts ...string) (*gin.Engine, *stubAIRunner) {
	t.Helper()
	db := newSOPHandlerTestDB(t)
	lastSOPTestDB = db
	require.NoError(t, db.AutoMigrate(&types.SopTemplate{}, &types.SopTemplateRevision{}))
	items := sop.NewItemService(repository.NewSopProjectRepo(db), repository.NewSopItemRepo(db),
		repository.NewSopItemExtraRepo(db), repository.NewSopRunRepo(db))
	runner := &stubAIRunner{texts: texts}
	ai := sop.NewAIService(runner, items, repository.NewSopItemExtraRepo(db))
	h := NewSOPHandler(SOPHandlerDeps{
		Projects: repository.NewSopProjectRepo(db),
		Runs:     repository.NewSopRunRepo(db),
		Items:    items,
		Templates: sop.NewTemplateService(repository.NewSopTemplateRepo(db),
			repository.NewSopProjectRepo(db)),
		AIService: ai, AIRunner: runner,
	})
	r := newSOPTestRouterWith(t, h)
	h.RegisterAIRoutes(r.Group("/api/v1/sop"))
	return r, runner
}

func TestSopAIBreakdownReturnsSubItems(t *testing.T) {
	r, _ := newSOPAITestRouter(t,
		`{"goal":"拆开","sub_items":[{"title":"子事项A","description":"x"}],"acceptance":"可验收"}`)
	w := sopDoJSON(t, r, "POST", "/api/v1/sop/ai/breakdown",
		`{"project_id":"p1","title":"大需求","description":"拆开看"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "子事项A")
	require.Contains(t, w.Body.String(), "可验收")

	// 缺标题 400
	require.Equal(t, http.StatusBadRequest,
		sopDoJSON(t, r, "POST", "/api/v1/sop/ai/breakdown", `{"title":"  "}`).Code)
}

// 编排非法 mode：400 且白话「模式只能是 flow 或 node」。
func TestSopAIOrchestrateInvalidMode(t *testing.T) {
	r, _ := newSOPAITestRouter(t)
	w := sopDoJSON(t, r, "POST", "/api/v1/sop/ai/orchestrate", `{"mode":"bogus","instruction":"x"}`)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "flow 或 node")
}

// flow 模式：返回草稿（draft）+ 警告，永不落库。
func TestSopAIOrchestrateFlowReturnsDraft(t *testing.T) {
	r, _ := newSOPAITestRouter(t,
		`{"nodes":[{"id":"n1","name":"分析","type":"agent","task_prompt":"看看"}],"edges":[]}`)
	w := sopDoJSON(t, r, "POST", "/api/v1/sop/ai/orchestrate",
		`{"mode":"flow","instruction":"做一个需求分析流程"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp struct {
		Data sop.FlowDraft
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "draft", resp.Data.Status)
	require.Len(t, resp.Data.Definition.Nodes, 1)
}

// node 模式：清单外的执行角色被跳过并回传原因。
func TestSopAIOrchestrateNodeValidatesInventory(t *testing.T) {
	r, _ := newSOPAITestRouter(t, `{"ops":[
		{"op":"set","node_id":"n1","fields":{"executor":{"kind":"platform_agent","agent_id":"ghost"}}}
	]}`)
	inventory := `{"agents":[{"id":"a1","name":"工程师"}]}`
	body := `{"mode":"node","instruction":"给 n1 指定工程师","inventory":` + inventory + `,
		"definition":{"nodes":[{"id":"n1","name":"实现","type":"agent"}],"edges":[]}}`
	w := sopDoJSON(t, r, "POST", "/api/v1/sop/ai/orchestrate", body)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var resp struct {
		Data sop.ApplyOpsResult
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(t, resp.Data.Applied)
	require.Len(t, resp.Data.Skipped, 1)
	require.Contains(t, resp.Data.Skipped[0].Reason, "执行角色不存在")
}

// 分析事项：返回 session_id（前端转订阅会话 SSE）。
func TestSopAIAnalyzeItemReturnsSession(t *testing.T) {
	r, _ := newSOPAITestRouter(t, "分析结果")
	db := lastSOPTestDB
	items := repository.NewSopItemRepo(db)
	it := &types.SopItem{TenantID: 1, ProjectID: "p1", No: "TP-1", Title: "需求", Status: "todo"}
	require.NoError(t, items.Create(context.Background(), it))

	w := sopDoJSON(t, r, "POST", "/api/v1/sop/ai/analyze-item", `{"item_id":"`+it.ID+`"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "sess-ai")

	// 缺 item_id 400
	require.Equal(t, http.StatusBadRequest,
		sopDoJSON(t, r, "POST", "/api/v1/sop/ai/analyze-item", `{}`).Code)
}

// 澄清生成：落库 open 且返回清单。
func TestSopAIClarifyGeneratePersists(t *testing.T) {
	r, _ := newSOPAITestRouter(t, `[{"question":"目标用户？","guess":"内部"}]`)
	db := lastSOPTestDB
	items := repository.NewSopItemRepo(db)
	it := &types.SopItem{TenantID: 1, ProjectID: "p1", No: "TP-1", Title: "需求", Status: "todo"}
	require.NoError(t, items.Create(context.Background(), it))

	w := sopDoJSON(t, r, "POST", "/api/v1/sop/ai/clarify-generate", `{"item_id":"`+it.ID+`"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "目标用户？")

	list, err := repository.NewSopItemExtraRepo(db).ListClarifications(context.Background(), 1, it.ID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, types.SopClarificationStatusOpen, list[0].Status)
}
