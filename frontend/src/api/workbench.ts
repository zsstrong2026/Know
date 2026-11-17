/**
 * 工作台 API 客户端。
 *
 * 约束（§9.3）：
 *   - 组件里禁止手写 URL，一律从这里导入；
 *   - owner 从不作为参数传递，由后端从 JWT 上下文取；
 *   - 统一在后端响应 `{ success, data }` 上解包，调用方直接拿业务对象；
 *   - 错误码统一用 workbenchErrorCode() 读取，不要在别处再抄一份判断。
 */
import { get, getDown, patch, post, postUpload, put, del } from '@/utils/request'
import { WORKBENCH_ERROR, workbenchConflictVersion, workbenchErrorCode } from '@/workbench/error-code'
import type {
  ArchiveSourceType,
  SaveWorkbenchContentResult,
  WorkbenchArchiveState,
  WorkbenchAsset,
  WorkbenchBootstrap,
  WorkbenchFolder,
  WorkbenchTag,
  WorkbenchBacklink,
  WorkbenchItem,
  WorkbenchItemContent,
  WorkbenchItemKind,
  WorkbenchItemPage,
  WorkbenchItemSort,
  WorkbenchJournalDay,
  WorkbenchJournalMonth,
  WorkbenchLeavePreview,
  WorkbenchNoticePage,
  WorkbenchReindexResult,
  WorkbenchTemplateContent,
  WorkbenchTemplateSummary,
  WorkbenchVersionSummary,
} from '@/types/workbench'

const BASE = '/api/v1/workbench'

// 错误码与解析函数放在 workbench/error-code.ts（无依赖，可在 node 单测里直接加载），
// 这里再导出一次，业务侧仍然只从 @/api/workbench 导入。
export { WORKBENCH_ERROR, workbenchConflictVersion, workbenchErrorCode }

export function getWorkbenchBootstrap(): Promise<WorkbenchBootstrap> {
  return get<{ success: boolean; data: WorkbenchBootstrap }>(`${BASE}/bootstrap`).then((r) => r.data)
}

/**
 * 启用语义检索：为当前用户的工作知识库绑定 embedding 模型并重建索引（§7.8）。
 *
 * 不接受 kb_id —— 服务端固定操作调用者自己的工作库，避免变成任意库重建入口。
 * model_id 留空则用默认 Embedding 模型；没有可用模型时返回 2406。
 */
export function requestSemanticIndex(modelId = ''): Promise<WorkbenchReindexResult> {
  return post<{ success: boolean; data: WorkbenchReindexResult }>(
    `${BASE}/kb/semantic-index`,
    { model_id: modelId },
  ).then((r) => r.data)
}

export interface ListWorkbenchItemsParams {
  kind?: WorkbenchItemKind
  /** 排除某类条目：「最近」用它挡掉事项（kind=database） */
  exclude_kind?: WorkbenchItemKind
  folder_id?: string
  favorite?: boolean
  trashed?: boolean
  q?: string
  /** 按标签过滤（P4-2） */
  tag_id?: string
  /** 列表排序（B5）：updated（默认）/ created / title */
  sort?: WorkbenchItemSort
  cursor?: string
  limit?: number
}

export function listWorkbenchItems(params: ListWorkbenchItemsParams = {}): Promise<WorkbenchItemPage> {
  const query = new URLSearchParams()
  if (params.kind) query.set('kind', params.kind)
  if (params.exclude_kind) query.set('exclude_kind', params.exclude_kind)
  if (params.folder_id) query.set('folder_id', params.folder_id)
  if (params.favorite) query.set('favorite', '1')
  if (params.trashed) query.set('trashed', '1')
  if (params.q) query.set('q', params.q)
  if (params.tag_id) query.set('tag_id', params.tag_id)
  if (params.sort) query.set('sort', params.sort)
  if (params.cursor) query.set('cursor', params.cursor)
  query.set('limit', String(params.limit ?? 30))
  return get<{ success: boolean; data: WorkbenchItemPage }>(`${BASE}/items?${query.toString()}`)
    .then((r) => r.data)
}

