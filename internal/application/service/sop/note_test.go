package sop

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
)

func newTestNotesService(t *testing.T) (*NotesService, *types.SopProject) {
	t.Helper()
	db := newBriefTestDB(t)
	p := seedNotesProject(t, db, 1, "kb-1")
	svc := NewNotesService(NoteDeps{
		Notes:    repository.NewSopProjectNoteRepo(db),
		Items:    itemListerOf(t, db),
		Projects: repository.NewSopProjectRepo(db),
	})
	return svc, p
}

// 运行摘要必须确定性：同一次运行的节点/产物/用量永远得到同样的正文；
// 产物带 sha256 前 8 位，且不落敏感串。
func TestAutoRecordRunIsDeterministic(t *testing.T) {
	svc, p := newTestNotesService(t)
	ctx := context.Background()

	run := &types.SopRun{
		TenantID: 1, ProjectID: p.ID, ItemID: "i1",
		Status: types.SopRunSucceeded,
		Usage:  types.SopUsage{InputTokens: 1200, OutputTokens: 300},
		HumanContext: []types.SopHumanContextEntry{
			{Source: "approval", Text: "同意上线，注意回滚"},
			{Source: "human_context", Text: "补充：优先移动端"},
		},
	}
	nodeRuns := []types.SopNodeRun{
		{NodeID: "n1", Status: types.SopNodeSucceeded},
		{NodeID: "n2", Status: types.SopNodeSucceeded},
	}
	arts := []types.SopArtifact{
		{Name: "PRD", Path: "out/prd.md", SHA256: "abcdef0123456789"},
	}

	require.NoError(t, svc.AutoRecordRun(ctx, run, "登录改造", nodeRuns, arts))

	list, err := svc.List(ctx, 1, p.ID, types.SopNoteKindRunSummary)
	require.NoError(t, err)
	require.Len(t, list, 1)
	n := list[0]
	require.Equal(t, types.SopNoteSourceEngine, n.Source)
	require.Equal(t, "运行结论：登录改造", n.Title)
	require.Equal(t, "i1", n.ItemID)
	require.Equal(t, run.ID, n.RunID)
	require.Contains(t, n.Body, "out/prd.md")
	require.Contains(t, n.Body, "abcdef01")
	require.Contains(t, n.Body, "输入 1200")
	require.Contains(t, n.Body, "同意上线，注意回滚")
	require.NotContains(t, n.Body, "补充：优先移动端", "只取审批意见，不把补充上下文当结论")
	require.NotContains(t, n.Body, "sk-")

	// 确定性：同入参再跑一次，正文逐字节相同。
	require.NoError(t, svc.AutoRecordRun(ctx, run, "登录改造", nodeRuns, arts))
	again, err := svc.List(ctx, 1, p.ID, types.SopNoteKindRunSummary)
	require.NoError(t, err)
	require.Len(t, again, 2)
	require.Equal(t, n.Body, again[0].Body, "同入参正文必须一致")
}

// 人工记一条：来源固定 human、kind 缺省 note、触发简报重渲染；跨租户项目拒绝。
func TestNotesCreateHuman(t *testing.T) {
	db := newBriefTestDB(t)
	triggered := 0
	svc := NewNotesService(NoteDeps{
		Notes:    repository.NewSopProjectNoteRepo(db),
		Items:    itemListerOf(t, db),
		Projects: repository.NewSopProjectRepo(db),
		OnRecord: func(_ uint64, _ string) { triggered++ },
	})
	ctx := context.Background()
	p := seedNotesProject(t, db, 1, "kb-1")

	n := &types.SopProjectNote{ProjectID: p.ID, Body: "新决策"}
	require.NoError(t, svc.Create(ctx, 1, "u1", n))
	require.Equal(t, types.SopNoteSourceHuman, n.Source)
	require.Equal(t, types.SopNoteKindNote, n.Kind)
	require.Equal(t, "u1", n.CreatedBy)
	require.Equal(t, 1, triggered, "写入后必须触发简报重渲染")

	foreign := &types.SopProjectNote{ProjectID: "p-other", Body: "x"}
	require.ErrorIs(t, svc.Create(ctx, 1, "u1", foreign), ErrSopNoteProjectNotFound)

	n.Body = "改过的决策"
	n.Pinned = true
	require.NoError(t, svc.Edit(ctx, 1, "u1", n))
	got, err := svc.Get(ctx, 1, n.ID)
	require.NoError(t, err)
	require.Equal(t, "改过的决策", got.Body)
	require.True(t, got.Pinned)
	require.Equal(t, 2, triggered, "编辑同样要触发重渲染")

	require.NoError(t, svc.SoftDelete(ctx, 1, n.ID))
	_, err = svc.Get(ctx, 1, n.ID)
	require.Error(t, err)
}

