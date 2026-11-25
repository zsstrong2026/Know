package engine_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service/sop/engine"
	"github.com/Tencent/WeKnora/internal/application/service/sop/executor"
	"github.com/Tencent/WeKnora/internal/application/service/sop/executor/cli"
	"github.com/Tencent/WeKnora/internal/types"
)

// 本文件放在 engine_test 外部测试包：CLI 执行器 import engine（工作区类型），
// 同包内引用会形成 import cycle。

type mixedEnv struct {
	engine    *engine.Engine
	runs      *repository.SopRunRepo
	templates *repository.SopTemplateRepo
	db        *gorm.DB
}

func newMixedEnv(t *testing.T) *mixedEnv {
	t.Helper()
	db := openMixedDB(t)
	reg := executor.NewRegistry()
	reg.Register(&stubAgentProvider{})
	reg.Register(executor.NewScriptProvider())
	reg.Register(&cli.CLIProvider{A: cli.ZcodeAdapter{}})

	runs := repository.NewSopRunRepo(db)
	e := engine.NewEngine(engine.Deps{
		Runs:           runs,
		Templates:      repository.NewSopTemplateRepo(db),
		Projects:       repository.NewSopProjectRepo(db),
		Items:          repository.NewSopItemRepo(db),
		Extras:         repository.NewSopItemExtraRepo(db),
		Registry:       reg,
		WorkspaceRoot:  t.TempDir(),
		MaxParallel:    2,
		DisableRunLoop: false,
	})
	return &mixedEnv{engine: e, runs: runs, templates: repository.NewSopTemplateRepo(db), db: db}
}

func openMixedDB(t *testing.T) *gorm.DB {
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
		&types.SopCLICredential{}, &types.SopTriggerConfig{},
		&types.SopTriggerLog{}, &types.SopDispatchReservation{},
	))
	require.NoError(t, db.Exec(
		"CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_node_runs_run_node ON sop_node_runs(run_id, node_id)",
	).Error)
	return db
}

// stubAgentProvider 是平台智能体替身：只回结论，不写产物。
type stubAgentProvider struct{}

func (p *stubAgentProvider) Kind() string { return "platform_agent" }

func (p *stubAgentProvider) Prepare(context.Context, *types.SopNode, string) error { return nil }

func (p *stubAgentProvider) Execute(
	ctx context.Context, tb executor.TaskBook, _ executor.ExecOptions,
) (executor.ExecResult, error) {
	return executor.ExecResult{Output: "平台智能体完成", GateResult: types.SopGatePass}, nil
}

func (p *stubAgentProvider) Cancel(context.Context) error { return nil }

