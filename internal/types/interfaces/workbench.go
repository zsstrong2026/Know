package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// 工作台仓储接口（P1-2 范围：条目 / 版本 / 文件夹）。
//
// 约定（三条都必须遵守，否则 owner 隔离会漏）：
//  1. 所有方法的 tenantID 与 ownerID 都由调用方显式传入，仓储不自己“猜”owner；
//  2. 越权与不存在统一返回 gorm.ErrRecordNotFound，由服务层转 2400，
//     不得用 403 暴露资源存在性；
//  3. 内容写入走 UpdateItemContent 的乐观锁，元数据更新走 UpdateItem 的白名单，
//     两者不得互相影响（重命名不应和内容保存触发 409）。

// WorkbenchItemRepository 条目仓储。
type WorkbenchItemRepository interface {
	// CreateItem 插入条目；ID/时间戳由服务层填好，仓储只落库。
	CreateItem(ctx context.Context, tenantID uint64, ownerID string, item *types.WorkbenchItem) error

	// GetItem 返回含 ydoc/text_content 的完整条目；自动排除回收站。
	GetItem(ctx context.Context, tenantID uint64, ownerID, id string) (*types.WorkbenchItem, error)

	// GetItemMeta 只取元数据（不加载 ydoc），供读接口与存在性校验使用。
	GetItemMeta(ctx context.Context, tenantID uint64, ownerID, id string) (*types.WorkbenchItem, error)

	// GetTrashedItem 只查回收站内的条目（deleted_at IS NOT NULL）。
	// 仅 RestoreItem 与 purge 前置校验使用；普通读路径一律走 GetItem。
	GetTrashedItem(ctx context.Context, tenantID uint64, ownerID, id string) (*types.WorkbenchItem, error)

	// UpdateItem 只写 fields 里出现的列（白名单见实现），moveToRoot=true 时额外把
	// folder_id 置 NULL 并忽略 fields["folder_id"]。
	// 回收站中的条目不可更新：RowsAffected==0 返回 gorm.ErrRecordNotFound。
	UpdateItem(
		ctx context.Context, tenantID uint64, ownerID, id string,
		fields map[string]any, moveToRoot bool,
	) (*types.WorkbenchItem, error)

	// ListItems 按 filter 分页；返回 (items, hasMore, error)。
	// 只 SELECT 元数据列，不加载 ydoc。filter.Limit 由服务层夹到合法区间。
	ListItems(
		ctx context.Context, tenantID uint64, ownerID string, filter types.WorkbenchItemFilter,
	) ([]*types.WorkbenchItem, bool, error)

	// ListByJournalMonth 返回 journal_date 落在 [start, end] 闭区间内的画布条目，
	// 按 journal_date ASC, created_at ASC 排序，不分页。
	// start/end 是 YYYY-MM-DD 字符串，直接进 SQL 比较，不转 time.Time。
	ListByJournalMonth(
		ctx context.Context, tenantID uint64, ownerID, start, end string,
	) ([]*types.WorkbenchItem, error)

	// TrashItem 写 deleted_at（进回收站）。已在回收站时不改写 deleted_at。
	TrashItem(ctx context.Context, tenantID uint64, ownerID, id string) error

	// PurgeItem 硬删条目及其 versions/links/assets 行；存储文件由服务层在事务后删。
	PurgeItem(ctx context.Context, tenantID uint64, ownerID, id string) error

	// RestoreItem 清空 deleted_at 并返回恢复后的条目。
	RestoreItem(ctx context.Context, tenantID uint64, ownerID, id string) (*types.WorkbenchItem, error)

	// TouchLastOpened 更新 last_opened_at；失败不影响主流程（调用方可忽略错误）。
	TouchLastOpened(ctx context.Context, tenantID uint64, ownerID, id string) error

	// UpdateItemContent 乐观锁写入内容：
	//   - force=false 时要求 snapshot_version = baseVersion，冲突返回
	//     types.ErrWorkbenchVersionConflict（带回当前版本与更新时间）；
	//   - force=true 时跳过版本校验，直接 snapshot_version + 1；
	//   - 行不存在（或不属于该 tenant/owner）返回 gorm.ErrRecordNotFound。
	// 返回写入后的 (snapshot_version, updated_at)。
	UpdateItemContent(
		ctx context.Context, tenantID uint64, ownerID, id string,
		baseVersion int, force bool, ydoc []byte, text string, schemaVersion int,
	) (int, time.Time, error)

	// CountItems 统计未进回收站的条目数（Bootstrap 用）。
	CountItems(ctx context.Context, tenantID uint64, ownerID string) (int64, error)

	// PurgeTrashedBefore 硬删 deleted_at 早于 cutoff 的条目（CleanupRunner 用）。
	PurgeTrashedBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error)
}

