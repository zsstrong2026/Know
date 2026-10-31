import assert from 'node:assert/strict'
import { test } from 'node:test'

import { dotLayout, groupByDate, monthSummary, statusCounts } from './month-cells.ts'
import type { ArchiveStatus, WorkbenchJournalEntry } from '@/types/workbench'

/** 构造最小条目：只填月格聚合需要的字段。 */
function entry(
  id: string,
  date: string,
  status: ArchiveStatus = 'none',
): WorkbenchJournalEntry {
  return {
    id,
    title: id,
    journal_date: date,
    current_mode: 'page',
    updated_at: '2026-10-30T00:00:00Z',
    archive_status: status,
    archived_version: 0,
  }
}

test('groupByDate 按日期分组，忽略无日期的条目', () => {
  const grouped = groupByDate([
    entry('a', '2026-10-30'),
    entry('b', '2026-10-30'),
    entry('c', '2026-10-31'),
    { ...entry('d', '2026-10-31'), journal_date: '' },
  ])
  assert.equal(grouped.get('2026-10-30')?.length, 2)
  assert.equal(grouped.get('2026-10-31')?.length, 1, '空日期的条目被丢弃')
})

test('dotLayout 按严重程度排序并截断', () => {
  const rows = [
    entry('a', '2026-10-30', 'none'),
    entry('b', '2026-10-30', 'active'),
    entry('c', '2026-10-30', 'partial'),
    entry('d', '2026-10-30', 'stale'),
  ]
  const month = dotLayout(rows, 3)
  assert.deepEqual(month.dots, ['partial', 'stale', 'active'], '取最严重的三个')
  assert.equal(month.extra, 1, '剩余 1 条进 +N')

  const week = dotLayout(rows, 2)
  assert.deepEqual(week.dots, ['partial', 'stale'])
  assert.equal(week.extra, 2)
})

test('dotLayout 空数组与 max<=0 都不画点', () => {
  assert.deepEqual(dotLayout([], 3), { dots: [], extra: 0 })
  assert.deepEqual(dotLayout([entry('a', '2026-10-30')], 0), { dots: [], extra: 0 })
})

test('dotLayout 同状态保持原顺序，避免渲染抖动', () => {
  const rows = [
    entry('first', '2026-10-30', 'active'),
    entry('second', '2026-10-30', 'active'),
    entry('third', '2026-10-30', 'active'),
  ]
  const layout = dotLayout(rows, 2)
  assert.deepEqual(layout.dots, ['active', 'active'])
  assert.equal(layout.extra, 1)
})

test('statusCounts 统计七态，缺失状态按 none', () => {
  const counts = statusCounts([
    entry('a', '2026-10-30', 'active'),
    entry('b', '2026-10-30', 'processing'),
    entry('c', '2026-10-30', 'partial'),
    { ...entry('d', '2026-10-30'), archive_status: undefined as unknown as ArchiveStatus },
  ])
  assert.equal(counts.active, 1)
  assert.equal(counts.processing, 1)
  assert.equal(counts.partial, 1)
  assert.equal(counts.none, 1)
  assert.equal(counts.source_deleted, 0)
})

test('monthSummary 只统计当前月，并给出有内容的天数', () => {
  const summary = monthSummary(
    [
      entry('a', '2026-10-30'),
      entry('b', '2026-10-30'),
      entry('c', '2026-10-31'),
      entry('d', '2026-11-01'),
    ],
    '2026-10',
  )
  assert.equal(summary.total, 3, '11 月的条目不计入 10 月')
  assert.equal(summary.days, 2)
})

test('monthSummary 空月份返回零', () => {
  assert.deepEqual(monthSummary([], '2026-10'), { total: 0, days: 0 })
})