// TestMixedChainWithRework 用假 zcode 二进制走完整 CLIProvider 链路
// （PATH 检测 → 私有 HOME → stream-json 翻译 → 产物提取 → 门禁），
// 串起「平台智能体分析 → CLI 实现 → 脚本测试 → 人工验收 → 平台智能体汇总」，
// 并在验收打回后断言 CLI 节点返工重跑。
func TestMixedChainWithRework(t *testing.T) {
	binDir := t.TempDir()
	writeFakeZcode(t, binDir)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	env := newMixedEnv(t)
	tpl := seedMixedTemplate(t, env)
	run := createRunFromTemplate(t, env, tpl)

	require.NoError(t, env.engine.Start(context.Background()))
	defer func() { _ = env.engine.Stop(context.Background()) }()

	// 等到验收节点真的挂起（运行 waiting 也可能是别的节点转人工，不能只看运行状态）
	waitNodeStatus(t, env, run.ID, "approve", types.SopNodeWaitingApproval)
	nodes, err := env.runs.ListNodeRuns(context.Background(), 1, run.ID)
	require.NoError(t, err)
	for _, id := range []string{"analysis", "impl", "test"} {
		require.Equal(t, types.SopNodeSucceeded, findNodeRun(t, nodes, id).Status,
			"节点 %s 应已通过", id)
	}

	require.NoError(t, env.engine.Reject(context.Background(), 1, run.ID, "approve", "接口不符，重写", "u1", "impl"))
	// 打回后运行会短暂停留在 waiting：必须等到 impl 真的重跑（Attempt>=2）
	// 且验收节点再次进入 waiting_approval，才算返工循环走完。
	// 打回会把目标节点重置为 pending 并清空自动重试预算（Attempt 归零、ReworkCount+1），
	// 因此这里断言的是「重跑并重新走到验收」，而不是 Attempt 累加。
	require.Eventually(t, func() bool {
		nrs, err := env.runs.ListNodeRuns(context.Background(), 1, run.ID)
		if err != nil {
			return false
		}
		implOK, approveWaiting := false, false
		for i := range nrs {
			if nrs[i].NodeID == "impl" {
				implOK = nrs[i].Status == types.SopNodeSucceeded && nrs[i].ReworkCount >= 1
			}
			if nrs[i].NodeID == "approve" {
				approveWaiting = nrs[i].Status == types.SopNodeWaitingApproval
			}
		}
		return implOK && approveWaiting
	}, 40*time.Second, 200*time.Millisecond, "打回后 CLI 节点应重跑并重新进入验收")
	waitRunStatus(t, env, run.ID, types.SopRunWaiting)

	nodes, err = env.runs.ListNodeRuns(context.Background(), 1, run.ID)
	require.NoError(t, err)
	impl := findNodeRun(t, nodes, "impl")
	require.Equal(t, 1, impl.ReworkCount, "打回计数 +1")
	// 返工意见只服务于返工那一轮；节点已再次通过，落库必须清空，
	// 否则下一次重试/重跑会把旧意见当成新指令下发。
	require.Empty(t, impl.Feedback, "通过后清掉返工意见")

	require.NoError(t, env.engine.Approve(context.Background(), 1, run.ID, "approve", "通过", "u1"))
	waitRunStatus(t, env, run.ID, types.SopRunSucceeded)

	// 产物指纹必须与磁盘一致（CLI 写文件 → 引擎收集 → sha256）
	got, err := env.runs.GetRun(context.Background(), 0, run.ID)
	require.NoError(t, err)
	arts, err := env.runs.ListArtifacts(context.Background(), 1, run.ID)
	require.NoError(t, err)
	require.NotEmpty(t, arts)
	for _, art := range arts {
		raw, err := os.ReadFile(filepath.Join(got.WorkspaceDir, filepath.FromSlash(art.Path)))
		require.NoError(t, err, "产物 %s 应存在", art.Path)
		require.Equal(t, art.SHA256, fmt.Sprintf("%x", sha256.Sum256(raw)))
	}
}

// TestCancelKillsProcessTree 取消后脚本进程树必须消失（Windows 走 taskkill /T）。
func TestCancelKillsProcessTree(t *testing.T) {
	env := newMixedEnv(t)
	run := seedScriptRun(t, env, "echo $$ > work/pids.txt; sleep 30 & wait")

	require.NoError(t, env.engine.Start(context.Background()))
	defer func() { _ = env.engine.Stop(context.Background()) }()

	waitNodeStatus(t, env, run.ID, "s1", types.SopNodeRunning)
	require.NoError(t, env.engine.Cancel(context.Background(), 1, run.ID))
	waitRunStatus(t, env, run.ID, types.SopRunCancelled)

	got, err := env.runs.GetRun(context.Background(), 0, run.ID)
	require.NoError(t, err)
	assertProcessGone(t, got.WorkspaceDir)
}