// WorkbenchVersionRepository 条目历史版本仓储。
type WorkbenchVersionRepository interface {
	// InsertVersion 插入版本行；同一 (item_id, version) 重复时静默忽略
	// （两个标签页同时触发 checkpoint 时只留一条）。
	InsertVersion(ctx context.Context, v *types.WorkbenchItemVersion) error

	// ListVersions 返回版本摘要（不含 ydoc），按 version DESC 排序。
	ListVersions(
		ctx context.Context, tenantID uint64, ownerID, itemID string, limit int,
	) ([]*types.WorkbenchVersionSummary, error)

	// GetVersion 读取单个历史版本（含 ydoc），用于恢复。
	GetVersion(
		ctx context.Context, tenantID uint64, ownerID, itemID string, version int,
	) (*types.WorkbenchItemVersion, error)

	// RestoreVersion 在一个事务里读历史快照并按 baseVersion 乐观锁覆盖当前内容。
	// 返回 (新 snapshot_version, 新 editor_schema_version, updated_at, error)；
	// 版本不存在返回 gorm.ErrRecordNotFound，乐观锁失败返回
	// types.ErrWorkbenchVersionConflict。
	RestoreVersion(
		ctx context.Context, tenantID uint64, ownerID, itemID string, version, baseVersion int,
	) (int, int, time.Time, error)

	// PruneVersions 只保留最近 keep 个版本，返回删除行数。
	PruneVersions(
		ctx context.Context, tenantID uint64, ownerID, itemID string, keep int,
	) (int64, error)

	// PruneExpiredVersions 跨用户执行版本保留：删除 created_at 早于 before
	// 且不在每个条目最近 keep 个版本内的行（清理任务用）。
	// limit<=0 表示不限量；返回删除行数。
	PruneExpiredVersions(ctx context.Context, before time.Time, keep, limit int) (int64, error)
}

// WorkbenchFolderRepository 文件夹仓储。
type WorkbenchFolderRepository interface {
	CreateFolder(ctx context.Context, tenantID uint64, ownerID string, folder *types.WorkbenchFolder) error
	GetFolder(ctx context.Context, tenantID uint64, ownerID, id string) (*types.WorkbenchFolder, error)
	UpdateFolder(
		ctx context.Context, tenantID uint64, ownerID, id string, fields map[string]any,
	) (*types.WorkbenchFolder, error)
	// DeleteFolder 软删文件夹；调用方负责先把子文件夹与条目上移。
	DeleteFolder(ctx context.Context, tenantID uint64, ownerID, id string) error
	ListFolders(ctx context.Context, tenantID uint64, ownerID string) ([]*types.WorkbenchFolder, error)
	// ReparentChildren 把 folderID 的直接子文件夹挂到 newParentID（nil 表示根）。
	ReparentChildren(
		ctx context.Context, tenantID uint64, ownerID, folderID string, newParentID *string,
	) error
	// MoveItemsToFolder 把 folderID 下的条目挂到 newParentID（nil 表示根）。
	MoveItemsToFolder(
		ctx context.Context, tenantID uint64, ownerID, folderID string, newParentID *string,
	) error
	// ReorderFolders 按给定顺序重写同一父级下文件夹的 sort_order（从 0 连续）。
	// parentID 为 nil 表示根层级；不在该父级下的 id 会被忽略。
	ReorderFolders(
		ctx context.Context, tenantID uint64, ownerID string, parentID *string, orderedIDs []string,
	) error
	CountFolders(ctx context.Context, tenantID uint64, ownerID string) (int64, error)
}

// WorkbenchRepository 是服务层依赖的组合接口。
type WorkbenchRepository interface {
	WorkbenchItemRepository
	WorkbenchVersionRepository
	WorkbenchFolderRepository
	WorkbenchArchiveRepository
	WorkbenchNoticeRepository
	WorkbenchTemplateRepository
	WorkbenchOrphanRepository
	WorkbenchAssetRepository
	WorkbenchTagRepository
	WorkbenchLinkRepository
}

