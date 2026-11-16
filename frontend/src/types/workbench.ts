/**
 * 工作台 DTO（与后端 internal/types/workbench.go 的 json tag 一一对应）。
 *
 * 约定：
 *   - 时间统一为 RFC3339 UTC 字符串，展示层再本地化；
 *   - archive_status 只在列表响应里批量填充，创建/详情可能没有；
 *   - current_mode 为 null 时按 kind 兜底为 edgeless（后端 DefaultModeForKind 同口径）。
 */

export type WorkbenchItemKind = 'canvas' | 'database'

export type WorkbenchScope = 'recent' | 'database' | 'folder' | 'favorites' | 'trash'

/** 列表排序（B5）：最近更新 / 创建时间 / 名称。 */
export type WorkbenchItemSort = 'updated' | 'created' | 'title'

/** 新建菜单的四个入口（§9.5 / §9.16.2）。 */
export type WorkbenchCreateAction = 'document' | 'canvas' | 'database' | 'folder'

export type ArchiveSourceType = 'item' | 'doc'

/** 七态：后端由映射行 + 知识存在性实时聚合，不落库（§7.6）。 */
export type ArchiveStatus =
  | 'none'
  | 'processing'
  | 'active'
  | 'stale'
  | 'partial'
  | 'source_deleted'
  | 'deleted'

export interface WorkbenchKbCapabilities {
  vector: boolean
  keyword: boolean
}

export interface WorkbenchKbSummary {
  id: string
  name: string
  is_system: boolean
  system_key: string
  visibility: 'private' | 'workspace'
  embedding_model_id: string
  /** 工作库真实文档数（含归档与导入），不是墨匣条目数 */
  knowledge_count: number
  capabilities: WorkbenchKbCapabilities
}

export interface WorkbenchBootstrapCounts {
  items: number
  folders: number
  documents: number
  archive_parts: number
}

export interface WorkbenchBootstrapLimits {
  item_content_bytes: number
  archive_total_bytes: number
  archive_parts: number
}

/** 发布门禁状态（§9.1）：由 bootstrap 下发，前端据此隐藏入口，不硬编码。 */
export interface WorkbenchFeatureToggles {
  templates: boolean
  journals: boolean
  ai: boolean
  dual_mode: boolean
}

export interface WorkbenchBootstrap {
  work_kb: WorkbenchKbSummary
  needs_semantic_index: boolean
  counts: WorkbenchBootstrapCounts
  unread_notices: number
  limits: WorkbenchBootstrapLimits
  features: WorkbenchFeatureToggles
  server_time: string
}

/** 语义索引重建结果（§7.8）：queued 是已重新入队的知识条数。 */
export interface WorkbenchReindexResult {
  queued: number
  model_id: string
}

export interface WorkbenchItem {
  id: string
  kind: WorkbenchItemKind
  title: string
  icon: string
  folder_id: string | null
  snapshot_version: number
  visibility: 'private' | 'workspace'
  is_favorite: boolean
  /** 路线 1：YYYY-MM-DD 或 null（未钉日历）；后端 DateOnly 无值时为 null。 */
  journal_date: string | null
  /** 路线 2：null 表示未显式设置，前端按 edgeless 兜底。 */
  current_mode: 'page' | 'edgeless' | null
  last_opened_at: string | null
  created_at: string
  updated_at: string
  archive_status?: ArchiveStatus
  archived_version?: number
}

/** 个人标签（P4-2）：tenant_id + owner_id 隔离，同 workspace 的其他人不可见。 */
export interface WorkbenchTag {
  id: string
  name: string
  /** 白名单色名：gray / blue / green / yellow / red / purple */
  color: string
  item_count: number
  created_at: string
  updated_at: string
}

/** 反向链接（P4-2「页内提及」）：谁在正文里提到过本条目。 */
export interface WorkbenchBacklink {
  /** 引用方条目 id，点击可跳转 */
  id: string
  title: string
  kind: string
  updated_at: string
}

export interface WorkbenchItemPage {
  items: WorkbenchItem[]
  next_cursor: string
}