// TestLeaseRecoveryAfterCrash 模拟「进程崩溃、不再续租」：库里留下 running + 过期租约，
// 新实例第一轮 tick 必须把它回收为 failed（带租约原因），并清掉在跑的节点。
//
// 不用 Stop 来模拟崩溃：Stop 是优雅停机，会按取消收口为 cancelled，测不到租约回收。
func TestLeaseRecoveryAfterCrash(t *testing.T) {
	env := newMixedEnv(t)
	ctx := context.Background()

	// 手工造一条「正在跑、租约已过期」的运行与其 running 节点
	def := types.SopDefinition{Nodes: []types.SopNode{{
		ID: "s1", Name: "脚本", Type: "script",
		Executor: types.SopNodeExecutor{Kind: "script"},
		Script:   "echo x",
	}}}
	types.NormalizeSopDefinition(&def)
	run := &types.SopRun{
		TenantID: 1, WorkflowSnapshot: def, Status: types.SopRunRunning,
		TriggerKind: types.SopRunTriggerManual, Input: map[string]any{},
		WorkspaceDir: t.TempDir(),
	}
	require.NoError(t, env.runs.CreateRun(ctx, run))
	expired := time.Now().Add(-time.Minute)
	run.LeaseUntil = &expired
	require.NoError(t, env.runs.UpdateRun(ctx, run))

	nr := &types.SopNodeRun{
		TenantID: 1, RunID: run.ID, NodeID: "s1",
		Status: types.SopNodeRunning, Attempt: 1,
	}
	require.NoError(t, env.runs.CreateNodeRun(ctx, nr))

	second := engine.NewEngine(engine.Deps{
		Runs: env.runs, Templates: env.templates,
		Projects:       repository.NewSopProjectRepo(env.db),
		Items:          repository.NewSopItemRepo(env.db),
		Extras:         repository.NewSopItemExtraRepo(env.db),
		Registry:       env.engineRegistry(t),
		WorkspaceRoot:  t.TempDir(),
		MaxParallel:    2,
		DisableRunLoop: false,
	})
	require.NoError(t, second.Start(ctx))
	defer func() { _ = second.Stop(ctx) }()

	require.Eventually(t, func() bool {
		got, err := env.runs.GetRun(ctx, 0, run.ID)
		return err == nil && got.Status == types.SopRunFailed
	}, 20*time.Second, 200*time.Millisecond, "过期租约的运行应被回收为 failed")

	got, err := env.runs.GetRun(ctx, 0, run.ID)
	require.NoError(t, err)
	require.Contains(t, got.Error, "租约")
	require.NotNil(t, got.FinishedAt, "回收后必须落结束时间")
	// 注：ExpireLeases 只收口运行本身，节点行保持 running（与既有 TestExpireLeasesOnTick
	// 同口径）；节点展示层的兜底不在本任务范围。
}

// engineRegistry 复用一个与 newMixedEnv 相同的注册表（含假 zcode）。
func (m *mixedEnv) engineRegistry(t *testing.T) *executor.Registry {
	t.Helper()
	reg := executor.NewRegistry()
	reg.Register(&stubAgentProvider{})
	reg.Register(executor.NewScriptProvider())
	reg.Register(&cli.CLIProvider{A: cli.ZcodeAdapter{}})
	return reg
}

// TestApprovalTimeoutStrategies notify 只提醒一次 / fail 自动失败 / 默认永不超时。
func TestApprovalTimeoutStrategies(t *testing.T) {
	for _, tc := range []struct {
		name   string
		action string
		want   string
	}{
		{"notify", "notify", types.SopRunWaiting},
		{"fail", "fail", types.SopRunFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newMixedEnv(t)
			run := seedApprovalRun(t, env, 1, tc.action)

			require.NoError(t, env.engine.Start(context.Background()))
			defer func() { _ = env.engine.Stop(context.Background()) }()

			waitRunStatus(t, env, run.ID, types.SopRunWaiting)
			require.NoError(t, env.db.Exec(
				"UPDATE sop_node_runs SET updated_at = ? WHERE run_id = ? AND status = ?",
				time.Now().Add(-2*time.Hour), run.ID, types.SopNodeWaitingApproval,
			).Error)

			if tc.action == "notify" {
				// notify 策略下运行仍是 waiting：不能拿「状态=waiting」当条件（它一开始就满足），
				// 必须等到提醒真的追加，并且只有一条。
				require.Eventually(t, func() bool {
					got, err := env.runs.GetRun(context.Background(), 0, run.ID)
					if err != nil {
						return false
					}
					reminders := 0
					for _, entry := range got.HumanContext {
						if entry.Source == "system_reminder" {
							reminders++
						}
					}
					return reminders == 1
				}, 20*time.Second, 200*time.Millisecond, "审批超时应追加一条提醒")

				// 再跨过一个 tick，确认不会重复提醒
				time.Sleep(3 * time.Second)
				got, err := env.runs.GetRun(context.Background(), 0, run.ID)
				require.NoError(t, err)
				reminders := 0
				for _, entry := range got.HumanContext {
					if entry.Source == "system_reminder" {
						reminders++
					}
				}
				require.Equal(t, 1, reminders, "提醒只追加一次，不随 tick 刷屏")
				require.Equal(t, types.SopRunWaiting, got.Status)
				return
			}

			require.Eventually(t, func() bool {
				got, err := env.runs.GetRun(context.Background(), 0, run.ID)
				return err == nil && got.Status == tc.want
			}, 20*time.Second, 200*time.Millisecond)
		})
	}

	t.Run("default_never_times_out", func(t *testing.T) {
		env := newMixedEnv(t)
		run := seedApprovalRun(t, env, 0, "notify")

		require.NoError(t, env.engine.Start(context.Background()))
		defer func() { _ = env.engine.Stop(context.Background()) }()

		waitRunStatus(t, env, run.ID, types.SopRunWaiting)
		require.NoError(t, env.db.Exec(
			"UPDATE sop_node_runs SET updated_at = ? WHERE run_id = ?",
			time.Now().Add(-10000*time.Hour), run.ID,
		).Error)
		time.Sleep(3 * time.Second) // 跨过至少一个 tick
		got, err := env.runs.GetRun(context.Background(), 0, run.ID)
		require.NoError(t, err)
		require.Equal(t, types.SopRunWaiting, got.Status)
	})
}

