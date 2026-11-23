package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service/sop"
	"github.com/Tencent/WeKnora/internal/application/service/sop/executor"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// newSOPRunTestRouter 装配带运行服务的 SOP 路由（事件总线独立一份，便于断言）。
func newSOPRunTestRouter(t *testing.T) (*gin.Engine, *gorm.DB, *sop.EventHub) {
	t.Helper()
	db := newSOPHandlerTestDB(t)
	lastSOPTestDB = db
	require.NoError(t, db.AutoMigrate(&types.SopTemplate{}, &types.SopTemplateRevision{}, &types.SopArtifact{}))
	hub := sop.NewEventHub()
	h := NewSOPHandler(SOPHandlerDeps{
		Projects: repository.NewSopProjectRepo(db),
		Runs:     repository.NewSopRunRepo(db),
		Items: sop.NewItemService(repository.NewSopProjectRepo(db), repository.NewSopItemRepo(db),
			repository.NewSopItemExtraRepo(db), repository.NewSopRunRepo(db)),
		Templates: sop.NewTemplateService(repository.NewSopTemplateRepo(db), repository.NewSopProjectRepo(db)),
		RunSvc: sop.NewRunService(repository.NewSopTemplateRepo(db), repository.NewSopRunRepo(db),
			repository.NewSopProjectRepo(db), repository.NewSopItemRepo(db)),
		Hub: hub,
	})
	r := newSOPTestRouterWith(t, h)
	runs := r.Group("/api/v1/sop/runs")
	{
		runs.POST("", h.CreateRun)
		runs.GET("", h.ListRuns)
		runs.GET("/stats/summary", h.RunStats)
		runs.GET("/:id", h.GetRun)
		runs.GET("/:id/events", h.RunEvents)
		runs.GET("/:id/artifacts", h.ListRunArtifacts)
		runs.GET("/:id/artifacts/:aid/file", h.DownloadArtifact)
		runs.GET("/:id/nodes/:nid/logs", h.NodeLogs)
	}
	return r, db, hub
}

// seedRunTemplate 建已发布模板（单 agent 节点）。
func seedRunTemplate(t *testing.T, db *gorm.DB, schema []string) *types.SopTemplate {
	t.Helper()
	ctx := context.Background()
	repo := repository.NewSopTemplateRepo(db)
	tpl := &types.SopTemplate{TenantID: 1, Name: "流程", Definition: types.SopDefinition{
		InputSchema: schema,
		Nodes: []types.SopNode{{
			ID: "n1", Name: "实现", Type: "agent",
			Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"},
		}},
	}}
	require.NoError(t, repo.Create(ctx, tpl))
	_, err := repo.Publish(ctx, 1, tpl.ID, "u1")
	require.NoError(t, err)
	got, err := repo.GetByID(ctx, 1, tpl.ID)
	require.NoError(t, err)
	return got
}

func TestCreateRunRequiresPublishedTemplate(t *testing.T) {
	r, db, _ := newSOPRunTestRouter(t)
	draft := &types.SopTemplate{TenantID: 1, Name: "draft",
		Definition: types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: "agent"}}}}
	require.NoError(t, repository.NewSopTemplateRepo(db).Create(context.Background(), draft))

	w := sopDoJSON(t, r, "POST", "/api/v1/sop/runs", fmt.Sprintf(`{"template_id":%q}`, draft.ID))
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "尚未发布")
}