export function createWorkbenchItem(payload: {
  kind: WorkbenchItemKind
  title: string
  folder_id?: string | null
  journal_date?: string | null
  current_mode?: 'page' | 'edgeless'
}): Promise<WorkbenchItem> {
  return post<{ success: boolean; data: WorkbenchItem }>(`${BASE}/items`, payload).then((r) => r.data)
}

export function getWorkbenchItem(id: string): Promise<WorkbenchItem> {
  return get<{ success: boolean; data: WorkbenchItem }>(`${BASE}/items/${encodeURIComponent(id)}`)
    .then((r) => r.data)
}

export function updateWorkbenchItem(
  id: string,
  payload: Partial<Pick<WorkbenchItem, 'title' | 'icon' | 'folder_id' | 'is_favorite'>> & {
    move_to_root?: boolean
    /** 三态：缺省/null=不改，''=取消钉，YYYY-MM-DD=改期（§7.4） */
    journal_date?: string
    current_mode?: 'page' | 'edgeless'
  },
): Promise<WorkbenchItem> {
  return put<{ success: boolean; data: WorkbenchItem }>(
    `${BASE}/items/${encodeURIComponent(id)}`,
    payload,
  ).then((r) => r.data)
}

/**
 * 上传列表封面缩略图（编辑器保存后截取的当前视图）。
 *
 * 直接传图片字节而不是 multipart：单张图很小，服务端会嗅探真实类型；
 * version 是截取时的 snapshot_version，服务端只接受不旧于当前封面的写入。
 */
export function saveWorkbenchThumbnail(
  id: string,
  image: Blob,
  version: number,
): Promise<{ thumbnail_version: number }> {
  return put<{ success: boolean; data: { thumbnail_version: number } }>(
    `${BASE}/items/${encodeURIComponent(id)}/thumbnail?version=${version}`,
    image,
    { headers: { 'Content-Type': image.type || 'image/jpeg' } },
  ).then((r) => r.data)
}

/** 读取列表封面；没有封面时接口返回 404，由调用方回退到骨架封面。 */
export function getWorkbenchThumbnail(id: string, version: number): Promise<Blob> {
  return getDown(
    `${BASE}/items/${encodeURIComponent(id)}/thumbnail?v=${version}`,
  )
}

export function deleteWorkbenchItem(
  id: string,
  opts: { delete_archives?: boolean; purge?: boolean } = {},
): Promise<void> {
  const query = new URLSearchParams()
  if (opts.delete_archives) query.set('delete_archives', '1')
  if (opts.purge) query.set('purge', '1')
  const suffix = query.toString()
  return del<{ success: boolean }>(
    `${BASE}/items/${encodeURIComponent(id)}${suffix ? `?${suffix}` : ''}`,
  ).then(() => undefined)
}

export function restoreWorkbenchItem(id: string): Promise<WorkbenchItem> {
  return post<{ success: boolean; data: WorkbenchItem }>(
    `${BASE}/items/${encodeURIComponent(id)}/restore`,
    {},
  ).then((r) => r.data)
}

/**
 * 一键清空回收站：永久删除当前用户回收站里的全部条目。
 *
 * 幂等：回收站为空时返回 purged=0；已归档知识不受影响。
 */
export function emptyWorkbenchTrash(): Promise<number> {
  return post<{ success: boolean; data: { purged: number } }>(`${BASE}/trash/empty`, {})
    .then((r) => r.data.purged)
}

export function getWorkbenchContent(id: string): Promise<WorkbenchItemContent> {
  return get<{ success: boolean; data: WorkbenchItemContent }>(
    `${BASE}/items/${encodeURIComponent(id)}/content`,
  ).then((r) => r.data)
}

export function saveWorkbenchContent(
  id: string,
  payload: {
    base_version: number
    editor_schema_version: number
    ydoc: string
    text_content: string
    client_id: string
    force?: boolean
    checkpoint?: boolean
  },
): Promise<SaveWorkbenchContentResult> {
  return put<{ success: boolean; data: SaveWorkbenchContentResult }>(
    `${BASE}/items/${encodeURIComponent(id)}/content`,
    payload,
  ).then((r) => r.data)
}