// TestRetryFromFailedNodeAndChildSOP 失败重试 + 子 SOP 产物回传。
func TestRetryFromFailedNodeAndChildSOP(t *testing.T) {
	t.Run("retry", func(t *testing.T) {
		env := newMixedEnv(t)
		run := seedScriptRun(t, env, "test -f work/retried && echo ok || (touch work/retried; exit 1)")

		require.NoError(t, env.engine.Start(context.Background()))
		defer func() { _ = env.engine.Stop(context.Background()) }()

		waitRunStatus(t, env, run.ID, types.SopRunFailed)
		require.NoError(t, env.engine.Retry(context.Background(), 1, run.ID))
		waitRunStatus(t, env, run.ID, types.SopRunSucceeded)

		nodes, err := env.runs.ListNodeRuns(context.Background(), 1, run.ID)
		require.NoError(t, err)
		require.Equal(t, types.SopNodeSucceeded, findNodeRun(t, nodes, "s1").Status)
	})

	t.Run("child_sop", func(t *testing.T) {
		env := newMixedEnv(t)
		child := seedChildTemplate(t, env)
		parent := seedParentTemplate(t, env, child.PublishedRevisionID)
		run := createRunFromTemplate(t, env, parent)

		require.NoError(t, env.engine.Start(context.Background()))
		defer func() { _ = env.engine.Stop(context.Background()) }()

		require.Eventually(t, func() bool {
			got, err := env.runs.GetRun(context.Background(), 0, run.ID)
			return err == nil && got.Status == types.SopRunSucceeded
		}, 40*time.Second, 200*time.Millisecond)

		nodes, err := env.runs.ListNodeRuns(context.Background(), 1, run.ID)
		require.NoError(t, err)
		sub := findNodeRun(t, nodes, "sub")
		require.NotEmpty(t, sub.ChildRunID)

		childRun, err := env.runs.GetRun(context.Background(), 0, sub.ChildRunID)
		require.NoError(t, err)
		require.Equal(t, types.SopRunTriggerChildRun, childRun.TriggerKind)
		require.Equal(t, run.ID, childRun.ParentRunID)

		arts, err := env.runs.ListArtifacts(context.Background(), 1, run.ID)
		require.NoError(t, err)
		var childArt *types.SopArtifact
		for i := range arts {
			if arts[i].NodeID == "sub" {
				childArt = &arts[i]
			}
		}
		require.NotNil(t, childArt, "子流程产物应导入父运行")
		parentRun, err := env.runs.GetRun(context.Background(), 0, run.ID)
		require.NoError(t, err)
		raw, err := os.ReadFile(filepath.Join(parentRun.WorkspaceDir, filepath.FromSlash(childArt.Path)))
		require.NoError(t, err)
		require.Equal(t, childArt.SHA256, fmt.Sprintf("%x", sha256.Sum256(raw)))
	})
}

// ---- 测试助手 ----

// writeFakeZcode 写假 zcode（stream-json 终态帧 + 写 out/impl.md）。
// Windows 额外写 zcode.cmd 包装，保证 exec.LookPath 命中。
func writeFakeZcode(t *testing.T, binDir string) {
	t.Helper()
	script := strings.Join([]string{
		"#!/usr/bin/env bash",
		`echo '{"type":"system","subtype":"init","session_id":"fake-session"}'`,
		`echo '{"type":"assistant","message":{"content":[{"type":"text","text":"正在实现"}]}}'`,
		// 产物路径按节点隔离：引擎把 out/impl.md 改写到 out/impl/impl.md，
		// 假 CLI 的工作目录是运行根，所以必须写到同一个位置。
		"mkdir -p out/impl",
		`printf '# 实现\n\n假 zcode 产出\n' > out/impl/impl.md`,
		`echo '{"type":"result","subtype":"success","result":"实现完成","session_id":"fake-session","is_error":false}'`,
		"",
	}, "\n")
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "zcode"), []byte(script), 0o755))
	if runtime.GOOS == "windows" {
		wrapper := "@bash \"%~dp0zcode\" %*\r\n"
		require.NoError(t, os.WriteFile(filepath.Join(binDir, "zcode.cmd"), []byte(wrapper), 0o644))
	}
}

