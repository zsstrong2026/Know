package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// ---- SOP 仓储接口：统一约定 ----
//  1. 所有方法首参为 ctx；
//  2. 读写一律带 tenant_id 条件，防跨租户越权（GetByID 同时带 id 与 tenant_id）；
//  3. 查不到时由调用方按 gorm.ErrRecordNotFound 映射 404。

// SopProjectRepository 项目仓储（后续 SOP 任务在此文件追加事项/模板/运行等接口）。
type SopProjectRepository interface {
	Create(ctx context.Context, p *types.SopProject) error
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.SopProject, error)
	ListByTenant(ctx context.Context, tenantID uint64) ([]types.SopProject, error)
	Update(ctx context.Context, p *types.SopProject) error
	Delete(ctx context.Context, tenantID uint64, id string) error
	// AllocItemNo 事务内自增编号游标，返回本次分配编号（首次=1）。
	AllocItemNo(ctx context.Context, tenantID uint64, projectID string) (int, error)
	// AddRepo 绑定仓库，返回入库后的绑定；URL 非法或重复返回错误。
	AddRepo(
		ctx context.Context, tenantID uint64, id string, repo types.SopProjectRepo,
	) (*types.SopProjectRepo, error)
	// RemoveRepo 解绑仓库，默认仓库顺延到剩余第一个。
	RemoveRepo(ctx context.Context, tenantID uint64, id, repoID string) error
	// SetDefaultRepo 指定默认仓库，并把它排到列表首位。
	SetDefaultRepo(ctx context.Context, tenantID uint64, id, repoID string) error
	// CountActiveRuns 统计 status ∈ queued/running/waiting/paused 的运行数。
	// 实现随 sop_runs 表（Task 5）补齐。
	CountActiveRuns(ctx context.Context, tenantID uint64, projectID string) (int64, error)
}

// SopItemRepository 事项仓储（实现：repository.SopItemRepo）。
type SopItemRepository interface {
	Create(ctx context.Context, it *types.SopItem) error
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.SopItem, error)
	// ListWithDerived 列表 + 派生字段（current_node/progress/exec_state）。
	// runStatusByItem 与 nodeRunsByItem 由 service 层按「每个事项最近一次运行」聚合后注入，
	// 元素形如 "<节点状态>|<节点名>"；exec 过滤在派生字段组装后进行。
	ListWithDerived(
		ctx context.Context, tenantID uint64, projectID string, f types.SopItemFilter,
		runStatusByItem map[string]string, nodeRunsByItem map[string][]string,
	) ([]types.SopItemWithDerived, error)
	// Update 全列保存可编辑字段；状态不随此方法落库（唯一写入口是 UpdateStatus）。
	Update(ctx context.Context, it *types.SopItem) error
	Delete(ctx context.Context, tenantID uint64, id string) error
	// UpdateStatus 唯一的状态写入口：内部过 CanTransitSopItem，非法迁移不落库。
	UpdateStatus(ctx context.Context, tenantID uint64, id, to string) error
	// BulkUpdateStatus 批量迁移状态；skipRunning 命中的 id 与非法/不存在的一并进 skipped。
	BulkUpdateStatus(
		ctx context.Context, tenantID uint64, ids []string, to string, skipRunning map[string]bool,
	) (updated, skipped []string)
	AddAttachment(ctx context.Context, a *types.SopItemAttachment) error
	ListAttachments(ctx context.Context, tenantID uint64, itemID string) ([]types.SopItemAttachment, error)
	// ListByProjectNo 按项目列出全部未软删事项（created_at, no 升序）。
	ListByProjectNo(ctx context.Context, tenantID uint64, projectID string) ([]types.SopItem, error)
}
