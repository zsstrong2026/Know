package sop

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
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

// 活动流由引擎写（唯一出口）：服务再写一遍会出现两条 approve 记录，前端时间线立即重复。
func TestApprovalServiceDoesNotDoubleLogActivity(t *testing.T) {
	eng := &stubApprover{}
	svc := NewApprovalService(eng, nil)
	require.NoError(t, svc.Approve(context.Background(), 1, "r1", "n1", "同意", "u1"))
	require.Len(t, eng.calls, 1)
}

// ApprovalTimeoutHours<=0 的节点不参与清扫（spec §6 默认无限等待）。
func TestApprovalTimeoutPolicyReadsNodeFields(t *testing.T) {
	def := types.SopDefinition{Nodes: []types.SopNode{
		{ID: "n1", Type: types.SopNodeTypeApproval},
		{ID: "n2", Type: types.SopNodeTypeApproval, ApprovalTimeoutHours: 2, ApprovalTimeoutAction: "notify"},
	}}
	require.Equal(t, 0, def.Nodes[0].ApprovalTimeoutHours, "未配置=无限等待")
	require.Equal(t, 2, def.Nodes[1].ApprovalTimeoutHours)
	require.Equal(t, "notify", def.Nodes[1].ApprovalTimeoutAction)
}
