package engine_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service/sop"
	"github.com/Tencent/WeKnora/internal/application/service/sop/engine"
	"github.com/Tencent/WeKnora/internal/application/service/sop/executor"
	"github.com/Tencent/WeKnora/internal/types"
)

// fakeBriefKB 记录简报同步调用（只经 CreateKnowledgeFromManual / UpdateManualKnowledge）。
type fakeBriefKB struct {
	createCalls int
	updateCalls int
	lastTitle   string
	lastContent string
	seq         int
}

func (f *fakeBriefKB) CreateKnowledgeFromManual(
	_ context.Context, _ string, p *types.ManualKnowledgePayload, _ string,
) (*types.Knowledge, error) {
	f.createCalls++
	f.seq++
	f.lastTitle, f.lastContent = p.Title, p.Content
	return &types.Knowledge{ID: fmt.Sprintf("k-%d", f.seq), Title: p.Title}, nil
}

func (f *fakeBriefKB) UpdateManualKnowledge(
	_ context.Context, id string, p *types.ManualKnowledgePayload,
) (*types.Knowledge, error) {
	f.updateCalls++
	f.lastTitle, f.lastContent = p.Title, p.Content
	return &types.Knowledge{ID: id, Title: p.Title}, nil
}

// captureAgentProvider 记录每次执行拿到的任务书（用于断言「项目现状」块真的下发了）。
type captureAgentProvider struct {
	prompts []string
	out     string
}

func (c *captureAgentProvider) Kind() string { return "platform_agent" }

func (c *captureAgentProvider) Prepare(context.Context, *types.SopNode, string) error { return nil }

func (c *captureAgentProvider) Execute(
	_ context.Context, tb executor.TaskBook, _ executor.ExecOptions,
) (executor.ExecResult, error) {
	c.prompts = append(c.prompts, tb.Prompt)
	if tb.OutDir != "" {
		_ = os.WriteFile(filepath.Join(tb.OutDir, "a.md"), []byte("# out"), 0o644)
	}
	return executor.ExecResult{Output: c.out, GateResult: types.SopGatePass}, nil
}

func (c *captureAgentProvider) Cancel(context.Context) error { return nil }

// briefEnv 是一个可跑完整链路的测试环境（真实仓储 + 真实 BriefService/NotesService）。
type briefEnv struct {
	DB      *gorm.DB
	Engine  *engine.Engine
	Brief   *sop.BriefService
	Notes   *sop.NotesService
	Items   *sop.ItemService
	Tpl     *types.SopTemplate
	Project *types.SopProject
	KB      *fakeBriefKB
	Agent   *captureAgentProvider
	// Triggers 是黑板写入后要求重渲染的次数（去抖窗口内只记一次写入一次）。
	Triggers *atomic.Int64
}

