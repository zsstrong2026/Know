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

// SopItemExtraRepository 事项澄清与活动日志仓储（实现：repository.SopItemExtraRepo）。
//
// 只增不改不删：本接口刻意不提供 Update / Delete，活动一旦写入不可变更；
// 澄清唯一的状态变化是 AnswerClarification 的 open → answered 单向迁移。
// 写入方法在事务内校验事项属于该租户，跨租户或不存在时返回 gorm.ErrRecordNotFound。
type SopItemExtraRepository interface {
	AddClarification(ctx context.Context, c *types.SopItemClarification) error
	// ListClarifications 列事项澄清，created_at 升序。
	ListClarifications(ctx context.Context, tenantID uint64, itemID string) ([]types.SopItemClarification, error)
	// AnswerClarification 仅当 status=open 时写答案并转 answered；
	// 已回答再答返回错误（ErrSopClarificationNotOpen，定义在 repository 包）。
	AnswerClarification(ctx context.Context, tenantID uint64, id, answer string) error
	// SeedAnswered 批量写入已答澄清（新建事项 AI 拆解问答 seeding）。
	SeedAnswered(ctx context.Context, tenantID uint64, itemID string, qas []types.SopItemClarification) error
	AddActivity(ctx context.Context, a *types.SopItemActivity) error
	// ListActivities 列事项活动，created_at 升序；kind 为空表示不过滤。
	ListActivities(ctx context.Context, tenantID uint64, itemID, kind string) ([]types.SopItemActivity, error)
}

// SopTemplateRepository 流程模板与发布 revision 仓储（实现：repository.SopTemplateRepo）。
//
// 不可变发布：Publish 把当前草稿冻成一条新 revision 并回写
// published_revision_id/status=published/version；已发布的 revision 永不修改，
// 之后改草稿只影响草稿本身。运行一律引用 revision，不读活模板。
//
// 内置模板（is_builtin）只读：Update / Delete 返回只读错误（定义在 repository 包），
// 修改路径是 Copy 出独立的自建草稿。内置行可按 is_builtin 跨租户只读可见（内置 seed）。
type SopTemplateRepository interface {
	Create(ctx context.Context, tpl *types.SopTemplate) error
	// GetByID 按 (租户, id) 取模板；内置模板跨租户只读可见。
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.SopTemplate, error)
	// ListByTenant 返回本租户全局（project_id=''）+ 指定项目 + 内置模板。
	ListByTenant(ctx context.Context, tenantID uint64, projectID string) ([]types.SopTemplate, error)
	// Update 只写可编辑列（name/description/definition/metric_category/updated_at）；
	// 发布态归属列由 Publish 独占。内置模板返回只读错误。
	Update(ctx context.Context, tpl *types.SopTemplate) error
	Delete(ctx context.Context, tenantID uint64, id string) error
	// Publish 冻结当前草稿为 revision=max+1 的不可变快照并回写模板发布态。
	Publish(ctx context.Context, tenantID uint64, id, publishedBy string) (*types.SopTemplateRevision, error)
	GetRevision(ctx context.Context, tenantID uint64, id string) (*types.SopTemplateRevision, error)
	// ListRevisions 列某模板全部 revision，revision 降序。
	ListRevisions(ctx context.Context, tenantID uint64, templateID string) ([]types.SopTemplateRevision, error)
	// Copy 深拷贝定义生成独立自建草稿（新 id、is_builtin=false、status=draft、version=1）。
	Copy(ctx context.Context, tenantID uint64, srcID, name string) (*types.SopTemplate, error)
}