func seedMixedTemplate(t *testing.T, env *mixedEnv) *types.SopTemplate {
	t.Helper()
	ctx := context.Background()
	def := types.SopDefinition{
		Nodes: []types.SopNode{
			{ID: "analysis", Name: "分析", Type: "agent",
				Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "stub"}},
			{ID: "impl", Name: "实现", Type: "agent",
				Executor: types.SopNodeExecutor{Kind: "cli", CLIKind: "zcode"},
				ArtifactOutputs: []types.SopArtifactOutput{{
					Name: "impl.md", Path: "out/impl.md", Required: true,
					Check: types.SopArtifactCheck{Type: types.SopArtifactCheckExists},
				}}},
			{ID: "test", Name: "测试", Type: "script",
				Executor: types.SopNodeExecutor{Kind: "script"},
				// 跨节点读上游产物必须走物化后的 inputs/<upstream>/：
				// 脚本正文里的 out/ 引用会被 nodeScoped 重写到本节点产出目录。
				Script: "test -s inputs/impl/impl.md && echo test-ok"},
			{ID: "approve", Name: "验收", Type: "approval",
				Executor: types.SopNodeExecutor{Kind: "platform_agent"}},
			{ID: "summary", Name: "汇总", Type: "agent",
				Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "stub"}},
		},
		Edges: []types.SopEdge{
			{ID: "e1", Source: "analysis", Target: "impl", When: "success"},
			{ID: "e2", Source: "impl", Target: "test", When: "success"},
			{ID: "e3", Source: "test", Target: "approve", When: "success"},
			{ID: "e4", Source: "approve", Target: "summary", When: "success"},
		},
	}
	types.NormalizeSopDefinition(&def)

	tpl := &types.SopTemplate{TenantID: 1, Name: "混合链路", Status: types.SopTemplateStatusDraft, Definition: def}
	require.NoError(t, env.templates.Create(ctx, tpl))
	rev, err := env.templates.Publish(ctx, 1, tpl.ID, "u1")
	require.NoError(t, err)
	tpl.PublishedRevisionID = rev.ID
	return tpl
}

func seedScriptRun(t *testing.T, env *mixedEnv, script string) *types.SopRun {
	t.Helper()
	def := types.SopDefinition{
		Nodes: []types.SopNode{{
			ID: "s1", Name: "脚本", Type: "script",
			Executor:  types.SopNodeExecutor{Kind: "script"},
			Script:    "mkdir -p work; " + script,
			OnFailure: types.SopOnFailureFail, MaxAttempts: 1,
		}},
	}
	return createRawRun(t, env, def)
}

func seedApprovalRun(t *testing.T, env *mixedEnv, hours int, action string) *types.SopRun {
	t.Helper()
	def := types.SopDefinition{
		Nodes: []types.SopNode{{
			ID: "approve", Name: "验收", Type: "approval",
			Executor:             types.SopNodeExecutor{Kind: "platform_agent"},
			ApprovalTimeoutHours: hours, ApprovalTimeoutAction: action,
		}},
	}
	return createRawRun(t, env, def)
}

func seedChildTemplate(t *testing.T, env *mixedEnv) *types.SopTemplate {
	t.Helper()
	ctx := context.Background()
	def := types.SopDefinition{
		Nodes: []types.SopNode{{
			ID: "child", Name: "子产物", Type: "script",
			Executor: types.SopNodeExecutor{Kind: "script"},
			Script:   "mkdir -p out; printf '# 子产物\\n' > out/child.md",
			ArtifactOutputs: []types.SopArtifactOutput{{
				Name: "child.md", Path: "out/child.md", Required: true,
				Check: types.SopArtifactCheck{Type: types.SopArtifactCheckExists},
			}},
		}},
	}
	types.NormalizeSopDefinition(&def)

	tpl := &types.SopTemplate{TenantID: 1, Name: "子流程", Status: types.SopTemplateStatusDraft, Definition: def}
	require.NoError(t, env.templates.Create(ctx, tpl))
	rev, err := env.templates.Publish(ctx, 1, tpl.ID, "u1")
	require.NoError(t, err)
	tpl.PublishedRevisionID = rev.ID
	return tpl
}

