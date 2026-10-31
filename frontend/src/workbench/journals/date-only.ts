/**
 * Journals 的日期纯逻辑：与后端 types.DateOnly / ParseMonth 对齐。
 *
 * 全链路只传 YYYY-MM-DD / YYYY-MM 字符串，不做时区换算：
 * 一旦用 new Date('2026-10-30') 取本地日期，东八区晚上会得到前一天。
 * 需要加减天数时统一走 UTC 毫秒运算，再用 toISOString 截出日期。
 */

/** 一天的毫秒数 */
const DAY_MS = 86_400_000

/** 解析 YYYY-MM，返回该月首末日（YYYY-MM-DD）。非法返回 null。 */
export function monthRange(month: string): { start: string; end: string } | null {
  if (!/^\d{4}-\d{2}$/.test(month)) return null
  const year = Number(month.slice(0, 4))
  const mon = Number(month.slice(5, 7))
  if (mon < 1 || mon > 12) return null
  // Date.UTC 的 day=0 表示“上个月最后一天”，即本月月末；闰年自动正确。
  const lastDay = new Date(Date.UTC(year, mon, 0)).getUTCDate()
  return {
    start: `${month}-01`,
    end: `${month}-${String(lastDay).padStart(2, '0')}`,
  }
}

/** 把 month 平移 delta 个月（跨年自动处理）。 */
export function shiftMonth(month: string, delta: number): string {
  const range = monthRange(month)
  if (!range) return month
  const year = Number(month.slice(0, 4))
  const mon = Number(month.slice(5, 7))
  const total = year * 12 + (mon - 1) + delta
  const nextYear = Math.floor(total / 12)
  const nextMon = (total % 12 + 12) % 12 + 1
  return `${nextYear}-${String(nextMon).padStart(2, '0')}`
}

/** 解析 YYYY-MM-DD 为 UTC 时间戳；非法返回 null。 */
export function parseDate(date: string): number | null {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) return null
  const ts = Date.parse(`${date}T00:00:00Z`)
  return Number.isNaN(ts) ? null : ts
}

/** 日期加减天数（跨月/跨年自动处理）。非法输入原样返回。 */
export function addDays(date: string, delta: number): string {
  const ts = parseDate(date)
  if (ts === null) return date
  return new Date(ts + delta * DAY_MS).toISOString().slice(0, 10)
}

/** 星期索引：0=周日 … 6=周六。非法输入返回 0。 */
export function weekdayIndex(date: string): number {
  const ts = parseDate(date)
  if (ts === null) return 0
  return new Date(ts).getUTCDay()
}

/** 所在自然周的周日（§9.13.2：周日为一周首日）。 */
export function weekStart(date: string): string {
  return addDays(date, -weekdayIndex(date))
}

/** 所在自然周的七天，按周日→周六排列。 */
export function weekDays(date: string): string[] {
  const start = weekStart(date)
  return Array.from({ length: 7 }, (_, i) => addDays(start, i))
}

/** 生成 6×7 月格：从周日起，含上下月补白，固定 42 格。 */
export function monthGrid(month: string): Array<{ date: string; inMonth: boolean }> {
  const range = monthRange(month)
  if (!range) return []
  const first = range.start
  const offset = weekdayIndex(first)
  const gridStart = addDays(first, -offset)
  return Array.from({ length: 42 }, (_, i) => {
    const iso = addDays(gridStart, i)
    return { date: iso, inMonth: iso.startsWith(month) }
  })
}

/** 当前本地日期（YYYY-MM-DD）。只用于“今天”高亮，不参与存储。 */
export function todayISO(now: Date = new Date()): string {
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`
}

/** 当前本地月份（YYYY-MM）。 */
export function currentMonth(now: Date = new Date()): string {
  return todayISO(now).slice(0, 7)
}

/** 格式化成标题用的「YYYY 年 M 月 D 日」（§9.13.3 新建 Journal 的固定标题）。 */
export function formatJournalTitle(date: string): string {
  const ts = parseDate(date)
  if (ts === null) return date
  const d = new Date(ts)
  return `${d.getUTCFullYear()} 年 ${d.getUTCMonth() + 1} 月 ${d.getUTCDate()} 日`
}
