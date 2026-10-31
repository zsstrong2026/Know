/**
 * 工作台 API 客户端。
 *
 * 约束（§9.3）：
 *   - 组件里禁止手写 URL，一律从这里导入；
 *   - owner 从不作为参数传递，由后端从 JWT 上下文取；
 *   - 统一在后端响应 `{ success, data }` 上解包，调用方直接拿业务对象；
 *   - 错误码统一用 workbenchErrorCode() 读取，不要在别处再抄一份判断。
 */
import { get, patch, post, put, del } from '@/utils/request'
import { WORKBENCH_ERROR, workbenchConflictVersion, workbenchErrorCode } from '@/workbench/error-code'
import type {
  ArchiveSourceType,
  SaveWorkbenchContentResult,
  WorkbenchArchiveState,
  WorkbenchBootstrap,
  WorkbenchFolder,
  WorkbenchItem,
  WorkbenchItemContent,
  WorkbenchItemKind,
  WorkbenchItemPage,
  WorkbenchJournalDay,
  WorkbenchJournalMonth,
  WorkbenchLeavePreview,
  WorkbenchNoticePage,
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

export interface ListWorkbenchItemsParams {
  kind?: WorkbenchItemKind
  folder_id?: string
  favorite?: boolean
  trashed?: boolean
  q?: string
  cursor?: string
  limit?: number
}

export function listWorkbenchItems(params: ListWorkbenchItemsParams = {}): Promise<WorkbenchItemPage> {
  const query = new URLSearchParams()
  if (params.kind) query.set('kind', params.kind)
  if (params.folder_id) query.set('folder_id', params.folder_id)
  if (params.favorite) query.set('favorite', '1')
  if (params.trashed) query.set('trashed', '1')
  if (params.q) query.set('q', params.q)
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

export function createWorkbenchTemplate(payload: {
  source_item_id: string
  name: string
  description?: string
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