// WorkbenchTagRepository 个人标签仓储（P4-2）。
//
// 全部方法按 tenant_id + owner_id 过滤，与条目一致：越权即"不存在"。
type WorkbenchTagRepository interface {
	CreateTag(ctx context.Context, tenantID uint64, ownerID string, tag *types.WorkbenchTag) error
	// ListTags 按名称排序返回该用户的全部标签，并带上条目数。
	ListTags(ctx context.Context, tenantID uint64, ownerID string) ([]*types.WorkbenchTag, error)
	GetTag(ctx context.Context, tenantID uint64, ownerID, id string) (*types.WorkbenchTag, error)
	// UpdateTag 只允许改 name / color（白名单在仓储层再兜一次）。
	UpdateTag(
		ctx context.Context, tenantID uint64, ownerID, id string, fields map[string]any,
	) (*types.WorkbenchTag, error)
	// DeleteTag 软删标签并清掉它的关联行；条目本身不受影响。
	DeleteTag(ctx context.Context, tenantID uint64, ownerID, id string) error
	// ListTagsByItem 返回某个条目上的标签，按名称排序。
	ListTagsByItem(ctx context.Context, tenantID uint64, ownerID, itemID string) ([]*types.WorkbenchTag, error)
	// ReplaceItemTags 覆盖式设置条目标签（事务内先删后插）。
	ReplaceItemTags(ctx context.Context, tenantID uint64, ownerID, itemID string, tagIDs []string) error
}

// WorkbenchLinkRepository 反向链接仓储（P4-2「页内提及」）。
//
// workbench_item_links 从 000136 就存在，这里才是它的第一个写入方。
// 全部方法按 tenant_id + owner_id 过滤，越权即空。
type WorkbenchLinkRepository interface {
	// ListItemIDsMentioning 在用户自己的条目里找「标题出现在 text 里」的条目。
	// 单字标题不参与（误报太多）；上限由调用方控制。
	ListItemIDsMentioning(
		ctx context.Context, tenantID uint64, ownerID, sourceItemID, text string, limit int,
	) ([]string, error)
	// ReplaceItemLinks 覆盖式重写 source 条目的 item 引用（事务内先删后插）。
	ReplaceItemLinks(
		ctx context.Context, tenantID uint64, ownerID, sourceItemID string, targets []types.WorkbenchLinkTarget,
	) error
	// ListBacklinks 谁引用了 targetItem：返回引用方条目摘要，按更新时间倒序。
	ListBacklinks(
		ctx context.Context, tenantID uint64, ownerID, targetItemID string, limit int,
	) ([]*types.WorkbenchBacklink, error)
}

// WorkbenchAssetRepository 条目资产元数据仓储（§12.1）。
//
// 只存元数据与存储路径，文件本体由 AssetStore 负责。三条约定：
//   - 所有查询都带 tenant_id + owner_id，越权与不存在同样返回 (nil, nil) 或 0 行；
//   - 软删只写 deleted_at，物理文件由服务层在明确时机删除；
//   - 同一 item 下同 content_hash 的 live 行唯一（uq_wia_item_hash），
//     重复上传时靠它去重，不重复落盘。
type WorkbenchAssetRepository interface {
	// CreateAsset 插入资产行；content_hash 冲突时返回唯一索引错误，
	// 调用方应改用 GetLiveAssetByHash 读回既有行。
	CreateAsset(ctx context.Context, asset *types.WorkbenchAsset) error

	// GetAsset 按 id 取 live 资产；越权与不存在都返回 (nil, nil)。
	GetAsset(ctx context.Context, tenantID uint64, ownerID, id string) (*types.WorkbenchAsset, error)

	// GetLiveAssetByHash 按内容哈希查同 item 下的 live 资产，用于上传去重。
	GetLiveAssetByHash(
		ctx context.Context, tenantID uint64, ownerID, itemID, contentHash string,
	) (*types.WorkbenchAsset, error)

	// ListAssets 按 created_at 升序返回 item 下的 live 资产。
	ListAssets(
		ctx context.Context, tenantID uint64, ownerID, itemID string,
	) ([]*types.WorkbenchAsset, error)

	// CountAssets 统计 live 资产数（配额检查用）。
	CountAssets(ctx context.Context, tenantID uint64, ownerID, itemID string) (int64, error)

	// TouchAsset 更新 last_seen_at，返回影响行数。
	TouchAsset(
		ctx context.Context, tenantID uint64, ownerID, id string, seenAt time.Time,
	) (int64, error)

	// SoftDeleteAsset 软删资产行，返回影响行数。
	SoftDeleteAsset(ctx context.Context, tenantID uint64, ownerID, id string) (int64, error)

	// ListStaleAssets 跨用户取 COALESCE(last_seen_at, created_at) < cutoff 的 live 资产，
	// 供清理任务回收长期未被引用的文件；limit<=0 表示不限量。
	ListStaleAssets(ctx context.Context, cutoff time.Time, limit int) ([]*types.WorkbenchAsset, error)

	// ListAssetPathsByItem 返回某 item 全部资产（含已软删）的 storage_path。
	// 硬删条目时必须先取：PurgeItem 会连带删掉 asset 行，之后再也查不到路径。
	ListAssetPathsByItem(
		ctx context.Context, tenantID uint64, ownerID, itemID string,
	) ([]string, error)
}

