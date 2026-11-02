/**
 * 条目「…」菜单：卡片与列表行共用同一份菜单定义。
 *
 * 菜单只描述「能做什么」，动作由列表页统一派发，
 * 避免两个视图各自维护一套 value → 事件的 switch。
 */
import type { WorkbenchItem } from '@/types/workbench'

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
    { content: t('workbench.archive.action.update'), value: 'archive' },
    { content: t('workbench.archive.deleteTitle'), value: 'deleteArchives' },
    { content: t('workbench.item.delete'), value: 'trash' },
  ]
}

/** 下拉组件回传的 value 是 unknown，这里收窄成确定的动作枚举。 */
export function resolveItemMenuAction(value: unknown): WorkbenchItemMenuAction | null {
  const key = String(value ?? '')
  return MENU_ACTIONS.has(key) ? (key as WorkbenchItemMenuAction) : null
}