export function listWorkbenchVersions(id: string, limit = 20): Promise<WorkbenchVersionSummary[]> {
  return get<{ success: boolean; data: { versions: WorkbenchVersionSummary[] } }>(
    `${BASE}/items/${encodeURIComponent(id)}/versions?limit=${limit}`,
  ).then((r) => r.data.versions)
}

export function restoreWorkbenchVersion(
  id: string,
  version: number,
  baseVersion: number,
): Promise<SaveWorkbenchContentResult> {
  return post<{ success: boolean; data: SaveWorkbenchContentResult }>(
    `${BASE}/items/${encodeURIComponent(id)}/versions/${version}/restore`,
    { base_version: baseVersion },
  ).then((r) => r.data)
}

// ---- 条目资产（§12.1） ----
// 上传走 multipart（postUpload 会自动带 boundary 与更长的超时）；
// 下载不在这里：BlockSuite 需要 Blob，由 workbench/editor/assets.ts 直接 fetch。

export function uploadWorkbenchAsset(itemId: string, file: File): Promise<WorkbenchAsset> {
  const form = new FormData()
  form.append('file', file)
  return postUpload(`${BASE}/items/${encodeURIComponent(itemId)}/assets`, form).then(
    (r) => r.data as WorkbenchAsset,
  )
}

export function listWorkbenchAssets(itemId: string): Promise<WorkbenchAsset[]> {
  return get<{ success: boolean; data: { assets: WorkbenchAsset[] } }>(
    `${BASE}/items/${encodeURIComponent(itemId)}/assets`,
  ).then((r) => r.data.assets)
}

export function deleteWorkbenchAsset(itemId: string, assetId: string): Promise<void> {
  return del<void>(
    `${BASE}/items/${encodeURIComponent(itemId)}/assets/${encodeURIComponent(assetId)}`,
  ).then(() => undefined)
}

/**
 * 上报实际引用的资产，刷新服务端 last_seen_at。
 *
 * 服务端不解析 Y.Doc，只能靠这个调用判断“文件是否还被引用”。
 * 失败不应影响编辑与保存，调用方自行吞掉错误。
 */
export function touchWorkbenchAssets(itemId: string, assetIds: string[]): Promise<number> {
  if (assetIds.length === 0) return Promise.resolve(0)
  return post<{ success: boolean; data: { touched: number } }>(
    `${BASE}/items/${encodeURIComponent(itemId)}/assets/touch`,
    { asset_ids: assetIds },
  ).then((r) => r.data.touched)
}

export function listWorkbenchFolders(): Promise<WorkbenchFolder[]> {
  return get<{ success: boolean; data: { folders: WorkbenchFolder[] } }>(`${BASE}/folders`)
    .then((r) => r.data.folders)
}

export function createWorkbenchFolder(payload: {
  name: string
  parent_id?: string | null
}): Promise<WorkbenchFolder> {
  return post<{ success: boolean; data: WorkbenchFolder }>(`${BASE}/folders`, payload)
    .then((r) => r.data)
}

export function updateWorkbenchFolder(
  id: string,
  payload: { name?: string; parent_id?: string | null },
): Promise<WorkbenchFolder> {
  return put<{ success: boolean; data: WorkbenchFolder }>(
    `${BASE}/folders/${encodeURIComponent(id)}`,
    payload,
  ).then((r) => r.data)
}

/**
 * 拖拽排序 / 跨层移动（P4-2）。
 *
 * position 是「目标父级下、排除自己之后」的插入下标；传 -1 表示追加到该层末尾。
 * 服务端会按新的顺序把整层 sort_order 重写成 0..n-1（幂等归一），
 * 因此这里不需要先读一次当前顺序。
 */
export function moveWorkbenchFolder(
  id: string,
  payload: { parent_id: string | null; position?: number },
): Promise<WorkbenchFolder> {
  return put<{ success: boolean; data: WorkbenchFolder }>(
    `${BASE}/folders/${encodeURIComponent(id)}/move`,
    { parent_id: payload.parent_id, position: payload.position ?? -1 },
  ).then((r) => r.data)
}

// ---- 个人标签（P4-2） ----

