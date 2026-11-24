package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service/sop"
	"github.com/Tencent/WeKnora/internal/types"
)

// newSOPBriefTestRouter 组装带事项/黑板/简报能力的最小路由。
func newSOPBriefTestRouter(t *testing.T) (*gin.Engine, *sop.NotesService) {
	t.Helper()
	db := newSOPHandlerTestDB(t)
	lastSOPTestDB = db
	projects := repository.NewSopProjectRepo(db)
	items := sop.NewItemService(
		projects, repository.NewSopItemRepo(db), repository.NewSopItemExtraRepo(db),
		repository.NewSopRunRepo(db),
	)
	notesRepo := repository.NewSopProjectNoteRepo(db)
	brief := sop.NewBriefService(sop.BriefDeps{
		Projects: projects, Notes: notesRepo, Items: items,
		Knowledge: stubBriefKnowledge{},
	})
	notes := sop.NewNotesService(sop.NoteDeps{
		Projects: projects, Notes: notesRepo, Items: items,
		Runs:     repository.NewSopRunRepo(db),
		OnRecord: brief.Trigger,
	})
	h := NewSOPHandler(SOPHandlerDeps{
		Projects: projects, Runs: repository.NewSopRunRepo(db),
		Items: items, Notes: notes, Brief: brief,
		RunSvc: sop.NewRunService(
			repository.NewSopTemplateRepo(db), repository.NewSopRunRepo(db),
			projects, repository.NewSopItemRepo(db),
		),
	})
	r := newSOPTestRouterWith(t, h)
	r.POST("/api/v1/sop/runs", h.CreateRun)
	return r, notes
}

var errStubKBNotReady = errors.New("测试环境不连知识库")

// stubBriefKnowledge 让简报同步在测试里恒失败且不落库（CreateProject 只 warn）。
type stubBriefKnowledge struct{}

func (stubBriefKnowledge) CreateKnowledgeFromManual(
	context.Context, string, *types.ManualKnowledgePayload, string,
) (*types.Knowledge, error) {
	return nil, errStubKBNotReady
}
func (stubBriefKnowledge) UpdateManualKnowledge(
	context.Context, string, *types.ManualKnowledgePayload,
) (*types.Knowledge, error) {
	return nil, errStubKBNotReady
}

// createTestItem 通过 HTTP 创建事项并返回 id。
func createTestItem(t *testing.T, r *gin.Engine, projectID, body string) string {
	t.Helper()
	w := sopDoJSON(t, r, "POST", "/api/v1/sop/projects/"+projectID+"/items", body)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var resp struct {
		Data types.SopItem
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp.Data.ID
}

// split 建子项：父项自动升级为大项，子项带 parent_id 与 split_source。
func TestSplitMarksParentBigAndSetsSource(t *testing.T) {
	r, _ := newSOPBriefTestRouter(t)
	pid := createTestProject(t, r)
	parent := createTestItem(t, r, pid, `{"title":"大需求"}`)

	w := sopDoJSON(t, r, "POST", "/api/v1/sop/items/"+parent+"/split",
		`{"children":[{"title":"子一","description":"d1"},{"title":"子二"}]}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var resp struct {
		Data struct {
			Created []types.SopItem `json:"created"`
		}
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data.Created, 2)
	for _, kid := range resp.Data.Created {
		require.Equal(t, parent, kid.ParentID)
		require.Equal(t, types.SopSplitSourceManual, kid.SplitSource)
	}

	detail := sopDoJSON(t, r, "GET", "/api/v1/sop/items/"+parent, "")
	require.Equal(t, http.StatusOK, detail.Code)
	var parentResp struct {
		Data types.SopItem
	}
	require.NoError(t, json.Unmarshal(detail.Body.Bytes(), &parentResp))
	require.True(t, parentResp.Data.IsBig, "独立事项被拆后自动升级为大项")

	kids := sopDoJSON(t, r, "GET", "/api/v1/sop/items/"+parent+"/children", "")
	require.Equal(t, http.StatusOK, kids.Code)
	require.Contains(t, kids.Body.String(), "子一")
}

// 三级嵌套拒绝：子项不能再拆。
func TestSplitRejectsThirdLevel(t *testing.T) {
	r, _ := newSOPBriefTestRouter(t)
	pid := createTestProject(t, r)
	parent := createTestItem(t, r, pid, `{"title":"大需求"}`)
	w := sopDoJSON(t, r, "POST", "/api/v1/sop/items/"+parent+"/split", `{"children":[{"title":"子项"}]}`)
	require.Equal(t, http.StatusCreated, w.Code)
	var got struct {
		Data struct {
			Created []types.SopItem `json:"created"`
		}
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Len(t, got.Data.Created, 1)

	w = sopDoJSON(t, r, "POST", "/api/v1/sop/items/"+got.Data.Created[0].ID+"/split",
		`{"children":[{"title":"三级"}]}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "只能拆两层")
}

// 大项不能直接发起运行（run 与 relaunch 两个入口都拦）。
func TestParentItemCannotLaunchDirectly(t *testing.T) {
	r, _ := newSOPBriefTestRouter(t)
	pid := createTestProject(t, r)
	parent := createTestItem(t, r, pid, `{"title":"大需求"}`)
	sopDoJSON(t, r, "POST", "/api/v1/sop/items/"+parent+"/split", `{"children":[{"title":"子项"}]}`)

	w := sopDoJSON(t, r, "POST", "/api/v1/sop/items/"+parent+"/relaunch", `{}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "请对子项触发")

	w = sopDoJSON(t, r, "POST", "/api/v1/sop/runs", `{"item_id":"`+parent+`","template_id":"x"}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "请对子项触发")
}

// 简报端点报出水位与绑定状态；黑板写入后 notes_max_seq 前进。
func TestBriefEndpointReportsSeqState(t *testing.T) {
	r, _ := newSOPBriefTestRouter(t)
	pid := createTestProject(t, r)

	w := sopDoJSON(t, r, "POST", "/api/v1/sop/projects/"+pid+"/notes",
		`{"kind":"decision","title":"A","body":"因为B"}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	got := sopDoJSON(t, r, "GET", "/api/v1/sop/projects/"+pid+"/brief", "")
	require.Equal(t, http.StatusOK, got.Code)
	var resp struct {
		Data struct {
			Markdown    string `json:"markdown"`
			RenderedSeq int64  `json:"rendered_seq"`
			NotesMaxSeq int64  `json:"notes_max_seq"`
			KBBound     bool   `json:"kb_bound"`
		}
	}
	require.NoError(t, json.Unmarshal(got.Body.Bytes(), &resp))
	require.Contains(t, resp.Data.Markdown, "因为B")
	require.Equal(t, int64(1), resp.Data.NotesMaxSeq)
	require.False(t, resp.Data.KBBound, "测试路由不建知识库")

	list := sopDoJSON(t, r, "GET", "/api/v1/sop/projects/"+pid+"/notes?kind=decision", "")
	require.Equal(t, http.StatusOK, list.Code)
	require.Contains(t, list.Body.String(), "因为B")
}

// 简报未绑知识库时手动渲染回 400 白话。
func TestRenderBriefWithoutKBRejected(t *testing.T) {
	r, _ := newSOPBriefTestRouter(t)
	pid := createTestProject(t, r)
	w := sopDoJSON(t, r, "POST", "/api/v1/sop/projects/"+pid+"/brief/render", `{}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "请先绑定知识库")
}
