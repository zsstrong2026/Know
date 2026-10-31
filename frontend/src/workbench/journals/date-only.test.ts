import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  addDays,
  currentMonth,
  formatJournalTitle,
  monthGrid,
  monthRange,
  shiftMonth,
  todayISO,
  weekDays,
  weekStart,
  weekdayIndex,
} from './date-only.ts'

test('monthRange 返回首末日，月末按日历计算', () => {
  assert.deepEqual(monthRange('2026-02'), { start: '2026-02-01', end: '2026-02-28' })
  assert.deepEqual(monthRange('2024-02'), { start: '2024-02-01', end: '2024-02-29' }, '闰年 2 月是 29 天')
  assert.deepEqual(monthRange('2026-10'), { start: '2026-10-01', end: '2026-10-31' })
  assert.deepEqual(monthRange('2026-04'), { start: '2026-04-01', end: '2026-04-30' })
})

test('monthRange 拒绝非法格式与非法月份', () => {
  for (const value of ['2026-13', '2026-00', '202610', '2026-1', 'abc']) {
    assert.equal(monthRange(value), null, `${value} 应被拒绝`)
  }
})

test('shiftMonth 跨年正确，负数回退', () => {
  assert.equal(shiftMonth('2026-10', 1), '2026-11')
  assert.equal(shiftMonth('2026-12', 1), '2027-01')
  assert.equal(shiftMonth('2026-01', -1), '2025-12')
  assert.equal(shiftMonth('2026-10', -12), '2025-10')
  assert.equal(shiftMonth('2026-10', 0), '2026-10')
})

test('monthGrid 固定 42 格，且从周日开始', () => {
  const cells = monthGrid('2026-10')
  assert.equal(cells.length, 42)
  // 2026-10-01 是周四 → 第一格应是 2026-09-27（周日）
  assert.equal(cells[0].date, '2026-09-27')
  assert.equal(cells[0].inMonth, false)
  const firstInMonth = cells.find((cell) => cell.inMonth)
  assert.equal(firstInMonth?.date, '2026-10-01')
  // 最后一天 10-31 在格子里
  assert.ok(cells.some((cell) => cell.date === '2026-10-31' && cell.inMonth))
})

test('monthGrid 含上下月补白标记', () => {
  const cells = monthGrid('2026-02')
  assert.equal(cells.filter((cell) => cell.inMonth).length, 28, '2026 年 2 月 28 天')
  // 2026-02-01 恰好是周日，本月第一格就是首日 → 没有上月补白，但一定有下月补白
  assert.equal(cells[0].date, '2026-02-01')
  assert.ok(cells.some((cell) => !cell.inMonth && cell.date > '2026-02-28'))

  const october = monthGrid('2026-10')
  assert.ok(october.some((cell) => !cell.inMonth && cell.date < '2026-10-01'), '10 月有上月补白')
  assert.ok(october.some((cell) => !cell.inMonth && cell.date > '2026-10-31'))
})

test('todayISO / currentMonth 使用本地日期，不叠加时区', () => {
  const fixed = new Date(2026, 9, 30, 23, 30) // 本地 10-30 23:30
  assert.equal(todayISO(fixed), '2026-10-30')
  assert.equal(currentMonth(fixed), '2026-10')
})

test('monthGrid 对非法月份返回空数组', () => {
  assert.deepEqual(monthGrid('bad'), [])
})

test('addDays 跨月跨年与闰年正确', () => {
  assert.equal(addDays('2026-10-31', 1), '2026-11-01')
  assert.equal(addDays('2026-12-31', 1), '2027-01-01')
  assert.equal(addDays('2024-02-28', 1), '2024-02-29', '闰年 2 月有 29 日')
  assert.equal(addDays('2026-03-01', -1), '2026-02-28')
  assert.equal(addDays('2026-10-30', 0), '2026-10-30')
  assert.equal(addDays('bad', 1), 'bad', '非法日期原样返回')
})

test('weekdayIndex / weekStart 以周日为一周首日', () => {
  // 2026-10-25 是周日
  assert.equal(weekdayIndex('2026-10-25'), 0)
  assert.equal(weekStart('2026-10-25'), '2026-10-25')
  // 2026-10-30 是周五
  assert.equal(weekdayIndex('2026-10-30'), 5)
  assert.equal(weekStart('2026-10-30'), '2026-10-25')
})

test('weekDays 固定返回七天且首尾跨月', () => {
  const days = weekDays('2026-10-30')
  assert.equal(days.length, 7)
  assert.equal(days[0], '2026-10-25')
  assert.equal(days[6], '2026-10-31')
  // 跨月周：10-31 到 11-01 在同一自然周
  const crossMonth = weekDays('2026-11-01')
  assert.equal(crossMonth[0], '2026-11-01')
  assert.equal(crossMonth[6], '2026-11-07')
})

test('formatJournalTitle 输出“YYYY 年 M 月 D 日”', () => {
  assert.equal(formatJournalTitle('2026-10-30'), '2026 年 10 月 30 日')
  assert.equal(formatJournalTitle('2026-01-05'), '2026 年 1 月 5 日', '年月日不补零')
  assert.equal(formatJournalTitle('bad'), 'bad')
})

