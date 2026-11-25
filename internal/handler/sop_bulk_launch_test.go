package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service/sop"
	"github.com/Tencent/WeKnora/internal/types"
)

// newSOPBulkLaunchTestRouter 需要「事项 + 模板 + 运行」三件套：批量触发要读模板状态。
func newSOPBulkLaunchTestRouter(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	db := newSOPHandlerTestDB(t)
	lastSOPTestDB = db
	require.NoError(t, db.AutoMigrate(&types.SopTemplate{}, &types.SopTemplateRevision{}))
	projects := repository.NewSopProjectRepo(db)
	items := sop.NewItemService(projects, repository.NewSopItemRepo(db),
		repository.NewSopItemExtraRepo(db), repository.NewSopRunRepo(db))
	h := NewSOPHandler(SOPHandlerDeps{
		Projects:  projects,
		Runs:      repository.NewSopRunRepo(db),
		Items:     items,
		Templates: sop.NewTemplateService(repository.NewSopTemplateRepo(db), projects),
		RunSvc: sop.NewRunService(repository.NewSopTemplateRepo(db), repository.NewSopRunRepo(db),
			projects, repository.NewSopItemRepo(db)),
	})
	r := newSOPTestRouterWith(t, h)
	return r, db
}

// 批量触发：传父项只对子项建运行，运行中的子项进 skipped。
func TestBulkLaunchExpandsParentAndSkipsRunning(t *testing.T) {
	r, db := newSOPBulkLaunchTestRouter(t)
	ctx := context.Background()
	tpl := seedRunTemplate(t, db, nil)
	pid := createTestProject(t, r)
	parent := createTestItem(t, r, pid, `{"title":"大需求"}`)

	w := sopDoJSON(t, r, "POST", "/api/v1/sop/items/"+parent+"/split",
		fmt.Sprintf(`{"children":[{"title":"子一","template_id":%q},{"title":"子二","template_id":%q}]}`,
			tpl.ID, tpl.ID))
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	kids := sopDoJSON(t, r, "GET", "/api/v1/sop/items/"+parent+"/children", "")
	require.Equal(t, http.StatusOK, kids.Code)
	var kidResp struct {
		Data []types.SopItemWithDerived
	}
	require.NoError(t, json.Unmarshal(kids.Body.Bytes(), &kidResp))
	require.Len(t, kidResp.Data, 2)

	// 让第二个子项处于运行中（派生执行情况 running → 应跳过）。
	running := kidResp.Data[1]
	require.NoError(t, repository.NewSopRunRepo(db).CreateRun(ctx, &types.SopRun{
		TenantID: 1, ProjectID: pid, ItemID: running.ID, Status: types.SopRunRunning,
		WorkflowSnapshot: types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Name: "实现"}}},
	}))

	w = sopDoJSON(t, r, "POST", "/api/v1/sop/items/bulk-launch", fmt.Sprintf(`{"ids":[%q]}`, parent))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp struct {
		Data sop.LaunchResult
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, []string{kidResp.Data[0].ID}, resp.Data.Started, "父项展开后只对可跑子项建运行")
	require.Equal(t, []string{running.ID}, resp.Data.Skipped, "运行中的子项跳过")
}

// 直接传子项 id 也能跑；没有模板的事项进 skipped。
func TestBulkLaunchChildAndMissingTemplate(t *testing.T) {
	r, db := newSOPBulkLaunchTestRouter(t)
	tpl := seedRunTemplate(t, db, nil)
	pid := createTestProject(t, r)
	withTpl := createTestItem(t, r, pid, fmt.Sprintf(`{"title":"有模板","template_id":%q}`, tpl.ID))
	noTpl := createTestItem(t, r, pid, `{"title":"无模板"}`)

	w := sopDoJSON(t, r, "POST", "/api/v1/sop/items/bulk-launch",
		fmt.Sprintf(`{"ids":[%q,%q]}`, withTpl, noTpl))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp struct {
		Data sop.LaunchResult
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, []string{withTpl}, resp.Data.Started)
	require.Equal(t, []string{noTpl}, resp.Data.Skipped)
}

// 空 ids 直接 400，不建任何运行。
func TestBulkLaunchRejectsEmpty(t *testing.T) {
	r, _ := newSOPBulkLaunchTestRouter(t)
	w := sopDoJSON(t, r, "POST", "/api/v1/sop/items/bulk-launch", `{"ids":[]}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "请选择")
}