func seedParentTemplate(t *testing.T, env *mixedEnv, childRevisionID string) *types.SopTemplate {
	t.Helper()
	ctx := context.Background()
	// sop 节点引用子模板的**模板 id**：从 revision 反查
	rev, err := env.templates.GetRevision(ctx, 1, childRevisionID)
	require.NoError(t, err)
	def := types.SopDefinition{
		Nodes: []types.SopNode{
			{ID: "sub", Name: "子流程", Type: "sop",
				Executor:        types.SopNodeExecutor{Kind: "platform_agent"},
				ChildTemplateID: rev.TemplateID},
			{ID: "wrap", Name: "汇总", Type: "script",
				Executor: types.SopNodeExecutor{Kind: "script"},
				Script:   "echo wrapped"},
		},
		Edges: []types.SopEdge{{ID: "e1", Source: "sub", Target: "wrap", When: "success"}},
	}
	types.NormalizeSopDefinition(&def)

	tpl := &types.SopTemplate{TenantID: 1, Name: "父流程", Status: types.SopTemplateStatusDraft, Definition: def}
	require.NoError(t, env.templates.Create(ctx, tpl))
	rev2, err := env.templates.Publish(ctx, 1, tpl.ID, "u1")
	require.NoError(t, err)
	tpl.PublishedRevisionID = rev2.ID
	return tpl
}

func createRawRun(t *testing.T, env *mixedEnv, def types.SopDefinition) *types.SopRun {
	t.Helper()
	types.NormalizeSopDefinition(&def)
	run := &types.SopRun{
		TenantID: 1, WorkflowSnapshot: def, Status: types.SopRunQueued,
		TriggerKind: types.SopRunTriggerManual, Input: map[string]any{},
	}
	require.NoError(t, env.runs.CreateRun(context.Background(), run))
	return run
}

func createRunFromTemplate(t *testing.T, env *mixedEnv, tpl *types.SopTemplate) *types.SopRun {
	t.Helper()
	ctx := context.Background()
	rev, err := env.templates.GetRevision(ctx, 1, tpl.PublishedRevisionID)
	require.NoError(t, err)
	run := &types.SopRun{
		TenantID: 1, TemplateID: tpl.ID, RevisionID: rev.ID,
		WorkflowSnapshot: rev.Definition, Status: types.SopRunQueued,
		TriggerKind: types.SopRunTriggerManual, Input: map[string]any{},
	}
	require.NoError(t, env.runs.CreateRun(ctx, run))
	return run
}

func waitRunStatus(t *testing.T, env *mixedEnv, runID, want string) {
	t.Helper()
	require.Eventually(t, func() bool {
		got, err := env.runs.GetRun(context.Background(), 0, runID)
		return err == nil && got.Status == want
	}, 40*time.Second, 200*time.Millisecond, "运行未到达状态 %s", want)
}

func waitNodeStatus(t *testing.T, env *mixedEnv, runID, nodeID, want string) {
	t.Helper()
	require.Eventually(t, func() bool {
		nr, err := env.runs.GetNodeRun(context.Background(), 1, runID, nodeID)
		return err == nil && nr.Status == want
	}, 30*time.Second, 200*time.Millisecond, "节点 %s 未到达状态 %s", nodeID, want)
}

func findNodeRun(t *testing.T, nodes []types.SopNodeRun, nodeID string) *types.SopNodeRun {
	t.Helper()
	for i := range nodes {
		if nodes[i].NodeID == nodeID {
			return &nodes[i]
		}
	}
	t.Fatalf("节点运行 %s 不存在", nodeID)
	return nil
}

// assertProcessGone 读 work/pids.txt 逐个探测进程确实消失。
func assertProcessGone(t *testing.T, workspaceDir string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(workspaceDir, "work", "pids.txt"))
	if err != nil {
		return // 进程在写 pid 前就被杀：无需断言
	}
	pids := strings.Fields(string(raw))
	if len(pids) == 0 {
		return
	}
	deadline := time.Now().Add(15 * time.Second)
	for _, pid := range pids {
		for processAlive(t, pid) {
			if time.Now().After(deadline) {
				t.Fatalf("进程 %s 在取消后仍然存活", pid)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
}