export function listWorkbenchTags(): Promise<WorkbenchTag[]> {
  return get<{ success: boolean; data: { tags: WorkbenchTag[] } }>(`${BASE}/tags`).then((r) => r.data.tags)
}

export function createWorkbenchTag(payload: { name: string; color?: string }): Promise<WorkbenchTag> {
  return post<{ success: boolean; data: WorkbenchTag }>(`${BASE}/tags`, payload).then((r) => r.data)
}

export function updateWorkbenchTag(
  id: string,
  payload: { name?: string; color?: string },
): Promise<WorkbenchTag> {
  return put<{ success: boolean; data: WorkbenchTag }>(`${BASE}/tags/${encodeURIComponent(id)}`, payload)
    .then((r) => r.data)
}

export function deleteWorkbenchTag(id: string): Promise<void> {
  return del<{ success: boolean }>(`${BASE}/tags/${encodeURIComponent(id)}`).then(() => undefined)
}

/** 某条目上的标签。 */
export function listWorkbenchItemTags(itemId: string): Promise<WorkbenchTag[]> {
  return get<{ success: boolean; data: { tags: WorkbenchTag[] } }>(
    `${BASE}/items/${encodeURIComponent(itemId)}/tags`,
  ).then((r) => r.data.tags)
}

/** 覆盖式设置条目标签；传空数组即清空。 */
export function setWorkbenchItemTags(itemId: string, tagIds: string[]): Promise<WorkbenchTag[]> {
  return put<{ success: boolean; data: { tags: WorkbenchTag[] } }>(
    `${BASE}/items/${encodeURIComponent(itemId)}/tags`,
    { tag_ids: tagIds },
  ).then((r) => r.data.tags)
}

/** 反向链接（P4-2）：正文里出现过本条目标题的其它条目。 */
export function getWorkbenchBacklinks(itemId: string): Promise<WorkbenchBacklink[]> {
  return get<{ success: boolean; data: { backlinks: WorkbenchBacklink[] } }>(
    `${BASE}/items/${encodeURIComponent(itemId)}/backlinks`,
  ).then((r) => r.data.backlinks)
}

export function deleteWorkbenchFolder(id: string): Promise<void> {
  return del<{ success: boolean }>(`${BASE}/folders/${encodeURIComponent(id)}`).then(() => undefined)
}

// ---- 文档模板（§9.16） ----
// 没有 apply-template 端点：套用由客户端取快照后走普通保存链路。

export function listWorkbenchTemplates(): Promise<WorkbenchTemplateSummary[]> {
  return get<{ success: boolean; data: { templates: WorkbenchTemplateSummary[] } }>(
    `${BASE}/templates`,
  ).then((r) => r.data.templates ?? [])
}

/**
 * 创建模板，两种来源二选一：
 *   - 传 source_item_id：服务端从已保存的文档复制快照（「另存为模板」）；
 *   - 不传 source_item_id：模板管理页「新建模板」，快照由客户端提交。
 */
export function createWorkbenchTemplate(payload: {
  source_item_id?: string
  name: string
  description?: string
  /** 仅新建模式使用，base64 编码的 Y.Doc 字节 */
  ydoc?: string
  editor_schema_version?: number
}): Promise<WorkbenchTemplateSummary> {
  return post<{ success: boolean; data: WorkbenchTemplateSummary }>(`${BASE}/templates`, payload)
    .then((r) => r.data)
}

export function updateWorkbenchTemplate(
  id: string,
  payload: { name?: string; description?: string },
): Promise<WorkbenchTemplateSummary> {
  return patch<{ success: boolean; data: WorkbenchTemplateSummary }>(
    `${BASE}/templates/${encodeURIComponent(id)}`,
    payload,
  ).then((r) => r.data)
}

export function deleteWorkbenchTemplate(id: string): Promise<void> {
  return del<{ success: boolean }>(`${BASE}/templates/${encodeURIComponent(id)}`).then(() => undefined)
}

/**
 * 覆盖模板正文（模板管理页的编辑器提交）。
 *
 * 与 createWorkbenchTemplate 的区别：创建时服务端从已保存条目复制字节；
 * 这里没有源文档，快照只能由客户端提交。
 */
