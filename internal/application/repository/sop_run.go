package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// 编译期断言：运行仓储实现必须满足接口契约（T12–T24 的消费面）。
var _ interfaces.SopRunRepository = (*SopRunRepo)(nil)

// SopRunFilter 是 types.SopRunFilter 的包内别名：消费方（T16 触发派发等）习惯按
// repository.SopRunFilter 书写，而接口定义在 interfaces 包、不能反向 import 本包。
type SopRunFilter = types.SopRunFilter

// sopClaimAttempts 是单次 ClaimDue 在「抢输了」时的重试上限：并发调度器抢同一条
// queued run 时，落败方换个候选再试，避免一轮就空手而归。
const sopClaimAttempts = 16

// SopRunRepo 是 SOP 运行、节点运行与产物的 GORM 实现。
//
// 不变式：
//   - 每条查询都带 tenant_id（GetRun 的 tenantID==0 是引擎内部直通分支，仅供调度器/执行器
//     使用，不得由 HTTP handler 传入未解析的租户 id）；
//   - 认领是 CAS：只有真正把 status 从 queued 改成 running 的一方拿得到（PG 以
//     UPDATE…RETURNING 回传的 id 为准，SQLite 以条件更新的 RowsAffected 为准），
//     并发调度器不可能双重认领；
//   - sop_node_runs 无软删：运行历史不可变，重跑走 ResetNodes 改状态。
type SopRunRepo struct{ db *gorm.DB }

// NewSopRunRepo 构造 SOP 运行仓储。
func NewSopRunRepo(db *gorm.DB) *SopRunRepo { return &SopRunRepo{db: db} }

// CreateRun 插入运行：状态/时间戳/JSON 列默认值由模型 BeforeCreate 补齐。
func (r *SopRunRepo) CreateRun(ctx context.Context, run *types.SopRun) error {
	return r.db.WithContext(ctx).Create(run).Error
}

// GetRun 按 (租户, id) 取运行；查不到返回 gorm.ErrRecordNotFound。
//
// tenantID == 0 保留给引擎内部使用：调度器/执行器没有租户上下文，按 id 直通读取，
// 返回的行仍带真实 TenantID，因此后续写路径（UpdateRun 等）照常按租户收口。与
// ListRuns 的 tenantID==0 同为引擎内部的跨租户读入口，HTTP handler 绝不得把未解析
// 成功（仍为 0）的租户 id 转发进来，否则等于绕过租户隔离；handler 应在进仓储前
// 校验租户缺失并直接返回 400。
func (r *SopRunRepo) GetRun(ctx context.Context, tenantID uint64, id string) (*types.SopRun, error) {
	var run types.SopRun
	q := r.db.WithContext(ctx)
	if tenantID == 0 {
		q = q.Where("id = ?", id)
	} else {
		q = q.Where("tenant_id = ? AND id = ?", tenantID, id)
	}
	err := q.First(&run).Error
	return &run, err
}

