/**
 * 数据库日期工具（P3-3，实现方案 §10.8.2）。
 *
 * 与 `journals/date-only.ts` 刻意分开：
 *   - 这里是**宽容**的：数据库单元格里用户可能存了带时间的时间戳或任意字符串，
 *     要尽量归一化成「本地日历日」，归一不了才返回 null；
 *   - Journals 那边是**严格**的：只接受 `YYYY-MM-DD` 且必须是真实日历日。
 *
 * 铁律：禁止用 `toISOString().slice(0, 10)` 归一化日期 —— 东八区晚上会退到前一天。
 */

/** Date → 本地日历日 `YYYY-MM-DD`。 */
export function formatLocalDate(date: Date): string {
  const y = date.getFullYear()
  const m = String(date.getMonth() + 1).padStart(2, '0')
  const d = String(date.getDate()).padStart(2, '0')
  return `${y}-${m}-${d}`
}

/** 时间戳 → 本地日历日；非法值返回空串。 */
export function timestampToDateOnly(value: unknown): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) return ''
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '' : formatLocalDate(date)
}

/**
 * 任意值 → 本地日历日。
 *
 * 字符串优先取前缀 `YYYY-MM-DD`（用户手填的 ISO 值），其余交给 Date 解析；
 * 数字按毫秒时间戳处理。解析不出真实日期时返回 null。
 */
export function parseDateOnly(value: unknown): string | null {
  if (typeof value === 'string') {
    const match = /^(\d{4})-(\d{2})-(\d{2})/.exec(value)
    if (match) return isRealDate(match[1], match[2], match[3]) ? `${match[1]}-${match[2]}-${match[3]}` : null
    const parsed = new Date(value)
    return Number.isNaN(parsed.getTime()) ? null : formatLocalDate(parsed)
  }
  if (typeof value === 'number' && Number.isFinite(value)) {
    const parsed = new Date(value)
    return Number.isNaN(parsed.getTime()) ? null : formatLocalDate(parsed)
  }
  return null
}

/** `YYYY-MM-DD` → 本地 0 点时间戳；非法返回 null。 */
export function dateOnlyToTimestamp(iso: string): number | null {
  const parts = parseIso(iso)
  if (!parts) return null
  // 用本地构造器而不是 Date.parse：后者对纯日期按 UTC 解析，东八区会偏 8 小时。
  return new Date(parts.year, parts.month - 1, parts.day).getTime()
}

/** 本地日历日加减天数（跨月/跨年/闰年按日历算，不按 24 小时）。 */
export function shiftDateOnly(iso: string, days: number): string {
  const parts = parseIso(iso)
  if (!parts) return iso
  const date = new Date(parts.year, parts.month - 1, parts.day + days)
  return formatLocalDate(date)
}

/** 两个本地日历日之间相差的天数（to - from）。 */
export function diffDateOnly(from: string, to: string): number | null {
  const a = dateOnlyToTimestamp(from)
  const b = dateOnlyToTimestamp(to)
  if (a === null || b === null) return null
  // 用 UTC 毫秒差再取整，避开夏令时当天 23/25 小时造成的 ±1 天误差。
  const utcA = Date.UTC(...toUtcParts(from))
  const utcB = Date.UTC(...toUtcParts(to))
  return Math.round((utcB - utcA) / 86_400_000)
}

/** 月份游标平移；`month` 形如 `YYYY-MM`。 */
export function shiftMonthCursor(month: string, delta: number): string {
  const parts = parseIso(`${month}-01`)
  if (!parts) return month
  const date = new Date(parts.year, parts.month - 1 + delta, 1)
  return formatLocalDate(date).slice(0, 7)
}

/** 当前本地月份 `YYYY-MM`。 */
export function currentMonthCursor(now: Date = new Date()): string {
  return formatLocalDate(now).slice(0, 7)
}

/**
 * 当前本地月份的边界：[本月 1 日 0 点, 下月 1 日 0 点)。
 *
 * 「截止日 在本月」在上游没有现成筛选函数（date 只有 before / after），
 * 因此拆成 `after(start - 1)` + `before(end)` 两条**原生**条件，
 * 语义等价且仍写在同一份视图数据里，不引入第二套筛选状态。
 */
export function monthRange(now: Date = new Date()): { start: number; end: number } {
  const year = now.getFullYear()
  const month = now.getMonth()
  return {
    start: new Date(year, month, 1).getTime(),
    end: new Date(year, month + 1, 1).getTime(),
  }
}

function parseIso(iso: string): { year: number; month: number; day: number } | null {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(iso)
  if (!match) return null
  if (!isRealDate(match[1], match[2], match[3])) return null
  return { year: Number(match[1]), month: Number(match[2]), day: Number(match[3]) }
}

/** 排除 2026-02-31 这类“格式正确但不存在”的日期。 */
function isRealDate(year: string, month: string, day: string): boolean {
  const y = Number(year)
  const m = Number(month)
  const d = Number(day)
  if (m < 1 || m > 12 || d < 1) return false
  const probe = new Date(y, m - 1, d)
  return probe.getFullYear() === y && probe.getMonth() === m - 1 && probe.getDate() === d
}

function toUtcParts(iso: string): [number, number, number] {
  const parts = parseIso(iso)
  return parts ? [parts.year, parts.month - 1, parts.day] : [0, 0, 0]
}