func newBriefEnv(t *testing.T) *briefEnv {
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
		&types.SopTemplate{}, &types.SopTemplateRevision{},
		&types.SopRun{}, &types.SopNodeRun{}, &types.SopArtifact{},
		&types.SopProjectNote{},
	))
	require.NoError(t, db.Exec(
		"CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_node_runs_run_node ON sop_node_runs(run_id, node_id)",
	).Error)
	require.NoError(t, db.Exec(
		"CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_notes_project_seq "+
			"ON sop_project_notes(project_id, seq) WHERE deleted_at IS NULL",
	).Error)

	projects := repository.NewSopProjectRepo(db)
	itemsRepo := repository.NewSopItemRepo(db)
	extras := repository.NewSopItemExtraRepo(db)
	runs := repository.NewSopRunRepo(db)
	notesRepo := repository.NewSopProjectNoteRepo(db)
	itemSvc := sop.NewItemService(projects, itemsRepo, extras, runs)

	proj := &types.SopProject{TenantID: 1, Name: "共享上下文项目", Code: "CTX", KBID: "kb-1"}
	require.NoError(t, projects.Create(context.Background(), proj))

	triggers := &atomic.Int64{}
	kb := &fakeBriefKB{}
	brief := sop.NewBriefService(sop.BriefDeps{
		Projects: projects, Notes: notesRepo, Items: itemSvc, Knowledge: kb,
	})
	notes := sop.NewNotesService(sop.NoteDeps{
		Projects: projects, Notes: notesRepo, Items: itemSvc, Runs: runs,
		// 简报重渲染的合并窗口由 sop 包单测覆盖（TestBriefTriggerDebounces）；
		// E2E 里只记「有人要求重渲染」，避免后台 2s 定时器与测试收尾竞态。
		OnRecord: func(uint64, string) { triggers.Add(1) },
	})

	agent := &captureAgentProvider{out: "分析完成"}
	reg := executor.NewRegistry()
	reg.Register(agent)
	reg.Register(executor.NewScriptProvider())

	eng := engine.NewEngine(engine.Deps{
		Runs: runs, Templates: repository.NewSopTemplateRepo(db), Projects: projects,
		Items: itemsRepo, Extras: extras, Registry: reg,
		ItemStatus: itemSvc, WorkspaceRoot: t.TempDir(), MaxParallel: 2,
		BriefProvider: brief, Notes: notesRepo,
		OnRunFinal: func(ctx context.Context, run *types.SopRun) {
			require.NoError(t, notes.RecordRunFinal(ctx, run))
		},
		CreateItems: func(ctx context.Context, run *types.SopRun, specs []types.SopChildItemSpec) error {
			_, err := itemSvc.CreateChildItems(ctx, run.TenantID, run.ItemID, specs, types.SopSplitSourceTemplate)
			return err
		},
	})

	tpl := seedBriefTemplate(t, db, proj.ID)
	return &briefEnv{
		DB: db, Engine: eng, Brief: brief, Notes: notes, Items: itemSvc,
		Tpl: tpl, Project: proj, KB: kb, Agent: agent, Triggers: triggers,
	}
}

// seedBriefTemplate 建一个两节点模板：分析 → 汇总（都写 out/a.md，供产物断言）。
func seedBriefTemplate(t *testing.T, db *gorm.DB, projectID string) *types.SopTemplate {
	t.Helper()
	ctx := context.Background()
	repo := repository.NewSopTemplateRepo(db)
	def := types.SopDefinition{
		Nodes: []types.SopNode{
			{
				ID: "analysis", Name: "分析", Type: "agent",
				Executor:   types.SopNodeExecutor{Kind: "platform_agent", AgentID: "stub"},
				TaskPrompt: "分析需求",
				ArtifactOutputs: []types.SopArtifactOutput{{
					Name: "a.md", Path: "out/a.md", Required: true,
					Check: types.SopArtifactCheck{Type: types.SopArtifactCheckExists},
				}},
			},
			{
				ID: "summary", Name: "汇总", Type: "agent",
				Executor:   types.SopNodeExecutor{Kind: "platform_agent", AgentID: "stub"},
				TaskPrompt: "汇总结论",
				ArtifactOutputs: []types.SopArtifactOutput{{
					Name: "a.md", Path: "out/a.md", Required: true,
					Check: types.SopArtifactCheck{Type: types.SopArtifactCheckExists},
				}},
			},
		},
		Edges: []types.SopEdge{{ID: "e1", Source: "analysis", Target: "summary", When: "success"}},
	}
	types.NormalizeSopDefinition(&def)
	tpl := &types.SopTemplate{
		TenantID: 1, ProjectID: projectID, Name: "共享上下文链路",
		Status: types.SopTemplateStatusDraft, Definition: def,
	}
	require.NoError(t, repo.Create(ctx, tpl))
	rev, err := repo.Publish(ctx, 1, tpl.ID, "u1")
	require.NoError(t, err)
	tpl.PublishedRevisionID = rev.ID
	return tpl
}

func (env *briefEnv) createRun(t *testing.T, itemID string) *types.SopRun {
	t.Helper()
	ctx := context.Background()
	rev, err := env.Engine.Deps().Templates.GetRevision(ctx, 1, env.Tpl.PublishedRevisionID)
	require.NoError(t, err)
	run := &types.SopRun{
		TenantID: 1, ProjectID: env.Project.ID, TemplateID: env.Tpl.ID, RevisionID: rev.ID,
		WorkflowSnapshot: rev.Definition, Status: types.SopRunQueued,
		TriggerKind: types.SopRunTriggerManual, Input: map[string]any{}, ItemID: itemID,
	}
	require.NoError(t, env.Engine.Deps().Runs.CreateRun(ctx, run))
	return run
}

