/**
 * 条目「…」菜单：卡片与列表行共用同一份菜单定义。
 *
 * 菜单只描述「能做什么」，动作由列表页统一派发，
 * 避免两个视图各自维护一套 value → 事件的 switch。
 */
import type { WorkbenchItem } from '@/types/workbench'
import { archiveBadge } from './archive-status'

export type WorkbenchItemMenuAction =
  | 'open'
  | 'rename'
  | 'move'
  | 'pin'
  | 'archive'
  | 'deleteArchives'
  | 'trash'
  | 'restore'
  | 'purge'

export interface WorkbenchItemMenuOption {
  content: string
  value: WorkbenchItemMenuAction
  /** 置灰但保留入口：让用户看到「为什么现在不能做」，而不是选项凭空消失 */
  disabled?: boolean
}

const MENU_ACTIONS: ReadonlySet<string> = new Set<WorkbenchItemMenuAction>([
  'open',
  'rename',
  'move',
  'pin',
  'archive',
  'deleteArchives',
  'trash',
  'restore',
  'purge',
])

/**
 * 生成菜单项。
 *
 * 回收站里只剩恢复/永久删除；「钉到某天」只对画布开放（Journals 只含 canvas）。
 * 归档入口按当前状态取动作名（归档 / 更新归档 / 重试 / 重新归档），
 * 不可操作时置灰而不是隐藏，避免菜单项忽有忽无。
 */
export function buildItemMenuOptions(
  item: WorkbenchItem,
  trashed: boolean,
  t: (key: string) => string,
): WorkbenchItemMenuOption[] {
  if (trashed) {
    return [
      { content: t('workbench.item.restore'), value: 'restore' },
      { content: t('workbench.item.purge'), value: 'purge' },
    ]
  }
  const badge = archiveBadge(item.archive_status, item.archived_version)
  // create 之外的动作用「更新归档」这一套文案；处理中/源已删除只置灰
  const archiveLabel =
    badge.action === 'none' || badge.action === 'progress'
      ? t('workbench.archive.action.update')
      : t(`workbench.archive.action.${badge.action}`)
  const archiveDisabled =
    item.archive_status === 'processing' || item.archive_status === 'source_deleted'
  return [
    { content: t('workbench.item.open'), value: 'open' },
    { content: t('workbench.item.rename'), value: 'rename' },
    // 文件夹只收纳文档/画布：数据库不提供「移动到文件夹」，避免出现无法归类的入口。
    ...(item.kind !== 'database'
      ? [{ content: t('workbench.item.move'), value: 'move' as const }]
      : []),
    // 「钉到某天」只对画布开放（Journals 只含 canvas）
    ...(item.kind === 'canvas'
      ? [{ content: t('workbench.item.pin'), value: 'pin' as const }]
      : []),
    { content: archiveLabel, value: 'archive', disabled: archiveDisabled },
    { content: t('workbench.archive.deleteTitle'), value: 'deleteArchives' },
    { content: t('workbench.item.delete'), value: 'trash' },
  ]
}

/** 下拉组件回传的 value 是 unknown，这里收窄成确定的动作枚举。 */
export function resolveItemMenuAction(value: unknown): WorkbenchItemMenuAction | null {
  const key = String(value ?? '')
  return MENU_ACTIONS.has(key) ? (key as WorkbenchItemMenuAction) : null
}
