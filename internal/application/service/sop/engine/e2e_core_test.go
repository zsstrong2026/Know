package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service/sop/executor"
	"github.com/Tencent/WeKnora/internal/types"
)

// TestCoreChainE2E 用真实仓储 + 真实调度循环跑通「分析 → 脚本校验 → 人工验收 → 汇总」：
// sqlite :memory: 恒跑；设置 SOP_TEST_PG_DSN 时追加 PG 子测试（迁移由 make migrate-up 预先跑过）。
func TestCoreChainE2E(t *testing.T) {
	drivers := []struct {
		name string
		open func(t *testing.T) *gorm.DB
	}{
		{"sqlite", openSqliteMem},
		{"postgres", func(t *testing.T) *gorm.DB {
			dsn := os.Getenv("SOP_TEST_PG_DSN")
			if dsn == "" {
				t.Skip("SOP_TEST_PG_DSN 未设置（CI/本机无 PG 时跳过）")
			}
			db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
				Logger: logger.Default.LogMode(logger.Silent),
			})
			require.NoError(t, err)
			cleanSopTables(t, db)
			return db
		}},
	}

	for _, driver := range drivers {
		t.Run(driver.name, func(t *testing.T) {
			db := driver.open(t)
			e := newRealEngine(t, db)

			tpl := seedChainTemplate(t, db)
			run := createRunFromTemplate(t, e, tpl)

			require.NoError(t, e.Start(context.Background()))
			defer func() { _ = e.Stop(context.Background()) }()

			// 跑到审批节点后必须停在 waiting（不会自动通过）
			waitFor(t, 20*time.Second, func() bool {
				got, err := e.deps.Runs.GetRun(context.Background(), 0, run.ID)
				return err == nil && got.Status == types.SopRunWaiting
			})

			require.NoError(t, e.Approve(context.Background(), 1, run.ID, "approve", "验收通过", "u1"))

			waitFor(t, 20*time.Second, func() bool {
				got, err := e.deps.Runs.GetRun(context.Background(), 0, run.ID)
				return err == nil && got.Status == types.SopRunSucceeded
			})

			arts, err := e.deps.Runs.ListArtifacts(context.Background(), 1, run.ID)
			require.NoError(t, err)
			require.GreaterOrEqual(t, len(arts), 2, "分析与汇总节点各应留下产物")
			for _, art := range arts {
				require.Len(t, art.SHA256, 64, "产物指纹必须齐全")
			}
		})
	}
}

// openSqliteMem 建与生产同构的 sqlite 内存库（复用 engine 测试的 AutoMigrate 口径）。
func openSqliteMem(t *testing.T) *gorm.DB {
	t.Helper()
	return newSopEngineTestDB(t)
}

// cleanSopTables 按依赖顺序清空 sop_* 表（PG 专用；sqlite 每个测试都是新内存库）。
func cleanSopTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	tables := []string{
		"sop_dispatch_reservations",
		"sop_trigger_logs",
		"sop_trigger_configs",
		"sop_cli_credentials",
		"sop_artifacts",
		"sop_node_runs",
		"sop_runs",
		"sop_template_revisions",
		"sop_templates",
		"sop_item_activities",
		"sop_item_clarifications",
		"sop_item_attachments",
		"sop_items",
		"sop_projects",
	}
	for _, table := range tables {
		if err := db.Exec("DELETE FROM " + table).Error; err != nil {
			t.Fatalf("清空 %s 失败: %v", table, err)
		}
	}
}

