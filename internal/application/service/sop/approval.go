// approval.go 是审批中心的服务门面：把「通过 / 打回 / 补充信息重跑 / 终止」四件
// 人工操作、以及待办聚合（T20）收在一个入口后面，HTTP 层不需要知道引擎与仓储。
//
// 为什么要这一层：
//   - HTTP 层只需要「谁审了哪条运行的哪一步」，不必感知引擎与仓储的分布；
//   - 引擎的四个方法各自已经写了留痕文件、业务活动流、运行状态重算与推进唤醒，
//     服务层**不再重复写**活动（重复写会让事项时间线上出现两条相同记录）；
//   - 待办聚合（T20）与将来的批量审批都只动这一层。
//
// 注意：本层不做权限校验——租户隔离与 RBAC 在 HTTP 层（T26）与仓储层各自收口，
// 在这里再判一遍只会让「同一规则三处实现」更难对齐。
package sop

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/application/service/sop/engine"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// RunApprover 是审批服务依赖的引擎能力（真实实现 = engine.Engine）。
//
// 只声明这四个方法：服务不需要引擎的推进、调度、答复能力，声明宽了会让测试
// 被迫实现一堆它不关心的行为。
type RunApprover interface {
	Approve(ctx context.Context, tenantID uint64, runID, nodeID, comment, approver string) error
	Reject(ctx context.Context, tenantID uint64, runID, nodeID, comment, approver, rejectTo string) error
	ReworkCurrent(ctx context.Context, tenantID uint64, runID, nodeID, comment, approver string) error
	Terminate(ctx context.Context, tenantID uint64, runID, nodeID, comment, approver string) error
}

// 编译期断言：真实引擎必须满足本服务的方法集（签名漂移在这里就断，而不是接线时才炸）。
var _ RunApprover = (*engine.Engine)(nil)

// ApprovalService 审批中心的服务门面。
type ApprovalService struct {
	engine RunApprover
	// extras 供 T20 待办聚合使用；允许为 nil（本阶段四个操作都不读它）。
	extras interfaces.SopItemExtraRepository
	// aggregates 是待办聚合的三路只读来源（ListPending 用；未装配时返回空表）。
	aggregates AggregateDeps
}

// NewApprovalService 构造审批服务；engine 必须非空（nil 会在调用时报白话错误）。
func NewApprovalService(engine RunApprover, extras interfaces.SopItemExtraRepository) *ApprovalService {
	return &ApprovalService{engine: engine, extras: extras}
}

// Approve 通过一个等待审批的节点。
func (s *ApprovalService) Approve(
	ctx context.Context, tenantID uint64, runID, nodeID, comment, userID string,
) error {
	if err := s.ready(); err != nil {
		return err
	}
	return s.engine.Approve(ctx, tenantID, runID, nodeID, comment, userID)
}

// Reject 打回：rejectTo 为空时按节点模板的 RejectTo 处理（引擎负责校验目标）。
func (s *ApprovalService) Reject(
	ctx context.Context, tenantID uint64, runID, nodeID, comment, userID, rejectTo string,
) error {
	if err := s.ready(); err != nil {
		return err
	}
	return s.engine.Reject(ctx, tenantID, runID, nodeID, comment, userID, rejectTo)
}

// ReworkCurrent 让当前步骤携补充信息重跑。
func (s *ApprovalService) ReworkCurrent(
	ctx context.Context, tenantID uint64, runID, nodeID, comment, userID string,
) error {
	if err := s.ready(); err != nil {
		return err
	}
	return s.engine.ReworkCurrent(ctx, tenantID, runID, nodeID, comment, userID)
}

// Terminate 人工终止：节点失败、运行收口。
func (s *ApprovalService) Terminate(
	ctx context.Context, tenantID uint64, runID, nodeID, comment, userID string,
) error {
	if err := s.ready(); err != nil {
		return err
	}
	return s.engine.Terminate(ctx, tenantID, runID, nodeID, comment, userID)
}

// ready 检查依赖是否装配：未接线的服务调用会 NPE，报白话比 panic 好排查。
func (s *ApprovalService) ready() error {
	if s == nil || s.engine == nil {
		return errors.New("审批服务未就绪：引擎未注入")
	}
	return nil
}