func TestCreateRunSnapshotsRevision(t *testing.T) {
	r, db, _ := newSOPRunTestRouter(t)
	tpl := seedRunTemplate(t, db, nil)
	w := sopDoJSON(t, r, "POST", "/api/v1/sop/runs", fmt.Sprintf(`{"template_id":%q}`, tpl.ID))
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	var resp struct {
		Data types.SopRun
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, types.SopRunQueued, resp.Data.Status)

	// 发布后改草稿不影响运行快照
	tpl.Definition.Nodes[0].Name = "改了"
	require.NoError(t, repository.NewSopTemplateRepo(db).Update(context.Background(), tpl))
	got, err := repository.NewSopRunRepo(db).GetRun(context.Background(), 1, resp.Data.ID)
	require.NoError(t, err)
	require.Equal(t, "实现", got.WorkflowSnapshot.Nodes[0].Name)

	// 详情聚合
	detail := sopDoJSON(t, r, "GET", "/api/v1/sop/runs/"+resp.Data.ID, "")
	require.Equal(t, http.StatusOK, detail.Code)
	require.Contains(t, detail.Body.String(), `"node_runs"`)
	require.Contains(t, detail.Body.String(), `"artifacts"`)
}

// 输入缺必填项：400 且逐项列出。
func TestCreateRunValidatesInput(t *testing.T) {
	r, db, _ := newSOPRunTestRouter(t)
	tpl := seedRunTemplate(t, db, []string{"repo_url"})
	w := sopDoJSON(t, r, "POST", "/api/v1/sop/runs", fmt.Sprintf(`{"template_id":%q}`, tpl.ID))
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "repo_url")
}

func TestRunEventsSSEStreams(t *testing.T) {
	r, db, hub := newSOPRunTestRouter(t)
	ctx := context.Background()
	run := &types.SopRun{TenantID: 1, Status: types.SopRunRunning}
	require.NoError(t, repository.NewSopRunRepo(db).CreateRun(ctx, run))

	reqCtx, cancel := context.WithCancel(ctx)
	req := httptest.NewRequest("GET", "/api/v1/sop/runs/"+run.ID+"/events", nil).WithContext(reqCtx)
	req = req.WithContext(context.WithValue(req.Context(), types.TenantIDContextKey, uint64(1)))
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { r.ServeHTTP(w, req); close(done) }()

	// 等订阅真正建立再广播（否则事件没人收）
	require.Eventually(t, func() bool { return hub.SubscriberCount(run.ID) == 1 },
		3*time.Second, 20*time.Millisecond)
	hub.Publish(run.ID, executor.StreamEvent{Type: executor.StreamEventTextDelta, Text: "直播"})
	require.Eventually(t, func() bool { return strings.Contains(w.Body.String(), "直播") },
		3*time.Second, 20*time.Millisecond)
	cancel()
	<-done
	require.Contains(t, w.Body.String(), "data:")
	require.Equal(t, 0, hub.SubscriberCount(run.ID), "断开后订阅释放")
}

func TestArtifactFileDownloadAndInline(t *testing.T) {
	r, db, _ := newSOPRunTestRouter(t)
	ctx := context.Background()
	ws := t.TempDir()
	run := &types.SopRun{TenantID: 1, Status: types.SopRunSucceeded, WorkspaceDir: ws}
	require.NoError(t, repository.NewSopRunRepo(db).CreateRun(ctx, run))
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "out"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ws, "out", "a.md"), []byte("# 产物"), 0o644))
	art := &types.SopArtifact{TenantID: 1, RunID: run.ID, NodeID: "n1", Name: "报告",
		Path: "out/a.md", MediaType: "text/markdown"}
	require.NoError(t, repository.NewSopRunRepo(db).CreateArtifact(ctx, art))

	dl := sopDoJSON(t, r, "GET",
		"/api/v1/sop/runs/"+run.ID+"/artifacts/"+art.ID+"/file?download=1", "")
	require.Equal(t, http.StatusOK, dl.Code, dl.Body.String())
	require.Contains(t, dl.Header().Get("Content-Disposition"), "attachment")
	require.Contains(t, dl.Body.String(), "# 产物")

	inline := sopDoJSON(t, r, "GET",
		"/api/v1/sop/runs/"+run.ID+"/artifacts/"+art.ID+"/file", "")
	require.Equal(t, http.StatusOK, inline.Code)
	require.NotContains(t, inline.Header().Get("Content-Disposition"), "attachment")
	require.Contains(t, inline.Header().Get("Content-Type"), "text/markdown")

	// 跨运行取产物：404（产物属于别的运行）
	other := &types.SopRun{TenantID: 1, Status: types.SopRunSucceeded, WorkspaceDir: ws}
	require.NoError(t, repository.NewSopRunRepo(db).CreateRun(ctx, other))
	cross := sopDoJSON(t, r, "GET",
		"/api/v1/sop/runs/"+other.ID+"/artifacts/"+art.ID+"/file", "")
	require.Equal(t, http.StatusNotFound, cross.Code)

	// 产物文件缺失：404
	require.NoError(t, os.Remove(filepath.Join(ws, "out", "a.md")))
	gone := sopDoJSON(t, r, "GET",
		"/api/v1/sop/runs/"+run.ID+"/artifacts/"+art.ID+"/file", "")
	require.Equal(t, http.StatusNotFound, gone.Code)
}