// newRealEngine 真实仓储 + 真实 ScriptProvider + stub AgentProvider：
// agent 节点固定写出 out/a.md 并汇报 pass，脚本节点走真实 bash。
func newRealEngine(t *testing.T, db *gorm.DB) *Engine {
	t.Helper()
	reg := executor.NewRegistry()
	reg.Register(&fakeProvider{kind: "platform_agent", out: "分析完成"})
	reg.Register(executor.NewScriptProvider())

	return NewEngine(Deps{
		Runs:           repository.NewSopRunRepo(db),
		Templates:      repository.NewSopTemplateRepo(db),
		Projects:       repository.NewSopProjectRepo(db),
		Items:          repository.NewSopItemRepo(db),
		Extras:         repository.NewSopItemExtraRepo(db),
		Registry:       reg,
		ItemStatus:     &stubItemStatus{},
		WorkspaceRoot:  t.TempDir(),
		MaxParallel:    2,
		DisableRunLoop: false, // 真实调度循环（TickOnce + runLoop）
	})
}

// seedChainTemplate 建并发布四节点模板：分析 → 脚本校验 → 人工验收 → 汇总。
// 分析与汇总各声明 out/a.md 产物（引擎会按 out/<node>/ 改写路径），
// 所以同一个 stub 的写文件逻辑能同时满足两个节点。
func seedChainTemplate(t *testing.T, db *gorm.DB) *types.SopTemplate {
	t.Helper()
	ctx := context.Background()
	repo := repository.NewSopTemplateRepo(db)
	def := types.SopDefinition{
		Nodes: []types.SopNode{
			{
				ID: "analysis", Name: "分析", Type: "agent",
				Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "stub"},
				ArtifactOutputs: []types.SopArtifactOutput{{
					Name: "a.md", Path: "out/a.md", Required: true,
					Check: types.SopArtifactCheck{Type: types.SopArtifactCheckExists},
				}},
			},
			{
				ID: "check", Name: "校验", Type: "script",
				Executor: types.SopNodeExecutor{Kind: "script"},
				Script:   "echo check-ok",
			},
			{
				ID: "approve", Name: "验收", Type: "approval",
				Executor: types.SopNodeExecutor{Kind: "platform_agent"},
			},
			{
				ID: "summary", Name: "汇总", Type: "agent",
				Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "stub"},
				ArtifactOutputs: []types.SopArtifactOutput{{
					Name: "a.md", Path: "out/a.md", Required: true,
					Check: types.SopArtifactCheck{Type: types.SopArtifactCheckExists},
				}},
			},
		},
		Edges: []types.SopEdge{
			{ID: "e1", Source: "analysis", Target: "check", When: "success"},
			{ID: "e2", Source: "check", Target: "approve", When: "success"},
			{ID: "e3", Source: "approve", Target: "summary", When: "success"},
		},
	}
	types.NormalizeSopDefinition(&def)

	tpl := &types.SopTemplate{TenantID: 1, Name: "核心链路", Status: types.SopTemplateStatusDraft, Definition: def}
	require.NoError(t, repo.Create(ctx, tpl))
	rev, err := repo.Publish(ctx, 1, tpl.ID, "u1")
	require.NoError(t, err)
	tpl.PublishedRevisionID = rev.ID
	return tpl
}

// createRunFromTemplate 直接落一条 queued 运行，交给调度器认领（等价于 HTTP CreateRun）。
func createRunFromTemplate(t *testing.T, e *Engine, tpl *types.SopTemplate) *types.SopRun {
	t.Helper()
	ctx := context.Background()
	rev, err := e.deps.Templates.GetRevision(ctx, 1, tpl.PublishedRevisionID)
	require.NoError(t, err)

	run := &types.SopRun{
		TenantID: 1, TemplateID: tpl.ID, RevisionID: rev.ID,
		WorkflowSnapshot: rev.Definition, Status: types.SopRunQueued,
		TriggerKind: types.SopRunTriggerManual, Input: map[string]any{},
	}
	require.NoError(t, e.deps.Runs.CreateRun(ctx, run))
	return run
}

// waitFor 是 require.Eventually 的薄包装（失败时给出可读的最终状态）。
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	require.Eventually(t, cond, timeout, 200*time.Millisecond, "等待条件在 %s 内未满足", timeout)
}

var _ = filepath.Join