func (env *briefEnv) runStatus(t *testing.T, runID string) string {
	t.Helper()
	got, err := env.Engine.Deps().Runs.GetRun(context.Background(), 0, runID)
	require.NoError(t, err)
	return got.Status
}

func (env *briefEnv) noteCount(t *testing.T, kind string) int {
	t.Helper()
	list, err := repository.NewSopProjectNoteRepo(env.DB).
		List(context.Background(), 1, env.Project.ID, kind, nil, 100)
	require.NoError(t, err)
	return len(list)
}

// TestSharedContextFlowsToNodesAndBriefToKB 覆盖 spec §16.22/25 的端到端主线：
// 兄弟事项进度与黑板决策进入任务书 → 简报物化到 inputs → 运行终态自动摘要 → 简报同步进 KB。
func TestSharedContextFlowsToNodesAndBriefToKB(t *testing.T) {
	env := newBriefEnv(t)
	ctx := context.Background()

	// 1) 一条已完成的兄弟事项 + 一条待启动事项
	done, err := env.Items.Create(ctx, 1, "u1", env.Project.ID, sop.CreateItemInput{Title: "已完成的兄弟事项"})
	require.NoError(t, err)
	for _, to := range []string{
		types.SopItemStatusTodo, types.SopItemStatusInProgress,
		types.SopItemStatusInReview, types.SopItemStatusDone,
	} {
		require.NoError(t, env.Items.UpdateStatus(ctx, 1, done.ID, to))
	}
	target, err := env.Items.Create(ctx, 1, "u1", env.Project.ID, sop.CreateItemInput{Title: "本次执行的事项"})
	require.NoError(t, err)
	for _, to := range []string{types.SopItemStatusTodo, types.SopItemStatusInProgress} {
		require.NoError(t, env.Items.UpdateStatus(ctx, 1, target.ID, to))
	}

	// 2) 黑板记一条决策
	require.NoError(t, env.Notes.Create(ctx, 1, "u1", &types.SopProjectNote{
		ProjectID: env.Project.ID, Kind: types.SopNoteKindDecision,
		Title: "采用方案 B", Body: "因为可逆",
	}))

	// 3) 起运行并等待成功
	run := env.createRun(t, target.ID)
	require.NoError(t, env.Engine.Start(ctx))
	defer func() { _ = env.Engine.Stop(ctx) }()
	require.Eventually(t, func() bool { return env.runStatus(t, run.ID) == types.SopRunSucceeded },
		20*time.Second, 200*time.Millisecond)

	// 4) 任务书里带上了项目现状：决策 + 兄弟事项进度
	require.NotEmpty(t, env.Agent.prompts)
	joined := strings.Join(env.Agent.prompts, "\n----\n")
	require.Contains(t, joined, "## 项目现状")
	require.Contains(t, joined, "采用方案 B")
	require.Contains(t, joined, "已完成")

	// 5) 简报物化到工作区 inputs/project-brief.md
	got, err := env.Engine.Deps().Runs.GetRun(ctx, 1, run.ID)
	require.NoError(t, err)
	briefFile := filepath.Join(got.WorkspaceDir, "inputs", "project-brief.md")
	raw, err := os.ReadFile(briefFile)
	require.NoError(t, err)
	require.Contains(t, string(raw), "采用方案 B")
	require.Contains(t, got.ProjectContext, "采用方案 B", "运行详情「任务书」要能展示本次注入的项目现状")

	// 6) 运行终态自动写黑板（run_summary）
	require.Eventually(t, func() bool { return env.noteCount(t, types.SopNoteKindRunSummary) >= 1 },
		10*time.Second, 200*time.Millisecond)

	// 7) 简报同步进 KB：首版入库且正文含运行结论
	require.NoError(t, env.Brief.EnsureInitial(ctx, 1, env.Project.ID))
	require.Equal(t, 1, env.KB.createCalls)
	require.Contains(t, env.KB.lastTitle, "项目简报")
	require.Contains(t, env.KB.lastContent, "运行结论")
	require.GreaterOrEqual(t, env.Triggers.Load(), int64(2), "黑板写入（含运行摘要）都应要求重渲染")

	// 8) 再记一条只前进水位，不重复建文档
	require.NoError(t, env.Notes.Create(ctx, 1, "u1", &types.SopProjectNote{
		ProjectID: env.Project.ID, Kind: types.SopNoteKindRisk, Title: "索引延迟", Body: "需观察",
	}))
	require.NoError(t, env.Brief.EnsureInitial(ctx, 1, env.Project.ID))
	require.Equal(t, 1, env.KB.updateCalls)
	require.Equal(t, 1, env.KB.createCalls, "简报永远只有一份文档")
}

