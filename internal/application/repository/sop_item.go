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

// 编译期断言：事项仓储实现必须满足接口契约（接口是 Task 22/12 的消费面）。
var _ interfaces.SopItemRepository = (*SopItemRepo)(nil)

// ErrSopItemIllegalTransition 事项状态非法迁移：由 UpdateStatus / BulkUpdateStatus 返回。
var ErrSopItemIllegalTransition = errors.New("sop item: illegal status transition")

// SopItemRepo 是 SOP 事项仓储的 GORM 实现。
// 所有方法都显式接收 tenantID 并写进 WHERE，避免跨租户越权。
// 事项状态的唯一写入口是 UpdateStatus / BulkUpdateStatus，Update 不改状态。
type SopItemRepo struct{ db *gorm.DB }

// NewSopItemRepo 构造 SOP 事项仓储。
func NewSopItemRepo(db *gorm.DB) *SopItemRepo { return &SopItemRepo{db: db} }

// Create 插入事项：JSON 列空值兜底由模型 BeforeCreate 完成。
func (r *SopItemRepo) Create(ctx context.Context, it *types.SopItem) error {
	return r.db.WithContext(ctx).Create(it).Error
}

// GetByID 按 (租户, id) 取事项，查不到返回 gorm.ErrRecordNotFound。
func (r *SopItemRepo) GetByID(ctx context.Context, tenantID uint64, id string) (*types.SopItem, error) {
	var it types.SopItem
	err := r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&it).Error
	return &it, err
}

// ListByProjectNo 按项目列出全部未软删事项（按创建时间升序），供统计/简报等聚合使用。
// 分页与派生字段请走 ListWithDerived。
func (r *SopItemRepo) ListByProjectNo(ctx context.Context, tenantID uint64, projectID string) ([]types.SopItem, error) {
	var list []types.SopItem
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND project_id = ?", tenantID, projectID).
		Order("created_at ASC, no ASC").
		Find(&list).Error
	return list, err
}

// sopItemEditableColumns 是 Update 允许写入的列白名单（用户可编辑的业务字段）。
//
// 其余列一律不由 Update 改动：
//   - status：唯一写入口是 UpdateStatus；
//   - project_id/no/is_big/parent_id/split_source：归属与两级拆分结构，部分 DTO 的零值
//     会把库里现值冲掉（跨项目重挂父项会绕过两级规则）；
//   - last_run_id：执行情况派生链路的键，被零值冲掉会让工单与运行失联；
//   - tenant_id/id/created_at/deleted_at/created_by：不可变列。
//
// updated_at 随每次更新刷新，见 sopItemUpdateColumns。
var sopItemEditableColumns = []string{
	"title", "description", "kind", "priority", "source", "proposer", "owner",
	"tags", "template_id", "kb_id", "knowledge_id", "outcome", "domain_data",
}

// sopItemUpdateColumns 是 Update 实际下发的 SET 列：可编辑列 + updated_at。
var sopItemUpdateColumns = append(append([]string{}, sopItemEditableColumns...), "updated_at")