// WorkbenchOrphanRepository 成员离开后的 30 天保留标记（§5.5）。
//
// 与其余仓储不同，这里的方法**不带 owner 过滤**：清理任务需要跨用户扫描
// 到期与即将到期的行，owner 由 orphan 行自身给出。
type WorkbenchOrphanRepository interface {
	// UpsertOrphan 写入或重置一条保留标记；重新被移除时重置 30 天。
	UpsertOrphan(ctx context.Context, orphan *types.WorkbenchOrphan) error

	// GetOrphan 返回该 (tenant, user) 的保留标记；没有则 (nil, nil)。
	GetOrphan(ctx context.Context, tenantID uint64, userID string) (*types.WorkbenchOrphan, error)

	// DeleteOrphan 删除保留标记（重新加入时），返回影响行数。
	DeleteOrphan(ctx context.Context, tenantID uint64, userID string) (int64, error)

	// ListOrphansDue 返回 purge_after <= at 的标记，按 purge_after 升序。
	ListOrphansDue(ctx context.Context, at time.Time, limit int) ([]*types.WorkbenchOrphan, error)

	// ListOrphansExpiring 返回 from < purge_after <= to 的标记（7 天预警窗口）。
	ListOrphansExpiring(
		ctx context.Context, from, to time.Time, limit int,
	) ([]*types.WorkbenchOrphan, error)

	// PurgeOwnerData 硬删该 owner 在工作台的全部数据行（条目/版本/文件夹/
	// 引用/资产/归档/模板），不含 orphan 标记本身。
	PurgeOwnerData(ctx context.Context, tenantID uint64, ownerID string) error
}

// WorkbenchTemplateRepository 文档模板仓储（§9.16）。
//
// 与条目仓储一致按 tenant_id + owner_id 隔离；模板硬删，不进回收站，
// 因此没有软删过滤，也没有 Trash/Restore。列表最多 50 条，不分页。
type WorkbenchTemplateRepository interface {
	// ListTemplates 按 updated_at 倒序返回元数据（不加载 ydoc）。
	ListTemplates(
		ctx context.Context, tenantID uint64, ownerID string,
	) ([]*types.WorkbenchTemplateSummary, error)

	// CountTemplates 配额检查用。
	CountTemplates(ctx context.Context, tenantID uint64, ownerID string) (int64, error)

	// CreateTemplate 插入一行；ydoc 字节由服务层从源条目复制后传入。
	CreateTemplate(ctx context.Context, tpl *types.WorkbenchTemplate) error

	// GetTemplate 取元数据（不加载 ydoc）；不存在或不属于该 owner 返回 nil。
	GetTemplate(
		ctx context.Context, tenantID uint64, ownerID, id string,
	) (*types.WorkbenchTemplate, error)

	// GetTemplateWithContent 取含 ydoc 的整行；仅用于套用模板。
	GetTemplateWithContent(
		ctx context.Context, tenantID uint64, ownerID, id string,
	) (*types.WorkbenchTemplate, error)

	// UpdateTemplate 只更新名称/描述。
	UpdateTemplate(
		ctx context.Context, tenantID uint64, ownerID, id string, fields map[string]any,
	) (*types.WorkbenchTemplate, error)

	// UpdateTemplateContent 只替换模板正文快照（ydoc / size_bytes / schema / updated_at）；
	// 名称与描述不动，已用该模板创建的文档不受影响。
	UpdateTemplateContent(
		ctx context.Context, tenantID uint64, ownerID, id string,
		ydoc []byte, sizeBytes, schemaVersion int,
	) (*types.WorkbenchTemplate, error)

	// DeleteTemplate 硬删；已用该模板创建的文档不受影响。
	DeleteTemplate(ctx context.Context, tenantID uint64, ownerID, id string) (int64, error)

	// DeleteByOwner 供租户/成员清理使用：硬删该 owner 的全部模板。
	DeleteByOwner(ctx context.Context, tenantID uint64, ownerID string) (int64, error)
}

