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

// newSOPAdminTestRouter 装配管理端点（真实仓储 + sqlite 内存库）。
func newSOPAdminTestRouter(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	db := newSOPHandlerTestDB(t)
	lastSOPTestDB = db
	require.NoError(t, db.AutoMigrate(
		&types.SopCLICredential{}, &types.SopTriggerConfig{}, &types.SopTriggerLog{},
	))
	items := sop.NewItemService(repository.NewSopProjectRepo(db), repository.NewSopItemRepo(db),
		repository.NewSopItemExtraRepo(db), repository.NewSopRunRepo(db))
	creds := repository.NewSopCredentialRepo(db)
	approvals := sop.NewApprovalService(nil, repository.NewSopItemExtraRepo(db)).
		WithAggregateSources(sop.AggregateDeps{
			Runs:     repository.NewSopRunRepo(db),
			Projects: repository.NewSopProjectRepo(db),
			Items:    repository.NewSopItemRepo(db),
		})
	h := NewSOPHandler(SOPHandlerDeps{
		Projects:  repository.NewSopProjectRepo(db),
		Runs:      repository.NewSopRunRepo(db),
		Items:     items,
		Approvals: approvals,
		Triggers:  repository.NewSopTriggerRepo(db),
		Creds:     creds,
		Archive: sop.NewArchiveService(repository.NewSopProjectRepo(db),
			repository.NewSopItemRepo(db), repository.NewSopRunRepo(db)),
		CredDelete: func(ctx context.Context, tenantID uint64, id string) error {
			return creds.Delete(ctx, tenantID, id)
		},
	})
	// newSOPTestRouterWith 已挂项目组与事项组；这里只补管理组。
	r := newSOPTestRouterWith(t, h)
	admin := r.Group("/api/v1/sop")
	{
		admin.GET("/approvals", h.ListApprovals)
		admin.GET("/projects/:id/trigger-config", h.GetTriggerConfig)
		admin.PUT("/projects/:id/trigger-config", h.PutTriggerConfig)
		admin.GET("/projects/:id/trigger-logs", h.ListTriggerLogs)
		admin.GET("/cli-credentials", h.ListCredentials)
		admin.PUT("/cli-credentials/:kind", h.PutCredential)
		admin.DELETE("/cli-credentials/:kind", h.DeleteCredential)
		admin.GET("/pm-archive/summary", h.PMArchiveSummary)
	}
	return r, db
}