// Update 只写「可编辑字段」白名单（sopItemUpdateColumns）。
// 状态列不参与写入：UpdateStatus 是唯一状态写入口，这样即使并发迁移已提交，
// 本方法也不会把旧状态写回去；库中原状态只回填到入参，保证调用方拿回的对象与库一致。
// 条件强制拼接 tenant_id（与 pmUpdateByID 同构），未命中返回 gorm.ErrRecordNotFound。
func (r *SopItemRepo) Update(ctx context.Context, it *types.SopItem) error {
	if it.Tags == nil {
		it.Tags = []string{}
	}
	if it.Outcome == nil {
		it.Outcome = map[string]any{}
	}
	if it.DomainData == nil {
		it.DomainData = map[string]any{}
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var cur types.SopItem
		if err := tx.Where("tenant_id = ? AND id = ?", it.TenantID, it.ID).First(&cur).Error; err != nil {
			return err
		}
		it.Status = cur.Status
		it.UpdatedAt = time.Now()
		res := tx.Model(it).
			Where("id = ? AND tenant_id = ?", it.ID, it.TenantID).
			Select(sopItemUpdateColumns).
			Updates(it)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

// Delete 软删事项（只按租户 + id 命中），未命中返回 gorm.ErrRecordNotFound。
func (r *SopItemRepo) Delete(ctx context.Context, tenantID uint64, id string) error {
	res := r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&types.SopItem{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// UpdateStatus 事项状态的唯一写入口：事务内读当前状态、过 CanTransitSopItem，
// 非法迁移不落库并返回 ErrSopItemIllegalTransition。
func (r *SopItemRepo) UpdateStatus(ctx context.Context, tenantID uint64, id, to string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var it types.SopItem
		if err := tx.Where("tenant_id = ? AND id = ?", tenantID, id).First(&it).Error; err != nil {
			return err
		}
		return applySopItemStatus(tx, &it, to)
	})
}

// BulkUpdateStatus 批量迁移状态，返回实际更新与跳过的 id（顺序同入参）。
// 跳过原因：事项不存在 / 跨租户 / 非法迁移 / skipRunning 标记为运行中。
// 每个 id 独立判定，一条失败不影响其余（批量入口不整体回滚）。
func (r *SopItemRepo) BulkUpdateStatus(
	ctx context.Context, tenantID uint64, ids []string, to string, skipRunning map[string]bool,
) (updated, skipped []string) {
	updated = make([]string, 0, len(ids))
	skipped = make([]string, 0, len(ids))
	for _, id := range ids {
		if skipRunning[id] {
			skipped = append(skipped, id)
			continue
		}
		if err := r.UpdateStatus(ctx, tenantID, id, to); err != nil {
			skipped = append(skipped, id)
			continue
		}
		updated = append(updated, id)
	}
	return updated, skipped
}

// applySopItemStatus 在事务内落库一次状态迁移：同状态为幂等 no-op（仍刷新 updated_at）。
// 显式拼 tenant_id 条件：本 helper 只写 status/updated_at 两列，但租户谓词仍不可省。
func applySopItemStatus(tx *gorm.DB, it *types.SopItem, to string) error {
	if !types.CanTransitSopItem(it.Status, to) {
		return fmt.Errorf("%w: %s -> %s", ErrSopItemIllegalTransition, it.Status, to)
	}
	now := time.Now()
	if err := tx.Model(&types.SopItem{}).
		Where("tenant_id = ? AND id = ?", it.TenantID, it.ID).
		UpdateColumns(map[string]any{"status": to, "updated_at": now}).Error; err != nil {
		return err
	}
	it.Status = to
	it.UpdatedAt = now
	return nil
}

// ListWithDerived 列表查询：基础过滤在 SQL 内完成，exec（执行情况五态）过滤在
// 组装派生字段之后进行，分页最后生效。
//
// runStatusByItem / nodeRunsByItem 由 service 层按「每个事项最近一次运行」聚合后注入
// （本 Task 尚无 sop_runs 表，故不做 JOIN）。nodeRunsByItem 的每个元素形如
// "<节点状态>|<节点名>"：状态用于 DeriveProgress，节点名用于 DeriveCurrentNode。
//
// TODO(T22/T5): sop_runs/sop_node_runs 就绪后改为由仓储内 LEFT JOIN + 子查询聚合，
// 并让 exec 过滤下推到 SQL，避免一次性把整项目事项拉进内存。
func (r *SopItemRepo) ListWithDerived(
	ctx context.Context, tenantID uint64, projectID string, f types.SopItemFilter,
	runStatusByItem map[string]string, nodeRunsByItem map[string][]string,
) ([]types.SopItemWithDerived, error) {
	q := r.db.WithContext(ctx).Where("tenant_id = ? AND project_id = ?", tenantID, projectID)
	if f.Q != "" {
		// LOWER + ESCAPE：PostgreSQL 与 SQLite 同为 LIKE，且用户输入的 % / _ 按字面量匹配。
		like := "%" + strings.ToLower(escapeLike(f.Q)) + "%"
		q = q.Where(
			"(LOWER(title) LIKE ? ESCAPE ? OR LOWER(no) LIKE ? ESCAPE ? OR LOWER(description) LIKE ? ESCAPE ?)",
			like, likeEscapeChar, like, likeEscapeChar, like, likeEscapeChar,
		)
	}
	// 精确过滤按固定顺序拼接，保证同一组条件总是生成同一条 SQL（便于计划缓存与排查）。
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.Owner != "" {
		q = q.Where("owner = ?", f.Owner)
	}
	if f.Source != "" {
		q = q.Where("source = ?", f.Source)
	}
	if f.Kind != "" {
		q = q.Where("kind = ?", f.Kind)
	}
	if f.ParentID != nil {
		q = q.Where("parent_id = ?", *f.ParentID)
	}

	var items []types.SopItem
	if err := q.Order("created_at DESC, no DESC").Find(&items).Error; err != nil {
		return nil, err
	}

	out := make([]types.SopItemWithDerived, 0, len(items))
	for _, it := range items {
		nodeRefs := nodeRunsByItem[it.ID]
		statuses := make([]string, 0, len(nodeRefs))
		var running, waiting, failed []string
		for _, ref := range nodeRefs {
			status, name := types.ParseSopNodeRef(ref)
			statuses = append(statuses, status)
			// 节点状态用 Task 5 的 sop_node_runs 常量比较，避免字面量漂移。
			switch status {
			case types.SopNodeRunning:
				running = append(running, name)
			case types.SopNodeWaitingApproval, types.SopNodePendingInput:
				waiting = append(waiting, name)
			case types.SopNodeFailed:
				failed = append(failed, name)
			}
		}
		runStatus, hasRun := runStatusByItem[it.ID]
		exec := types.DeriveExecState(it.Status, runStatus, hasRun)
		if f.Exec != "" && exec != f.Exec {
			continue
		}
		done, total := types.DeriveProgress(statuses)
		out = append(out, types.SopItemWithDerived{
			SopItem:       it,
			CurrentNode:   types.DeriveCurrentNode(it.Status, running, waiting, failed),
			ProgressDone:  done,
			ProgressTotal: total,
			ExecState:     exec,
		})
	}
	return paginateSopItems(out, f.Page, f.PageSize), nil
}

// paginateSopItems 按 1 起始的页码与页大小切片；PageSize<=0 表示不分页。
func paginateSopItems(list []types.SopItemWithDerived, page, pageSize int) []types.SopItemWithDerived {
	if pageSize <= 0 {
		return list
	}
	if page <= 0 {
		page = 1
	}
	start := (page - 1) * pageSize
	if start >= len(list) {
		return []types.SopItemWithDerived{}
	}
	end := start + pageSize
	if end > len(list) {
		end = len(list)
	}
	return list[start:end]
}

// AddAttachment 写入附件（文件字节 + 抽取文本）。
func (r *SopItemRepo) AddAttachment(ctx context.Context, a *types.SopItemAttachment) error {
	return r.db.WithContext(ctx).Create(a).Error
}

// ListAttachments 列附件：显式不取 data 列，避免把 BYTEA/BLOB 拉进内存。
func (r *SopItemRepo) ListAttachments(
	ctx context.Context, tenantID uint64, itemID string,
) ([]types.SopItemAttachment, error) {
	var list []types.SopItemAttachment
	err := r.db.WithContext(ctx).
		Select("id", "tenant_id", "item_id", "name", "media_type", "size",
			"text_content", "created_at", "deleted_at").
		Where("tenant_id = ? AND item_id = ?", tenantID, itemID).
		Order("created_at ASC").
		Find(&list).Error
	return list, err
}
