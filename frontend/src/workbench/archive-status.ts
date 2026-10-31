/**
 * 归档状态的纯逻辑：徽标文案、优先级与“可否操作”的判定。
 *
 * 与后端 archive.go 的状态机一一对应，但这里只做展示口径，不参与权限判断。
 * 抽成纯函数是为了可单测：组件只负责把返回值渲染成 DOM。
 */
import type { ArchiveStatus, WorkbenchArchiveState } from '@/types/workbench'

/** 七态徽标。文案必须同时给出文字，不能只靠颜色传达状态（§9.8 无障碍）。 */
export interface ArchiveBadge {
  status: ArchiveStatus
  /** i18n key 后缀，形如 workbench.archive.status.active */
  key: string
  /** 归档版本；0/undefined 表示没有可展示的版本 */
  version: number
  /** 需要展示的主操作：归档 / 更新归档 / 重试 / 重新归档 / 查看进度 */
  action: ArchiveBadgeAction
}

export type ArchiveBadgeAction = 'none' | 'create' | 'update' | 'retry' | 'recreate' | 'progress'

/**
 * 计算列表卡片上的归档徽标。
 *
 * 入参优先用 state 里的真实版本；列表批量填充时可能只有 archive_status/archived_version，
 * 因此这两个字段都是可选的，缺失时退回 0（不显示版本号）。
 */
export function archiveBadge(
  status: ArchiveStatus | undefined,
  archivedVersion: number | undefined,
): ArchiveBadge {
  const resolved: ArchiveStatus = status ?? 'none'
  const version = archivedVersion && archivedVersion > 0 ? archivedVersion : 0
  return { status: resolved, key: `workbench.archive.status.${resolved}`, version, action: actionFor(resolved) }
}

function actionFor(status: ArchiveStatus): ArchiveBadgeAction {
  switch (status) {
    case 'none':
      return 'create'
    case 'stale':
      return 'update'
    case 'partial':
      return 'retry'
    case 'deleted':
      return 'recreate'
    case 'processing':
      return 'progress'
    default:
      // active / source_deleted：不提供主操作（源已删除时刷新无意义）
      return 'none'
  }
}

/** 归档是否允许“更新/重试”：源已删除、处理中、无归档都不能点。 */
export function canReArchive(status: ArchiveStatus | undefined): boolean {
  switch (status) {
    case 'stale':
    case 'partial':
      return true
    default:
      return false
  }
}

/** 删除归档是否可用：只有存在 live 映射（active/stale/partial）时才有东西可删。 */
export function canDeleteArchive(status: ArchiveStatus | undefined): boolean {
  return status === 'active' || status === 'stale' || status === 'partial'
}

/** 状态优先级（数值越大越优先），用于聚合多 part 时取最严重的一个。 */
const STATUS_PRIORITY: Record<ArchiveStatus, number> = {
  source_deleted: 6,
  partial: 5,
  processing: 4,
  stale: 3,
  active: 2,
  deleted: 1,
  none: 0,
}

/** 从多 part 的 state 里取代表状态：优先级最高的那个。 */
export function dominantStatus(state: WorkbenchArchiveState | null | undefined): ArchiveStatus {
  if (!state) return 'none'
  // state.status 已由后端按同一优先级聚合过，这里只做兜底：
  // 如果它缺失或不是合法值，再按 parts 里的 knowledge_exists / source_version 重算。
  return state.status ?? 'none'
}

export function statusRank(status: ArchiveStatus | undefined): number {
  return STATUS_PRIORITY[status ?? 'none'] ?? 0
}

/** 聚合两个状态，返回更严重的那个（列表批量填充时偶发不一致会用到）。 */
export function worstStatus(
  a: ArchiveStatus | undefined,
  b: ArchiveStatus | undefined,
): ArchiveStatus {
  return statusRank(a) >= statusRank(b) ? (a ?? 'none') : (b ?? 'none')
}