export function updateWorkbenchTemplateContent(
  id: string,
  payload: { ydoc: string; editor_schema_version: number },
): Promise<WorkbenchTemplateSummary> {
  return put<{ success: boolean; data: WorkbenchTemplateSummary }>(
    `${BASE}/templates/${encodeURIComponent(id)}/content`,
    payload,
  ).then((r) => r.data)
}

export function getWorkbenchTemplateContent(id: string): Promise<WorkbenchTemplateContent> {
  return get<{ success: boolean; data: WorkbenchTemplateContent }>(
    `${BASE}/templates/${encodeURIComponent(id)}/content`,
  ).then((r) => r.data)
}

export function getArchiveState(
  sourceType: ArchiveSourceType,
  sourceId: string,
): Promise<WorkbenchArchiveState> {
  const query = new URLSearchParams({ source_type: sourceType, source_id: sourceId })
  return get<{ success: boolean; data: WorkbenchArchiveState }>(
    `${BASE}/archives/state?${query.toString()}`,
  ).then((r) => r.data)
}

export function createArchive(payload: {
  source_type: ArchiveSourceType
  source_id: string
  base_version?: number
  markdown?: string
  force?: boolean
}): Promise<WorkbenchArchiveState> {
  return post<{ success: boolean; data: WorkbenchArchiveState }>(`${BASE}/archives`, payload)
    .then((r) => r.data)
}

export function refreshArchive(payload: {
  source_type: ArchiveSourceType
  source_id: string
  base_version?: number
  markdown?: string
}): Promise<WorkbenchArchiveState> {
  return post<{ success: boolean; data: WorkbenchArchiveState }>(`${BASE}/archives/refresh`, payload)
    .then((r) => r.data)
}

export function deleteArchive(payload: {
  source_type: ArchiveSourceType
  source_id: string
  delete_knowledge: boolean
}): Promise<WorkbenchArchiveState> {
  return post<{ success: boolean; data: WorkbenchArchiveState }>(`${BASE}/archives/delete`, payload)
    .then((r) => r.data)
}

// ---- 数据库 AI（P3-5） ----
// 非流式：模型必须整包返回补丁，前端先预览再写入（§11.7）。
// 这里只发请求，补丁校验与写入分别在 workbench/ai/database-patch.ts 与
// workbench/editor/database-ai.ts。

export interface DatabaseAIPayload {
  item_id: string
  prompt: string
  agent_id?: string
  columns: Array<{ id: string; name: string; type: string }>
  rows: Array<{ id: string; title: string; cells: Record<string, string> }>
  truncated?: boolean
}

export function requestDatabasePatch(payload: DatabaseAIPayload): Promise<{
  patch: { ops: unknown[] }
  model_id?: string
  agent_id?: string
}> {
  return post<{ success: boolean; data: { patch: { ops: unknown[] }; model_id?: string; agent_id?: string } }>(
    `${BASE}/ai/database`,
    payload,
  ).then((r) => r.data)
}

// ---- 事项识别 ----
// 文档正文 → 事项候选：非流式整包 JSON，前端确认后写入事项表。
// 归档后的自动抽取复用同一个端点，只是来源标签不同。

export interface ItemExtractPayload {
  item_id: string
  markdown: string
  agent_id?: string
  source_title?: string
  status_options?: string[]
  priority_options?: string[]
  existing_titles?: string[]
  limit?: number
}

export interface ItemExtractCandidate {
  title: string
  status?: string
  owner?: string
  due?: string
  priority?: string
  relation?: string
  evidence?: string
  confidence?: number
}

export function requestItemCandidates(payload: ItemExtractPayload): Promise<{
  items: ItemExtractCandidate[]
  model_id?: string
  agent_id?: string
}> {
  return post<{
    success: boolean
    data: { items: ItemExtractCandidate[]; model_id?: string; agent_id?: string }
  }>(`${BASE}/ai/items`, payload).then((r) => r.data)
}

// ---- 画布 AI（P2-6） ----
// 与数据库 AI 同构：非流式整包补丁，前端预览确认后一次性写入。
// 元素快照由前端带上，服务端不解析 Y.Doc。