// WorkbenchNoticeRepository 用户级通知仓储（§5.7）。
//
// 与其余工作台仓储的关键差异：这些方法**不带 tenant_id 条件**。
// 用户被移出最后一个 workspace 后仍要能读到“数据将保留 30 天”的提醒，
// 而那时请求上下文里已经没有租户。隔离靠 user_id，不靠 tenant_id。
type WorkbenchNoticeRepository interface {
	// InsertNotice 插入通知；dedupe_key 命中 uq_wn_dedupe 时静默忽略，
	// 保证同一事件只提醒一次（返回值表示是否真的插入）。
	InsertNotice(ctx context.Context, notice *types.WorkbenchNotice) (bool, error)

	// ListNotices 按 user 取通知，created_at 倒序；unreadOnly 只看 read_at IS NULL。
	ListNotices(
		ctx context.Context, userID string, unreadOnly bool, limit int,
	) ([]*types.WorkbenchNotice, error)

	// CountUnread 未读数；bootstrap 与通知列表共用。
	CountUnread(ctx context.Context, userID string) (int, error)

	// MarkRead 标记单条已读，返回影响行数；0 行表示不存在或不属于该用户。
	MarkRead(ctx context.Context, userID, noticeID string) (int64, error)

	// MarkAllRead 标记该用户全部未读，返回影响行数。
	MarkAllRead(ctx context.Context, userID string) (int64, error)
}

// WorkbenchArchiveRepository 归档映射仓储。
//
// 「每个 source part 只保留一条 live 行」由部分唯一索引 uq_wa_live_part 保证
// （status <> 'deleted'），因此 CreatePending 必须容忍冲突：
// 冲突意味着另一个标签页正在归档同一 part，返回既有行而不是报错。
type WorkbenchArchiveRepository interface {
	// GetLiveByPart 返回 status <> 'deleted' 的那一行；没有则 (nil, nil)。
	GetLiveByPart(
		ctx context.Context, tenantID uint64, ownerID, sourceType, sourceID string, partNo int,
	) (*types.WorkbenchArchive, error)

	// CreatePending 插入一条 pending 映射。唯一索引冲突时重新查 live 行，
	// 并返回 types.ErrArchiveInProgress。
	CreatePending(ctx context.Context, row *types.WorkbenchArchive) (*types.WorkbenchArchive, error)

	// ActivatePart 把映射置为 active 并写入版本与标题。
	ActivatePart(ctx context.Context, id, knowledgeID string, sourceVersion int, title string) error

	// RebindKnowledge 把映射指向新的 knowledge id（知识被删后重建时使用）。
	RebindKnowledge(ctx context.Context, id, knowledgeID string, sourceVersion int, title string) error

	ListLiveBySource(
		ctx context.Context, tenantID uint64, ownerID, sourceType, sourceID string,
	) ([]*types.WorkbenchArchive, error)

	// ListLiveBySources 是批量版，供列表/日历一次取回整页条目的映射，避免 N+1。
	// ids 为空直接返回空 map，不发 SQL；缺失的 id 不出现在 map 里。
	ListLiveBySources(
		ctx context.Context, tenantID uint64, ownerID, sourceType string, ids []string,
	) (map[string][]*types.WorkbenchArchive, error)

	// MarkDeletedBySource 把某个 source 的全部 live 映射置 deleted，返回影响行数。
	MarkDeletedBySource(
		ctx context.Context, tenantID uint64, ownerID, sourceType, sourceID string,
	) (int64, error)

	// MarkDeletedBySourcePart 只标记单个 part。
	MarkDeletedBySourcePart(
		ctx context.Context, tenantID uint64, ownerID, sourceType, sourceID string, partNo int,
	) (int64, error)

	// MarkDeletedByKB 把某 KB 下 owner 匹配的 live 映射置 deleted（清空工作 KB 时用）。
	MarkDeletedByKB(ctx context.Context, tenantID uint64, ownerID, kbID string) (int64, error)

	// CountAnyBySource 统计该 source 的全部映射（含 deleted）。
	// state 用它区分“从未归档”（0 行 → none）与“归档已删除”（>0 行但无 live → deleted）。
	CountAnyBySource(
		ctx context.Context, tenantID uint64, ownerID, sourceType, sourceID string,
	) (int64, error)

	CountArchiveParts(ctx context.Context, tenantID uint64, ownerID string) (int64, error)

	// ListLiveArchives 跨用户取 live 映射（status IN ('active','pending')），
	// 供清理任务的归档对账使用；按 id 升序保证分批推进不重复。
	ListLiveArchives(ctx context.Context, limit int) ([]*types.WorkbenchArchive, error)

	// ActivatePending 把超时未完成的 pending 映射置为 active（对账步骤用）。
	ActivatePending(ctx context.Context, id string) (int64, error)
}