// 凭据 upsert：响应脱敏（只留尾 4 位），库内是密文；列表也不回明文。
func TestCredentialUpsertMasksAndEncrypts(t *testing.T) {
	// 加密依赖 SYSTEM_AES_KEY：没配就是「空密钥 = 明文」，这里显式给一把测试密钥。
	t.Setenv("SYSTEM_AES_KEY", "01234567890123456789012345678901")
	r, db := newSOPAdminTestRouter(t)
	const secret = "sk-super-secret-1234"

	w := sopDoJSON(t, r, "PUT", "/api/v1/sop/cli-credentials/zcode",
		`{"name":"我的 zcode","api_key":"`+secret+`"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), secret, "响应不得回明文")
	require.Contains(t, w.Body.String(), "1234", "只回尾 4 位")
	require.Contains(t, w.Body.String(), "我的 zcode", "name 存在 Extra 里并回显")

	var raw string
	require.NoError(t, db.Raw(
		"SELECT api_key FROM sop_cli_credentials WHERE cli_kind='zcode'").Scan(&raw).Error)
	require.NotContains(t, raw, secret, "库内必须是密文")

	list := sopDoJSON(t, r, "GET", "/api/v1/sop/cli-credentials", "")
	require.Equal(t, http.StatusOK, list.Code)
	require.NotContains(t, list.Body.String(), secret)
	require.Contains(t, list.Body.String(), "1234")

	// api_key 留空：沿用旧密钥（尾号不变）
	again := sopDoJSON(t, r, "PUT", "/api/v1/sop/cli-credentials/zcode", `{"base_url":"https://x"}`)
	require.Equal(t, http.StatusOK, again.Code)
	require.Contains(t, again.Body.String(), "1234", "空密钥沿用旧值")

	// 删除
	require.Equal(t, http.StatusOK,
		sopDoJSON(t, r, "DELETE", "/api/v1/sop/cli-credentials/zcode", "").Code)
	after := sopDoJSON(t, r, "GET", "/api/v1/sop/cli-credentials", "")
	require.NotContains(t, after.Body.String(), "zcode")
}

// 触发配置：cron 太密 + 条数越界 → 400 中文；合法配置 → 200。
func TestTriggerConfigValidation(t *testing.T) {
	r, _ := newSOPAdminTestRouter(t)
	pid := createTestProject(t, r)

	bad := sopDoJSON(t, r, "PUT", "/api/v1/sop/projects/"+pid+"/trigger-config",
		`{"mode":"cron","cron":"* * * * *","per_run_limit":50}`)
	require.Equal(t, http.StatusBadRequest, bad.Code, bad.Body.String())
	require.True(t,
		// 两个问题至少要报出一个（条数越界或 cron 太密），且是中文
		containsAny(bad.Body.String(), "1–10", "5 分钟"),
		"错误要翻译成中文：%s", bad.Body.String())

	ok := sopDoJSON(t, r, "PUT", "/api/v1/sop/projects/"+pid+"/trigger-config",
		`{"mode":"interval","interval_key":"30m","per_run_limit":3,"statuses":["todo"],"sources":"customer"}`)
	require.Equal(t, http.StatusOK, ok.Code, ok.Body.String())

	got := sopDoJSON(t, r, "GET", "/api/v1/sop/projects/"+pid+"/trigger-config", "")
	require.Equal(t, http.StatusOK, got.Code)
	require.Contains(t, got.Body.String(), `"interval_key":"30m"`)
}

// 未配置触发器的项目：读配置返回默认体（前端表单可直接编辑）。
func TestTriggerConfigDefaultsWhenUnset(t *testing.T) {
	r, _ := newSOPAdminTestRouter(t)
	pid := createTestProject(t, r)
	got := sopDoJSON(t, r, "GET", "/api/v1/sop/projects/"+pid+"/trigger-config", "")
	require.Equal(t, http.StatusOK, got.Code)
	require.Contains(t, got.Body.String(), `"mode":"interval"`)
	require.Contains(t, got.Body.String(), `"per_run_limit":10`)

	logs := sopDoJSON(t, r, "GET", "/api/v1/sop/projects/"+pid+"/trigger-logs", "")
	require.Equal(t, http.StatusNotFound, logs.Code, "没有配置时历史无从谈起")
}

// 审批中心：waiting 运行的等待审批节点进 sop 行。
func TestApprovalsEndpointAggregates(t *testing.T) {
	r, db := newSOPAdminTestRouter(t)
	ctx := context.Background()
	proj := &types.SopProject{TenantID: 1, Name: "P", Code: "TP"}
	require.NoError(t, repository.NewSopProjectRepo(db).Create(ctx, proj))
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Name: "确认", Type: "approval"}}}
	run := &types.SopRun{TenantID: 1, Status: types.SopRunWaiting, ProjectID: proj.ID, WorkflowSnapshot: def}
	require.NoError(t, repository.NewSopRunRepo(db).CreateRun(ctx, run))
	require.NoError(t, repository.NewSopRunRepo(db).CreateNodeRun(ctx, &types.SopNodeRun{
		TenantID: 1, RunID: run.ID, NodeID: "n1", Status: types.SopNodeWaitingApproval,
	}))

	w := sopDoJSON(t, r, "GET", "/api/v1/sop/approvals", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"kind":"sop"`)
	require.Contains(t, w.Body.String(), "确认")
	require.Contains(t, w.Body.String(), "P", "项目名联查")
}

// PM 归档摘要：只统计带 pm_source_id 的事项。
func TestPMArchiveSummaryCountsMigrated(t *testing.T) {
	r, db := newSOPAdminTestRouter(t)
	ctx := context.Background()
	proj := &types.SopProject{TenantID: 1, Name: "P", Code: "TP"}
	require.NoError(t, repository.NewSopProjectRepo(db).Create(ctx, proj))
	migrated := &types.SopItem{
		TenantID: 1, ProjectID: proj.ID, No: "TP-1", Title: "迁移来的", Status: "todo",
		DomainData: map[string]any{"pm_source_id": "pm-1"},
	}
	require.NoError(t, repository.NewSopItemRepo(db).Create(ctx, migrated))
	fresh := &types.SopItem{
		TenantID: 1, ProjectID: proj.ID, No: "TP-2", Title: "新建的", Status: "todo",
	}
	require.NoError(t, repository.NewSopItemRepo(db).Create(ctx, fresh))
	require.NoError(t, repository.NewSopRunRepo(db).CreateRun(ctx, &types.SopRun{
		TenantID: 1, ProjectID: proj.ID, ItemID: migrated.ID, Status: types.SopRunSucceeded,
	}))

	w := sopDoJSON(t, r, "GET", "/api/v1/sop/pm-archive/summary", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp struct {
		Data sop.PMArchiveCounts
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, 1, resp.Data.Projects)
	require.Equal(t, 1, resp.Data.Items, "只统计迁移事项")
	require.Equal(t, 1, resp.Data.Runs)
}

// containsAny 判断响应体是否包含任一关键词。
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && len(s) >= len(sub) && stringsContains(s, sub) {
			return true
		}
	}
	return false
}

// stringsContains 是一个不引入额外依赖的子串判断。
func stringsContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