// ListRuns 按过滤条件分页列运行（created_at 倒序，最新在前）。
// 过滤在 SQL 内完成；PageSize<=0 表示不分页。
//
// tenantID == 0 与 GetRun 同口径：保留给引擎内部（调度器没有租户上下文，需按状态
// 扫描全部租户的 waiting 运行做重启恢复与审批超时清扫），返回的行仍带真实
// TenantID；HTTP handler 不得转发未解析成功的租户 id。
func (r *SopRunRepo) ListRuns(
	ctx context.Context, tenantID uint64, f types.SopRunFilter, page, pageSize int,
) ([]types.SopRun, error) {
	q := r.db.WithContext(ctx)
	if tenantID != 0 {
		q = q.Where("tenant_id = ?", tenantID)
	}
	if f.ProjectID != "" {
		q = q.Where("project_id = ?", f.ProjectID)
	}
	if f.ItemID != "" {
		q = q.Where("item_id = ?", f.ItemID)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.TemplateID != "" {
		q = q.Where("template_id = ?", f.TemplateID)
	}
	if f.Q != "" {
		like := "%" + strings.ToLower(escapeLike(f.Q)) + "%"
		q = q.Where("LOWER(id) LIKE ? ESCAPE ?", like, likeEscapeChar)
	}
	if pageSize > 0 {
		if page <= 0 {
			page = 1
		}
		q = q.Offset((page - 1) * pageSize).Limit(pageSize)
	}
	var list []types.SopRun
	err := q.Order("created_at DESC, id DESC").Find(&list).Error
	return list, err
}

// sopRunUpdateColumns 是 UpdateRun 允许写入的列白名单（运行态字段）。
//
// 其余列一律不由 UpdateRun 改动：
//   - tenant_id/template_id/revision_id/workflow_snapshot/project_id/item_id/parent_run_id/
//     trigger_kind/trigger_ref_id/created_by/created_at：归属与创建态，运行期恒不可变
//     （被 DTO 零值冲掉会让运行与事项/模板失联，或跨租户改写）；
//   - deleted_at：软删不走本方法。
var sopRunUpdateColumns = []string{
	"status", "input", "human_context", "project_context", "workspace_dir",
	"lease_until", "heartbeat_at", "cancel_requested", "pause_requested",
	"usage", "execution_duration_ms", "error", "started_at", "finished_at", "updated_at",
}

// UpdateRun 只写运行态列白名单，条件强制拼接 tenant_id（未命中返回 gorm.ErrRecordNotFound）。
//
// 引擎从 GetRun(0) 拿到的行带真实 TenantID，因此内部写路径同样按租户收口；
// 跨租户拿着同一 id 的结构体无法改到别人的运行。
func (r *SopRunRepo) UpdateRun(ctx context.Context, run *types.SopRun) error {
	if run == nil {
		return gorm.ErrRecordNotFound
	}
	if run.Input == nil {
		run.Input = map[string]any{}
	}
	if run.HumanContext == nil {
		run.HumanContext = []types.SopHumanContextEntry{}
	}
	run.UpdatedAt = time.Now()
	res := r.db.WithContext(ctx).Model(&types.SopRun{}).
		Where("id = ? AND tenant_id = ?", run.ID, run.TenantID).
		Select(sopRunUpdateColumns).
		Updates(run)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// ClaimDue 以 CAS 认领一个 queued run 并转 running、写入租约。
//
// 并发正确性：只有真正把 status 从 queued 改成 running 的调用方（RowsAffected>0 /
// RETURNING 有行）拿到该 run；落败方换个候选重试，候选耗尽返回 (nil, nil)。
// PostgreSQL 用单条 UPDATE…RETURNING 完成「选最新一条 + 条件更新」，SQLite 不支持
// 同样的行锁语义，退化为「先读候选 → WHERE status='queued' 条件更新」，两条语句各自
// 自动提交（不进事务，避免共享缓存下的读锁升级死锁），更新语句本身就是 CAS。
func (r *SopRunRepo) ClaimDue(ctx context.Context, leaseUntil time.Time) (*types.SopRun, error) {
	for attempt := 0; attempt < sopClaimAttempts; attempt++ {
		claimed, retry, err := r.claimOnce(ctx, leaseUntil)
		if err != nil {
			return nil, err
		}
		if claimed != nil {
			return claimed, nil
		}
		if !retry {
			return nil, nil
		}
	}
	// 连续抢输（高并发下极端情况）：本轮空手，下一轮调度再试。
	return nil, nil
}

// claimOnce 尝试认领一次；retry 表示「候选还在但已被别人抢走」，值得再试。
func (r *SopRunRepo) claimOnce(
	ctx context.Context, leaseUntil time.Time,
) (claimed *types.SopRun, retry bool, err error) {
	db := r.db.WithContext(ctx)
	now := time.Now()
	if db.Dialector.Name() == "postgres" {
		return r.claimPostgres(ctx, leaseUntil, now)
	}

	var candidate types.SopRun
	findErr := db.Where("status = ? AND deleted_at IS NULL", types.SopRunQueued).
		Order("created_at ASC, id ASC").First(&candidate).Error
	if errors.Is(findErr, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if findErr != nil {
		return nil, false, findErr
	}
	res := db.Model(&types.SopRun{}).
		Where("id = ? AND status = ? AND deleted_at IS NULL", candidate.ID, types.SopRunQueued).
		Updates(map[string]any{
			"status":       types.SopRunRunning,
			"lease_until":  leaseUntil,
			"heartbeat_at": now,
			"started_at":   gorm.Expr("COALESCE(started_at, ?)", now),
			"updated_at":   now,
		})
	if res.Error != nil {
		return nil, false, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, true, nil
	}
	candidate.Status = types.SopRunRunning
	candidate.LeaseUntil = &leaseUntil
	candidate.HeartbeatAt = &now
	if candidate.StartedAt == nil {
		candidate.StartedAt = &now
	}
	candidate.UpdatedAt = now
	return &candidate, false, nil
}

// claimDecision 依据 PG `UPDATE … RETURNING id` 的执行结果判定认领成败。
//
// 判据只看 RETURNING 回传的 id（claimedID），不看 res.RowsAffected：GORM 对
// `UPDATE … RETURNING` + Scan 的 RowsAffected 跨驱动/版本并不可靠，可能出现
// RowsAffected==0 而 claimedID 已填的情况。若按 RowsAffected 判负，真正抢到的一行
// 会被判成「抢输了」，调用方重新数 pending 得 0 后返回 (nil, nil)，run 就卡在
// running、租约没人续，直到 ExpireLeases 才被回收为 failed。
//
// 返回 won=true 表示本调用方拿到了该行；won=false 且 err==nil 表示没拿到，
// 此时由调用方数 pending 决定是否重试（候选还在就 retry）。res.RowsAffected
// 有意不参与判定，仅为让调用点显式表明「RETURNING 行才是判据」。
func claimDecision(claimedID string, res *gorm.DB) (won bool, err error) {
	if res != nil && res.Error != nil {
		return false, res.Error
	}
	return claimedID != "", nil
}

// claimPostgres 用一条 UPDATE…RETURNING 完成认领：子查询挑最老的 queued，
// 外层 WHERE status='queued' 保证并发下只有一方拿到（另一方重评估后 0 行）。
// 成功判据见 claimDecision（RETURNING 有行，而非 RowsAffected）；返回 (nil,nil,nil)
// 由调用方区分「没有候选」与「抢输了」：判负时先探一次 pending 再决定是否重试。
func (r *SopRunRepo) claimPostgres(
	ctx context.Context, leaseUntil, now time.Time,
) (*types.SopRun, bool, error) {
	db := r.db.WithContext(ctx)
	var claimedID string
	res := db.Raw(`UPDATE sop_runs
		SET status = ?, lease_until = ?, heartbeat_at = ?, started_at = COALESCE(started_at, ?), updated_at = ?
		WHERE id = (SELECT id FROM sop_runs
			WHERE status = ? AND deleted_at IS NULL
			ORDER BY created_at ASC, id ASC LIMIT 1)
		  AND status = ? AND deleted_at IS NULL
		RETURNING id`,
		types.SopRunRunning, leaseUntil, now, now, now, types.SopRunQueued, types.SopRunQueued).
		Scan(&claimedID)
	won, err := claimDecision(claimedID, res)
	if err != nil {
		return nil, false, err
	}
	if !won {
		var pending int64
		if err := db.Model(&types.SopRun{}).
			Where("status = ? AND deleted_at IS NULL", types.SopRunQueued).Count(&pending).Error; err != nil {
			return nil, false, err
		}
		return nil, pending > 0, nil
	}
	run, err := r.GetRun(ctx, 0, claimedID)
	if err != nil {
		return nil, false, err
	}
	return run, false, nil
}

// Heartbeat 续租：刷新 lease_until 与 heartbeat_at。
// 只按 id 更新（执行中的实例已持有该 run，租约续期不改变归属）。
func (r *SopRunRepo) Heartbeat(ctx context.Context, runID string, leaseUntil time.Time) error {
	return r.db.WithContext(ctx).Model(&types.SopRun{}).
		Where("id = ? AND deleted_at IS NULL", runID).
		Updates(map[string]any{
			"lease_until":  leaseUntil,
			"heartbeat_at": time.Now(),
			"updated_at":   time.Now(),
		}).Error
}

// sopLeaseExpiredError 是回收文案：进程崩溃后租约过期，运行可重试。
const sopLeaseExpiredError = "执行租约过期（进程崩溃回收），可重试"

// ExpireLeases 回收租约过期的 running/waiting 运行。
//
// 分成两条路径，顺序不可颠倒：
//  1. cancel_requested=true 的行收口为 cancelled（先把它在途节点一并取消）——取消
//     请求已经落库、但推进协程在收口前崩溃时，不能让「取消」被租约回收写成 failed
//     （取消不是失败）。这一步放在仓储里，是因为调度器每轮只能看到条数，看不到
//     「哪些行是取消标记」；在引擎侧再扫一遍 failed 行会把整张失败表拉进内存。
//  2. 其余过期行标记 failed 并写回收原因。
//
// 返回两条路径实际回收的条数之和（调度器用于日志）。
func (r *SopRunRepo) ExpireLeases(ctx context.Context, now time.Time) (int64, error) {
	db := r.db.WithContext(ctx)
	expired := func() *gorm.DB {
		return db.Model(&types.SopRun{}).
			Where("status IN ? AND lease_until IS NOT NULL AND lease_until < ? AND deleted_at IS NULL",
				[]string{types.SopRunRunning, types.SopRunWaiting}, now)
	}
	// 1a. 先取消取消标记运行的未终态节点（子查询必须能在运行改状态之前命中这些行）。
	cancelScope := func() *gorm.DB {
		return db.Model(&types.SopRun{}).Select("id").
			Where("cancel_requested = ? AND status IN ? AND lease_until IS NOT NULL AND lease_until < ? AND deleted_at IS NULL",
				true, []string{types.SopRunRunning, types.SopRunWaiting}, now)
	}
	if err := db.Model(&types.SopNodeRun{}).
		Where("run_id IN (?)", cancelScope()).
		Where("status IN ?", []string{
			types.SopNodePending, types.SopNodeRunning,
			types.SopNodeWaitingApproval, types.SopNodePendingInput,
		}).
		Updates(map[string]any{
			"status":      types.SopNodeCancelled,
			"finished_at": now,
			"updated_at":  now,
		}).Error; err != nil {
		return 0, err
	}
	// 1b. 取消标记的运行收口为 cancelled（清标记，与引擎 finishCancelled 同口径）。
	cancelled := db.Model(&types.SopRun{}).
		Where("cancel_requested = ? AND status IN ? AND lease_until IS NOT NULL AND lease_until < ? AND deleted_at IS NULL",
			true, []string{types.SopRunRunning, types.SopRunWaiting}, now).
		Updates(map[string]any{
			"status":           types.SopRunCancelled,
			"cancel_requested": false,
			"error":            "",
			"finished_at":      now,
			"updated_at":       now,
		})
	if cancelled.Error != nil {
		return 0, cancelled.Error
	}
	// 2. 其余过期行照旧回收为 failed。
	failed := expired().
		Where("cancel_requested = ?", false).
		Updates(map[string]any{
			"status":      types.SopRunFailed,
			"error":       sopLeaseExpiredError,
			"finished_at": now,
			"updated_at":  now,
		})
	if failed.Error != nil {
		return cancelled.RowsAffected, failed.Error
	}
	return cancelled.RowsAffected + failed.RowsAffected, nil
}

// CreateNodeRun 插入节点运行记录；(run_id, node_id) 唯一，重复返回唯一约束错误。
func (r *SopRunRepo) CreateNodeRun(ctx context.Context, nr *types.SopNodeRun) error {
	return r.db.WithContext(ctx).Create(nr).Error
}

// GetNodeRun 按 (租户, run, node) 取节点运行，查不到返回 gorm.ErrRecordNotFound。
func (r *SopRunRepo) GetNodeRun(
	ctx context.Context, tenantID uint64, runID, nodeID string,
) (*types.SopNodeRun, error) {
	var nr types.SopNodeRun
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND run_id = ? AND node_id = ?", tenantID, runID, nodeID).
		First(&nr).Error
	return &nr, err
}

// ListNodeRuns 列某次运行的全部节点记录（created_at 升序，同刻按 node_id 稳定排序）。
func (r *SopRunRepo) ListNodeRuns(
	ctx context.Context, tenantID uint64, runID string,
) ([]types.SopNodeRun, error) {
	var list []types.SopNodeRun
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND run_id = ?", tenantID, runID).
		Order("created_at ASC, node_id ASC").
		Find(&list).Error
	return list, err
}

// sopNodeRunUpdateColumns 是 UpdateNodeRun 允许写入的列白名单（节点运行态字段）。
// run_id/tenant_id/id 是归属键，创建后不可变。
var sopNodeRunUpdateColumns = []string{
	"status", "attempt", "rework_count", "output", "error", "feedback",
	"gate_result", "gate_reason", "session_id", "resume_session_id", "child_run_id",
	"usage", "started_at", "finished_at", "updated_at",
}

// UpdateNodeRun 只写节点运行态列白名单，条件强制拼接 tenant_id（未命中返回
// gorm.ErrRecordNotFound）。
func (r *SopRunRepo) UpdateNodeRun(ctx context.Context, nr *types.SopNodeRun) error {
	if nr == nil {
		return gorm.ErrRecordNotFound
	}
	nr.UpdatedAt = time.Now()
	res := r.db.WithContext(ctx).Model(&types.SopNodeRun{}).
		Where("id = ? AND tenant_id = ?", nr.ID, nr.TenantID).
		Select(sopNodeRunUpdateColumns).
		Updates(nr)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// TransitionNodeRun 以 CAS 迁移节点运行状态：只有库里当前状态仍等于 from 时才把
// status 改为 to 并写入 patch，返回是否命中（rows affected == 1）。
//
// 用途：人工审批的录取是「谁先改到谁算」，两个并发审批必须恰好一个成功；用
// UpdateNodeRun（无条件按 id 写）会让后者静默覆盖前者的节点状态，并各自落一份
// 决定文件。patch 只接受节点运行态白名单列（status 由 to 决定，patch 里的 status
// 会被 to 覆盖），归属键与创建态字段一律忽略。
func (r *SopRunRepo) TransitionNodeRun(
	ctx context.Context, tenantID uint64, runID, nodeID, from, to string, patch map[string]any,
) (bool, error) {
	cols := map[string]any{}
	for k, v := range patch {
		if k == "status" || k == "updated_at" {
			continue
		}
		for _, allowed := range sopNodeRunUpdateColumns {
			if allowed == k {
				cols[k] = v
				break
			}
		}
	}
	cols["status"] = to
	cols["updated_at"] = time.Now()
	res := r.db.WithContext(ctx).Model(&types.SopNodeRun{}).
		Where("tenant_id = ? AND run_id = ? AND node_id = ? AND status = ?",
			tenantID, runID, nodeID, from).
		Updates(cols)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

// ResetNodes 把指定节点重置为 pending 并清空执行痕迹（重跑/打回共用）。
// 运行历史本身不删：只改状态与产物字段，Attempt 由调用方按需清零。
func (r *SopRunRepo) ResetNodes(ctx context.Context, runID string, nodeIDs []string) error {
	if len(nodeIDs) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Model(&types.SopNodeRun{}).
		Where("run_id = ? AND node_id IN ?", runID, nodeIDs).
		Updates(map[string]any{
			"status":       types.SopNodePending,
			"output":       "",
			"error":        "",
			"feedback":     "",
			"gate_result":  "",
			"gate_reason":  "",
			"child_run_id": "",
			"finished_at":  nil,
			"updated_at":   time.Now(),
		}).Error
}

// CreateArtifact 插入产物记录（id/attempt=1 由模型 BeforeCreate 补齐）。
func (r *SopRunRepo) CreateArtifact(ctx context.Context, a *types.SopArtifact) error {
	return r.db.WithContext(ctx).Create(a).Error
}

// ListArtifacts 列某次运行的全部产物（created_at 升序）；软删自动排除。
func (r *SopRunRepo) ListArtifacts(
	ctx context.Context, tenantID uint64, runID string,
) ([]types.SopArtifact, error) {
	var list []types.SopArtifact
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND run_id = ?", tenantID, runID).
		Order("created_at ASC, node_id ASC, attempt ASC").
		Find(&list).Error
	return list, err
}

// GetArtifact 按 (租户, id) 取产物，查不到返回 gorm.ErrRecordNotFound。
func (r *SopRunRepo) GetArtifact(
	ctx context.Context, tenantID uint64, id string,
) (*types.SopArtifact, error) {
	var a types.SopArtifact
	err := r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&a).Error
	return &a, err
}

// UpdateArtifact 保存产物的归档回填列（kb_id/knowledge_id/ingested_at）。
//
// 条件强制拼接 tenant_id；未命中返回 gorm.ErrRecordNotFound。
func (r *SopRunRepo) UpdateArtifact(ctx context.Context, art *types.SopArtifact) error {
	if art == nil {
		return gorm.ErrRecordNotFound
	}
	res := r.db.WithContext(ctx).Model(&types.SopArtifact{}).
		Where("id = ? AND tenant_id = ?", art.ID, art.TenantID).
		Select("kb_id", "knowledge_id", "ingested_at").
		Updates(art)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// DeleteArtifactsByNode 软删某运行某节点的全部产物行，返回删除条数。
//
// 子流程节点重新拉起子运行后，父运行该节点此前的产物行属于旧子运行的事实：先整体
// 作废再按新子运行的产物重建，同一路径才不会同时挂着新旧内容。软删而非物理删：
// 产物表本身是运行历史的一部分，软删让旧行对读路径（ListArtifacts 等）不可见即可。
func (r *SopRunRepo) DeleteArtifactsByNode(
	ctx context.Context, tenantID uint64, runID, nodeID string,
) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("tenant_id = ? AND run_id = ? AND node_id = ?", tenantID, runID, nodeID).
		Delete(&types.SopArtifact{})
	return res.RowsAffected, res.Error
}

// FindChildRun 查找某运行某子流程节点当前仍活跃的子运行；找不到返回 (nil, nil)。
//
// 子 run 的「CreateRun → 回写 ChildRunID」不是原子两步，崩溃会留下一条孤儿子运行；
// 拉起前先探测并复用它，避免一轮一轮重复创建。活跃态口径与 SopRunActiveStatuses 一致。
// 只按 parent_run_id（全局唯一）过滤，属于引擎内部入口，不对外暴露租户旁路。
func (r *SopRunRepo) FindChildRun(
	ctx context.Context, parentRunID, nodeID string,
) (*types.SopRun, error) {
	if parentRunID == "" || nodeID == "" {
		return nil, nil
	}
	var run types.SopRun
	err := r.db.WithContext(ctx).
		Where("parent_run_id = ? AND trigger_ref_id = ? AND status IN ?",
			parentRunID, nodeID, types.SopRunActiveStatuses).
		Order("created_at DESC, id DESC").
		First(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &run, nil
}

// ListChildRuns 列某运行派生的全部子运行（created_at 升序）；取消向下传播用。
func (r *SopRunRepo) ListChildRuns(
	ctx context.Context, parentRunID string,
) ([]types.SopRun, error) {
	if parentRunID == "" {
		return nil, nil
	}
	var list []types.SopRun
	err := r.db.WithContext(ctx).
		Where("parent_run_id = ?", parentRunID).
		Order("created_at ASC, id ASC").
		Find(&list).Error
	return list, err
}

// StatsByStatus 按状态分组计数（不含其它租户、不含软删）。
func (r *SopRunRepo) StatsByStatus(ctx context.Context, tenantID uint64) (map[string]int64, error) {
	type row struct {
		Status string
		N      int64
	}
	var rows []row
	err := r.db.WithContext(ctx).Model(&types.SopRun{}).
		Select("status, COUNT(*) AS n").
		Where("tenant_id = ?", tenantID).
		Group("status").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(rows))
	for _, x := range rows {
		out[x.Status] = x.N
	}
	return out, nil
}

// StatsSummary 运行统计摘要：各状态计数、总数、已结束数、成功率与累计 token。
//
// 成功率分母只取已结束（succeeded+failed+cancelled），running/queued/waiting 不算
// 成功也不算失败；没有已结束运行时成功率为 0（不除零）。
// projectID 为空表示全租户，非空时限定项目。
func (r *SopRunRepo) StatsSummary(
	ctx context.Context, tenantID uint64, projectID string,
) (*types.SopRunStats, error) {
	q := r.db.WithContext(ctx).Model(&types.SopRun{}).Where("tenant_id = ?", tenantID)
	if projectID != "" {
		q = q.Where("project_id = ?", projectID)
	}
	type row struct {
		Status string
		N      int64
	}
	var rows []row
	if err := q.Select("status, COUNT(*) AS n").Group("status").Scan(&rows).Error; err != nil {
		return nil, err
	}
	counts := make(map[string]int64, len(rows))
	var total int64
	for _, x := range rows {
		counts[x.Status] = x.N
		total += x.N
	}
	finished := counts[types.SopRunSucceeded] + counts[types.SopRunFailed] + counts[types.SopRunCancelled]
	stats := &types.SopRunStats{
		TotalCount:    total,
		FinishedCount: finished,
		StatusCounts:  counts,
	}
	if finished > 0 {
		stats.SuccessRate = float64(counts[types.SopRunSucceeded]) / float64(finished)
	}
	tokens, err := r.sumUsageTokens(ctx, tenantID, projectID)
	if err != nil {
		return nil, err
	}
	stats.TotalTokens = tokens
	return stats, nil
}

// sumUsageTokens 累计 usage 里的 input_tokens + output_tokens。
// usage 是 JSON 文档列，两种 dialector 的取数表达式不同，故按 dialector 分支。
func (r *SopRunRepo) sumUsageTokens(ctx context.Context, tenantID uint64, projectID string) (int64, error) {
	express := "COALESCE(SUM(json_extract(usage, '$.input_tokens')), 0) + " +
		"COALESCE(SUM(json_extract(usage, '$.output_tokens')), 0)"
	if r.db.Dialector.Name() == "postgres" {
		express = "COALESCE(SUM((usage->>'input_tokens')::bigint), 0) + " +
			"COALESCE(SUM((usage->>'output_tokens')::bigint), 0)"
	}
	q := r.db.WithContext(ctx).Model(&types.SopRun{}).Where("tenant_id = ? AND deleted_at IS NULL", tenantID)
	if projectID != "" {
		q = q.Where("project_id = ?", projectID)
	}
	var total int64
	if err := q.Select(express).Scan(&total).Error; err != nil {
		return 0, fmt.Errorf("sum sop run usage tokens: %w", err)
	}
	return total, nil
}