export interface CanvasAIPayload {
  item_id: string
  prompt: string
  agent_id?: string
  elements: Array<{
    id: string
    kind: string
    text: string
    x: number
    y: number
    w: number
    h: number
    frame_id: string
  }>
  frames: Array<{ id: string; title: string; x: number; y: number; w: number; h: number }>
  truncated?: boolean
}

export function requestCanvasPatch(payload: CanvasAIPayload): Promise<{
  patch: { ops: unknown[] }
  model_id?: string
  agent_id?: string
}> {
  return post<{ success: boolean; data: { patch: { ops: unknown[] }; model_id?: string; agent_id?: string } }>(
    `${BASE}/ai/canvas`,
    payload,
  ).then((r) => r.data)
}

// ---- 智能关联（P3） ----
// 与画布 AI 的区别：候选文档的正文只在服务端可见（列表 DTO 不返回 text_content），
// 因此召回必须由服务端做，前端只提交「我是谁 / 要几条 / 已忽略哪些」。

/** 一条关联结果：quote / target_quote 是原文句，用于卡片高亮与原文定位。 */
export interface WorkbenchRelation {
  item_id: string
  title: string
  reason: string
  strength: 'strong' | 'weak'
  quote: string
  target_quote: string
  label: string
  source: string
}

export interface WorkbenchRelationsPayload {
  item_id: string
  agent_id?: string
  limit?: number
  /** 已应用 / 已忽略的条目，召回时直接剔除 */
  exclude_item_ids?: string[]
}

export interface WorkbenchRelationsResult {
  relations: WorkbenchRelation[]
  /** 非空表示没走模型（如 no_model），语义关联不可用 */
  degraded_reason?: string
  truncated?: boolean
  model_id?: string
  agent_id?: string
}

export function requestWorkbenchRelations(
  payload: WorkbenchRelationsPayload,
): Promise<WorkbenchRelationsResult> {
  return post<{ success: boolean; data: WorkbenchRelationsResult }>(
    `${BASE}/ai/relations`,
    payload,
  ).then((r) => r.data)
}

/** 月历数据：只返回 kind='canvas' 且 journal_date 落在该月的条目（§7.11）。 */
export function getJournalsMonth(month?: string): Promise<WorkbenchJournalMonth> {
  const query = month ? `?month=${encodeURIComponent(month)}` : ''
  return get<{ success: boolean; data: WorkbenchJournalMonth }>(`${BASE}/journals/month${query}`)
    .then((r) => r.data)
}

/** 某天的 Journal：不存在返回空数组而非 404（§7.11.4）。 */
export function getJournalDay(date: string): Promise<WorkbenchJournalDay> {
  return get<{ success: boolean; data: WorkbenchJournalDay }>(
    `${BASE}/journals/${encodeURIComponent(date)}`,
  ).then((r) => r.data)
}

/** 离开 workspace 前的只读统计；失败由调用方退化为通用确认。 */
export function getLeavePreview(): Promise<WorkbenchLeavePreview> {
  return get<{ success: boolean; data: WorkbenchLeavePreview }>(`${BASE}/leave-preview`)
    .then((r) => r.data)
}

// 用户级通知不走 /workbench：被移出最后一个 workspace 后仍要能读到（§5.7）。
const NOTICE_BASE = '/api/v1/me/workbench-notices'

export function listWorkbenchNotices(
  opts: { unreadOnly?: boolean; limit?: number } = {},
): Promise<WorkbenchNoticePage> {
  const query = new URLSearchParams()
  if (opts.unreadOnly) query.set('unread_only', '1')
  query.set('limit', String(opts.limit ?? 20))
  return get<{ success: boolean; data: WorkbenchNoticePage }>(
    `${NOTICE_BASE}?${query.toString()}`,
  ).then((r) => r.data)
}

export function markWorkbenchNoticeRead(id: string): Promise<void> {
  return post<{ success: boolean }>(
    `${NOTICE_BASE}/${encodeURIComponent(id)}/read`,
    {},
  ).then(() => undefined)
}

export function markAllWorkbenchNoticesRead(): Promise<void> {
  return post<{ success: boolean }>(`${NOTICE_BASE}/read-all`, {}).then(() => undefined)
}