// 节点日志：有 jsonl 优先；只有 .log 时退化；都没有 404 白话。
func TestNodeLogsFallback(t *testing.T) {
	r, db, _ := newSOPRunTestRouter(t)
	ctx := context.Background()
	ws := t.TempDir()
	run := &types.SopRun{TenantID: 1, Status: types.SopRunSucceeded, WorkspaceDir: ws}
	require.NoError(t, repository.NewSopRunRepo(db).CreateRun(ctx, run))
	require.NoError(t, repository.NewSopRunRepo(db).CreateNodeRun(ctx, &types.SopNodeRun{
		TenantID: 1, RunID: run.ID, NodeID: "n1", Status: types.SopNodeSucceeded, Attempt: 2,
	}))

	missing := sopDoJSON(t, r, "GET", "/api/v1/sop/runs/"+run.ID+"/nodes/n1/logs", "")
	require.Equal(t, http.StatusBadRequest, missing.Code)
	require.Contains(t, missing.Body.String(), "暂无日志")

	require.NoError(t, os.MkdirAll(filepath.Join(ws, "logs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ws, "logs", "n1-attempt-2.log"),
		[]byte("脚本输出"), 0o644))
	ok := sopDoJSON(t, r, "GET", "/api/v1/sop/runs/"+run.ID+"/nodes/n1/logs", "")
	require.Equal(t, http.StatusOK, ok.Code, ok.Body.String())
	require.Contains(t, ok.Body.String(), "脚本输出")

	// attempt 覆盖
	require.NoError(t, os.WriteFile(filepath.Join(ws, "logs", "n1-attempt-1.jsonl"),
		[]byte(`{"type":"result"}`), 0o644))
	one := sopDoJSON(t, r, "GET", "/api/v1/sop/runs/"+run.ID+"/nodes/n1/logs?attempt=1", "")
	require.Equal(t, http.StatusOK, one.Code)
	require.Contains(t, one.Body.String(), "result")
}

// 运行列表按租户隔离；统计只算本租户。
func TestListRunsScopedByTenant(t *testing.T) {
	r, db, _ := newSOPRunTestRouter(t)
	ctx := context.Background()
	require.NoError(t, repository.NewSopRunRepo(db).CreateRun(ctx,
		&types.SopRun{TenantID: 1, Status: types.SopRunQueued}))
	require.NoError(t, repository.NewSopRunRepo(db).CreateRun(ctx,
		&types.SopRun{TenantID: 2, Status: types.SopRunQueued}))

	w := sopDoJSON(t, r, "GET", "/api/v1/sop/runs", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp struct {
		Data []types.SopRun
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data, 1)
	require.Equal(t, uint64(1), resp.Data[0].TenantID)

	stats := sopDoJSON(t, r, "GET", "/api/v1/sop/runs/stats/summary", "")
	require.Equal(t, http.StatusOK, stats.Code)
	require.Contains(t, stats.Body.String(), `"total_count":1`)
}
