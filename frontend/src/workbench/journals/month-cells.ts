/**
 * Journals 月格的纯投影逻辑（§9.13.2）。
 *
 * 只做「条目 → 状态点」的聚合，不请求数据、不碰 DOM：
 * 月格每格最多 3 个点、七日条每格最多 2 个点，超出的用 +N 表示。
 * 排序按 partial > stale > processing > active > deleted > none，与图例一致。
 */
import { statusRank } from '../archive-status'
import type { ArchiveStatus, WorkbenchJournalEntry } from '@/types/workbench'

/** 月格/图例里点与文案的固定顺序；source_deleted 不画点（§9.13.2 表）。 */
export const DOT_STATUS_ORDER: ArchiveStatus[] = [
  'partial',
  'stale',
  'processing',
  'active',
  'deleted',
  'none',
]

export interface DotLayout {
  /** 需要画出的状态点，已按严重程度排序并截断 */
  dots: ArchiveStatus[]
  /** 被截断的条目数；>0 时显示 +N */
  extra: number
}

/** 按日期分组；条目自带 journal_date，缺失的条目被忽略。 */
export function groupByDate(
  entries: WorkbenchJournalEntry[],
): Map<string, WorkbenchJournalEntry[]> {
  const grouped = new Map<string, WorkbenchJournalEntry[]>()
  for (const entry of entries) {
    if (!entry.journal_date) continue
    const rows = grouped.get(entry.journal_date)
    if (rows) rows.push(entry)
    else grouped.set(entry.journal_date, [entry])
  }
  return grouped
}

/** 取某天最严重的 max 个状态点，其余计数进 extra。 */
export function dotLayout(
  entries: WorkbenchJournalEntry[],
  max: number,
): DotLayout {
  if (entries.length === 0 || max <= 0) return { dots: [], extra: 0 }
  // 稳定排序：同状态保持后端返回的 created_at 升序，避免每次渲染点位跳动。
  const sorted = entries
    .map((entry, index) => ({ status: entry.archive_status ?? 'none', index }))
    .sort((a, b) => statusRank(b.status) - statusRank(a.status) || a.index - b.index)
  const dots = sorted.slice(0, max).map((row) => row.status)
  return { dots, extra: Math.max(0, entries.length - max) }
}

/** 各状态条数，用于月格 aria-label 与图例提示。 */
export function statusCounts(entries: WorkbenchJournalEntry[]): Record<ArchiveStatus, number> {
  const counts = {
    none: 0,
    deleted: 0,
    active: 0,
    stale: 0,
    processing: 0,
    partial: 0,
    source_deleted: 0,
  } satisfies Record<ArchiveStatus, number>
  for (const entry of entries) {
    const status = entry.archive_status ?? 'none'
    counts[status] += 1
  }
  return counts
}

/**
 * 该月实际有条目的天数与总条数。
 *
 * 面板计数只展示「本月 N 条」，truncated 时由视图补一句“已加载 N 条”，
 * 不假装拿到完整总数（§9.13.1）。
 */
export function monthSummary(entries: WorkbenchJournalEntry[], month: string): {
  total: number
  days: number
} {
  const grouped = groupByDate(entries)
  let total = 0
  let days = 0
  for (const [date, rows] of grouped) {
    if (!date.startsWith(month)) continue
    total += rows.length
    days += 1
  }
  return { total, days }
}