export interface WorkbenchFolder {
  id: string
  name: string
  parent_id: string | null
  sort_order: number
  /** 该文件夹直属的未删除文档数；列表接口聚合返回 */
  item_count: number
  created_at: string
  updated_at: string
}

export interface WorkbenchItemContent {
  id: string
  snapshot_version: number
  editor_schema_version: number
  /** base64 编码的 Y.Doc 字节。 */
  ydoc: string
  text_content: string
  updated_at: string
}

export interface WorkbenchVersionSummary {
  version: number
  editor_schema_version: number
  size_bytes: number
  created_at: string
}

/**
 * 条目资产元数据（§12.1）。
 *
 * 不含 storage_path：服务端从不在响应里给出存储路径，下载只认 asset id。
 */
export interface WorkbenchAsset {
  id: string
  item_id: string
  original_name: string
  mime_type: string
  size_bytes: number
  created_at: string
}

/** 文档模板元数据（§9.16）；不含 ydoc，最多 50 条，不分页。 */
export interface WorkbenchTemplateSummary {
  id: string
  name: string
  description: string
  size_bytes: number
  editor_schema_version: number
  source_item_id?: string
  created_at: string
  updated_at: string
}

/** 套用模板时取到的快照；ydoc 为 base64。 */
export interface WorkbenchTemplateContent {
  id: string
  name: string
  ydoc: string
  editor_schema_version: number
  size_bytes: number
}

export interface WorkbenchArchivePart {
  part_no: number
  part_count: number
  title: string
  knowledge_id: string
  parse_status: string
  source_version: number
  knowledge_exists: boolean
}

export interface WorkbenchArchiveState {
  source_type: ArchiveSourceType
  source_id: string
  current_version: number | null
  archived_version: number | null
  status: ArchiveStatus
  stale_reason?: string
  kb_id: string
  parts: WorkbenchArchivePart[]
  updated_at: string | null
}

export interface SaveWorkbenchContentResult {
  snapshot_version: number
  editor_schema_version: number
  checkpointed: boolean
  updated_at: string
}

/** Journals 月历条目（§7.11，与 Go 的 WorkbenchJournalEntry 一一对应）。 */
export interface WorkbenchJournalEntry {
  id: string
  title: string
  icon?: string
  /** 已兜底为 YYYY-MM-DD，不会是 null */
  journal_date: string
  /** 已按 DefaultModeForKind 兜底，不会为 null */
  current_mode: 'page' | 'edgeless'
  updated_at: string
  archive_status: ArchiveStatus
  /** 各 part source_version 最小值；0 表示没有归档 */
  archived_version: number
}

export interface WorkbenchJournalMonth {
  month: string
  entries: WorkbenchJournalEntry[]
  truncated: boolean
}

export interface WorkbenchJournalDay {
  date: string
  /** 0 条是合法的“这天还没写” */
  entries: WorkbenchJournalEntry[]
}

/** 用户级通知类型（§5.7）。 */
export type WorkbenchNoticeKind =
  | 'membership_removed'
  | 'membership_restored'
  | 'cleanup_warning'
  | 'archive_failed'

/** 通知 payload：只有数量与时间，不含任何正文。 */
export interface WorkbenchNoticePayload {
  kb_count?: number
  knowledge_count?: number
  item_count?: number
  doc_count?: number
  tenant_name?: string
  purge_after?: string
  task_id?: string
  doc_id?: string
  [key: string]: unknown
}

export interface WorkbenchNotice {
  id: string
  kind: WorkbenchNoticeKind
  tenant_id: number
  payload: WorkbenchNoticePayload
  created_at: string
  read_at: string | null
}

export interface WorkbenchNoticePage {
  items: WorkbenchNotice[]
  unread: number
}

/** 离开 workspace 前的只读统计（§7.7）。 */
export interface WorkbenchLeavePreview {
  kb_count: number
  knowledge_count: number
  item_count: number
  doc_count: number
  purge_after_days: number
  export_links: Array<{ label: string; href: string }>
}
