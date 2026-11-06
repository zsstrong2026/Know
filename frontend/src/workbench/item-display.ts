/**
 * 工作台条目的展示口径：副标题、相对时间与按天分组。
 *
 * 卡片视图与列表视图共用同一套口径，避免两个视图对同一条目给出不同描述。
 */
import i18n from '@/i18n'
import type { WorkbenchItem } from '@/types/workbench'

/**
 * 副标题：查看模式 · 日历日期，卡片描述与列表行第二行共用。
 *
 * 文档与画布是同一份内容的两种查看模式，因此这里只描述当前模式，
 * 不再输出「画布 · 文档」这种把两者并列的错误口径。
 * 文案走 i18n（`t` 可选，默认取全局实例）。
 */
export function itemCaption(
  item: WorkbenchItem,
  translate?: (key: string) => string,
): string {
  const t = translate ?? ((key: string) => i18n.global.t(key))
  if (item.kind === 'database') {
    const parts = [t('workbench.database.caption')]
    if (item.journal_date) parts.push(item.journal_date)
    return parts.join(' · ')
  }
  const mode = item.current_mode ?? 'edgeless'
  const parts = [
    t(mode === 'page' ? 'workbench.modeSwitch.page' : 'workbench.modeSwitch.edgeless'),
  ]
  if (item.journal_date) parts.push(item.journal_date)
  return parts.join(' · ')
}

/** 相对时间：7 天内用「x 分钟/小时/天前」，更早显示日期。 */
export function formatRelativeTime(raw: string): string {
  const time = new Date(raw).getTime()
  if (!Number.isFinite(time)) return ''
  const diff = Date.now() - time
  const minute = 60_000
  const hour = 60 * minute
  const day = 24 * hour
  if (diff < minute) return '刚刚'
  if (diff < hour) return `${Math.floor(diff / minute)} 分钟前`
  if (diff < day) return `${Math.floor(diff / hour)} 小时前`
  if (diff < 7 * day) return `${Math.floor(diff / day)} 天前`
  return new Date(time).toLocaleDateString()
}

/** 某个时间点所在自然日的零点（本地时区）。 */
export function dayStart(raw: string): number {
  const date = new Date(raw)
  if (!Number.isFinite(date.getTime())) return Number.NaN
  date.setHours(0, 0, 0, 0)
  return date.getTime()
}

/** 今天零点，用于把第一组标成「今天」。 */
export function todayStart(): number {
  const date = new Date()
  date.setHours(0, 0, 0, 0)
  return date.getTime()
}

/**
 * 按更新日期分组。
 *
 * 服务端已按 updated_at 降序返回，同一天的条目必然连续，
 * 因此只比较「上一组」，不需要 Map 聚合，也不会打乱加载顺序。
 */
export function groupItemsByDay(
  items: WorkbenchItem[],
): Array<{ day: number; items: WorkbenchItem[] }> {
  const groups: Array<{ day: number; items: WorkbenchItem[] }> = []
  for (const item of items) {
    const day = dayStart(item.updated_at)
    const last = groups[groups.length - 1]
    // NaN（时间非法）不与任何值相等，会单独成组，避免把脏数据并进某一天
    if (last && last.day === day) last.items.push(item)
    else groups.push({ day, items: [item] })
  }
  return groups
}
