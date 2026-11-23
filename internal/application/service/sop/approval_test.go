package sop

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service/sop/engine"
	"github.com/Tencent/WeKnora/internal/application/service/sop/executor"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// stubApprover 记录委托调用（ApprovalService 的引擎侧替身）。
type stubApprover struct {
	calls []string
	err   error
}

func (s *stubApprover) Approve(_ context.Context, _ uint64, runID, nodeID, comment, actor string) error {
	s.calls = append(s.calls, "approve:"+runID+"/"+nodeID+"/"+comment+"/"+actor)
	return s.err
}

func (s *stubApprover) Reject(_ context.Context, _ uint64, runID, nodeID, comment, actor, rejectTo string) error {
	s.calls = append(s.calls, "reject:"+runID+"/"+nodeID+"/"+comment+"/"+actor+"/"+rejectTo)
	return s.err
}

func (s *stubApprover) ReworkCurrent(_ context.Context, _ uint64, runID, nodeID, comment, actor string) error {
	s.calls = append(s.calls, "rework:"+runID+"/"+nodeID+"/"+comment+"/"+actor)
	return s.err
}

func (s *stubApprover) Terminate(_ context.Context, _ uint64, runID, nodeID, comment, actor string) error {
	s.calls = append(s.calls, "terminate:"+runID+"/"+nodeID+"/"+comment+"/"+actor)
	return s.err
}

func TestApprovalServiceDelegatesFourOperations(t *testing.T) {
	eng := &stubApprover{}
	svc := NewApprovalService(eng, nil)
	ctx := context.Background()

	require.NoError(t, svc.Approve(ctx, 1, "r1", "n1", "同意", "u1"))
	require.NoError(t, svc.Reject(ctx, 1, "r1", "n1", "重做", "u1", "n0"))
	require.NoError(t, svc.ReworkCurrent(ctx, 1, "r1", "n1", "补充", "u1"))
	require.NoError(t, svc.Terminate(ctx, 1, "r1", "n1", "不做", "u1"))

	require.Equal(t, []string{
		"approve:r1/n1/同意/u1",
		"reject:r1/n1/重做/u1/n0",
		"rework:r1/n1/补充/u1",
		"terminate:r1/n1/不做/u1",
	}, eng.calls, "四操作原样委托引擎，参数顺序与任务书一致")
}

func TestApprovalServicePropagatesEngineError(t *testing.T) {
	boom := errors.New("该步骤已被处理")
	svc := NewApprovalService(&stubApprover{err: boom}, nil)
	err := svc.Approve(context.Background(), 1, "r1", "n1", "", "u1")
	require.ErrorIs(t, err, boom, "引擎拒绝时服务必须把错误原样带回（不能让上层以为成功）")
}

func TestApprovalServiceNilEngineFails(t *testing.T) {
	svc := NewApprovalService(nil, nil)
	err := svc.Approve(context.Background(), 1, "r1", "n1", "", "u1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "引擎")
}

// 活动流由引擎唯一出口写：审批成功后事项上恰好一条 approve，不多不少。
//
// 这条测试用真实 sqlite 库 + 真实引擎跑，因为「服务层别重复写」这个决定的
// 风险只有落库才看得见（stub 计数只能证明服务调了引擎一次）。
func TestApprovalServiceWritesSingleBusinessActivity(t *testing.T) {
	db := newApprovalTestDB(t)
	runs := repository.NewSopRunRepo(db)
	items := repository.NewSopItemRepo(db)
	extras := repository.NewSopItemExtraRepo(db)
	engineSvc := engine.NewEngine(engine.Deps{
		Runs: runs, Items: items, Extras: extras,
		Registry: executor.NewRegistry(), WorkspaceRoot: t.TempDir(), DisableRunLoop: true,
	})
	svc := NewApprovalService(engineSvc, extras)
	ctx := context.Background()

	item := &types.SopItem{TenantID: 1, ProjectID: "p1", Title: "需求", Status: types.SopItemStatusInProgress}
	require.NoError(t, items.Create(ctx, item))
	def := types.SopDefinition{Nodes: []types.SopNode{{ID: "n1", Type: types.SopNodeTypeApproval}}}
	run := &types.SopRun{
		TenantID: 1, Status: types.SopRunWaiting, WorkflowSnapshot: def,
		WorkspaceDir: t.TempDir(), ItemID: item.ID,
	}
	require.NoError(t, runs.CreateRun(ctx, run))
	require.NoError(t, runs.CreateNodeRun(ctx, &types.SopNodeRun{
		TenantID: 1, RunID: run.ID, NodeID: "n1", Status: types.SopNodeWaitingApproval,
	}))

	require.NoError(t, svc.Approve(ctx, 1, run.ID, "n1", "同意", "u1"))
	acts, err := extras.ListActivities(ctx, 1, item.ID, types.SopActivityKindBusiness)
	require.NoError(t, err)
	require.Len(t, acts, 1, "审批成功只能留一条业务活动（引擎写，服务不重复写）")
	require.Equal(t, "approve", acts[0].Action)
	require.Equal(t, "u1", acts[0].Actor)
}

// newApprovalTestDB 建 sqlite :memory: 测试库（单连接，与引擎测试同口径）。
func newApprovalTestDB(t *testing.T) *gorm.DB {
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
		&types.SopCLICredential{}, &types.SopTriggerConfig{}, &types.SopTriggerLog{},
		&types.SopDispatchReservation{},
	))
	require.NoError(t, db.Exec(
		"CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_node_runs_run_node ON sop_node_runs(run_id, node_id)",
	).Error)
	return db
}