func TestNotesPin(t *testing.T) {
	db := newBriefTestDB(t)
	svc := NewNotesService(NoteDeps{
		Notes:    repository.NewSopProjectNoteRepo(db),
		Items:    itemListerOf(t, db),
		Projects: repository.NewSopProjectRepo(db),
	})
	ctx := context.Background()
	p := seedNotesProject(t, db, 1, "kb-1")
	n := &types.SopProjectNote{ProjectID: p.ID, Body: "b"}
	require.NoError(t, svc.Create(ctx, 1, "u1", n))

	require.NoError(t, svc.Pin(ctx, 1, n.ID, true))
	got, err := svc.Get(ctx, 1, n.ID)
	require.NoError(t, err)
	require.True(t, got.Pinned)

	require.Error(t, svc.Pin(ctx, 2, n.ID, false), "跨租户不可置顶")
}

func TestAutoRecordRunRejectsForeignRun(t *testing.T) {
	svc, _ := newTestNotesService(t)
	err := svc.AutoRecordRun(context.Background(), &types.SopRun{
		TenantID: 1, ProjectID: "missing", Status: types.SopRunFailed,
	}, "x", nil, nil)
	require.ErrorIs(t, err, ErrSopNoteProjectNotFound)
	require.Contains(t, err.Error(), "项目")
}

// RecordRunFinal 是引擎回调适配：自取节点/产物/事项标题后写摘要。
func TestRecordRunFinalFetchesContext(t *testing.T) {
	db := newBriefTestDB(t)
	ctx := context.Background()
	p := seedNotesProject(t, db, 1, "kb-1")
	items := repository.NewSopItemRepo(db)
	it := &types.SopItem{TenantID: 1, ProjectID: p.ID, No: "KNOW-1", Title: "登录改造"}
	require.NoError(t, items.Create(ctx, it))

	runs := repository.NewSopRunRepo(db)
	run := &types.SopRun{TenantID: 1, ProjectID: p.ID, ItemID: it.ID, Status: types.SopRunRunning}
	require.NoError(t, runs.CreateRun(ctx, run))
	require.NoError(t, runs.CreateNodeRun(ctx, &types.SopNodeRun{
		TenantID: 1, RunID: run.ID, NodeID: "n1", Status: types.SopNodeSucceeded,
	}))
	require.NoError(t, runs.CreateArtifact(ctx, &types.SopArtifact{
		TenantID: 1, RunID: run.ID, NodeID: "n1", Name: "PRD", Path: "out/prd.md",
		SHA256: "0123456789abcdef",
	}))

	svc := NewNotesService(NoteDeps{
		Notes: repository.NewSopProjectNoteRepo(db), Items: itemListerOf(t, db),
		Projects: repository.NewSopProjectRepo(db), Runs: runs,
	})
	run.Status = types.SopRunSucceeded
	require.NoError(t, svc.RecordRunFinal(ctx, run))

	list, err := svc.List(ctx, 1, p.ID, types.SopNoteKindRunSummary)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "运行结论：登录改造", list[0].Title)
	require.Contains(t, list[0].Body, "n1：成功")
	require.Contains(t, list[0].Body, "out/prd.md")
	require.Contains(t, list[0].Body, "01234567")
}