// TestSplitThreeChildrenEachRunIndependently 覆盖 spec §16.24 拆分主线：
// 3 个子项各自跑一条运行、父项汇总计数、父项自动 done、五态计数之和 = 顶层事项数。
func TestSplitThreeChildrenEachRunIndependently(t *testing.T) {
	env := newBriefEnv(t)
	ctx := context.Background()

	parent, err := env.Items.Create(ctx, 1, "u1", env.Project.ID, sop.CreateItemInput{Title: "大需求"})
	require.NoError(t, err)
	kids, err := env.Items.Split(ctx, 1, "u1", parent.ID, []types.SopChildItemSpec{
		{Title: "子一"}, {Title: "子二"}, {Title: "子三"},
	}, types.SopSplitSourceManual)
	require.NoError(t, err)
	require.Len(t, kids, 3)

	require.NoError(t, env.Engine.Start(ctx))
	defer func() { _ = env.Engine.Stop(ctx) }()

	runIDs := make([]string, 0, 3)
	for i := range kids {
		require.NoError(t, env.Items.UpdateStatus(ctx, 1, kids[i].ID, types.SopItemStatusTodo))
		run := env.createRun(t, kids[i].ID)
		runIDs = append(runIDs, run.ID)
	}
	require.Eventually(t, func() bool {
		for _, id := range runIDs {
			if env.runStatus(t, id) != types.SopRunSucceeded {
				return false
			}
		}
		return true
	}, 30*time.Second, 200*time.Millisecond)

	seen := map[string]bool{}
	for _, id := range runIDs {
		got, err := env.Engine.Deps().Runs.GetRun(ctx, 1, id)
		require.NoError(t, err)
		require.False(t, seen[got.ItemID], "三条运行必须属于不同子项")
		seen[got.ItemID] = true
	}
	require.Len(t, seen, 3)

	// 子项成功后事项进入 in_review（模板未声明 on_success），逐个推到 done
	for i := range kids {
		require.NoError(t, env.Items.UpdateStatus(ctx, 1, kids[i].ID, types.SopItemStatusDone))
	}

	// 父项汇总：3/3 完成、执行情况 closed、状态自动 done
	list, err := env.Items.ListWithDerived(ctx, 1, env.Project.ID, types.SopItemFilter{})
	require.NoError(t, err)
	byID := map[string]types.SopItemWithDerived{}
	for _, it := range list {
		byID[it.ID] = it
	}
	require.True(t, byID[parent.ID].IsBig)
	require.Equal(t, 3, byID[parent.ID].ChildTotal)
	require.Equal(t, 3, byID[parent.ID].ChildDone)
	require.Equal(t, types.SopExecStateClosed, byID[parent.ID].ChildExec)
	require.Equal(t, types.SopItemStatusDone, byID[parent.ID].Status, "全部子项完成后父项自动 done")

	// 五态计数之和 = 顶层事项数（跳过子项，避免双计）
	top, none, running, intervene, pr, closed := 0, 0, 0, 0, 0, 0
	for _, it := range list {
		if it.ParentID != "" {
			continue
		}
		top++
		switch it.ExecState {
		case types.SopExecStateNone:
			none++
		case types.SopExecStateRunning:
			running++
		case types.SopExecStateIntervene:
			intervene++
		case types.SopExecStatePR:
			pr++
		case types.SopExecStateClosed:
			closed++
		}
	}
	require.Equal(t, 1, top, "只有父项是顶层事项")
	require.Equal(t, top, none+running+intervene+pr+closed)
	require.Equal(t, 1, closed)
	require.Equal(t, 0, none+running+intervene+pr)
}
