import { test } from 'node:test'
import assert from 'node:assert/strict'

import {
  currentMonthCursor,
  dateOnlyToTimestamp,
  diffDateOnly,
  formatLocalDate,
  parseDateOnly,
  shiftDateOnly,
  shiftMonthCursor,
  timestampToDateOnly,
} from './database-dates'

test('formatLocalDate / timestampToDateOnly 用本地日历日，不偏一天', () => {
  // 东八区 2026-10-30 23:30 的本地时间戳
  const lateNight = new Date(2026, 9, 30, 23, 30).getTime()
  assert.equal(timestampToDateOnly(lateNight), '2026-10-30')
  assert.equal(formatLocalDate(new Date(2026, 0, 1)), '2026-01-01')
  assert.equal(timestampToDateOnly(null), '')
  assert.equal(timestampToDateOnly(Number.NaN), '')
})

test('parseDateOnly：字符串前缀 / 数字时间戳 / 非法值', () => {
  assert.equal(parseDateOnly('2026-10-30'), '2026-10-30')
  // 带时间的 ISO 字符串只取日期部分，不做时区换算
  assert.equal(parseDateOnly('2026-10-30T23:30:00+08:00'), '2026-10-30')
  assert.equal(parseDateOnly(new Date(2026, 9, 30).getTime()), '2026-10-30')
  assert.equal(parseDateOnly('not-a-date'), null)
  assert.equal(parseDateOnly(undefined), null)
  // 格式正确但日历上不存在的日期视为非法
  assert.equal(parseDateOnly('2026-02-31'), null)
})

test('dateOnlyToTimestamp 用本地 0 点，不是 UTC 0 点', () => {
  const ts = dateOnlyToTimestamp('2026-10-30')
  assert.ok(ts !== null)
  const date = new Date(ts)
  assert.equal(date.getFullYear(), 2026)
  assert.equal(date.getMonth(), 9)
  assert.equal(date.getDate(), 30)
  assert.equal(date.getHours(), 0)
})

test('shiftDateOnly：跨月 / 闰年 / 跨年', () => {
  assert.equal(shiftDateOnly('2026-10-31', 1), '2026-11-01')
  assert.equal(shiftDateOnly('2026-03-01', -1), '2026-02-28')
  assert.equal(shiftDateOnly('2028-03-01', -1), '2028-02-29')
  assert.equal(shiftDateOnly('2026-12-31', 1), '2027-01-01')
  // 非法输入原样返回，不抛
  assert.equal(shiftDateOnly('2026-13-01', 1), '2026-13-01')
})

test('diffDateOnly：保持天数差，跨夏令时也不吃 ±1 天', () => {
  assert.equal(diffDateOnly('2026-10-30', '2026-11-02'), 3)
  assert.equal(diffDateOnly('2026-11-02', '2026-10-30'), -3)
  // 北美 DST 切换日（2026-11-01）前后仍是自然日差
  assert.equal(diffDateOnly('2026-10-31', '2026-11-02'), 2)
  assert.equal(diffDateOnly('2026-03-07', '2026-03-09'), 2)
  assert.equal(diffDateOnly('bad', '2026-11-02'), null)
})

test('shiftMonthCursor / currentMonthCursor', () => {
  assert.equal(shiftMonthCursor('2026-01', -1), '2025-12')
  assert.equal(shiftMonthCursor('2026-12', 1), '2027-01')
  assert.equal(shiftMonthCursor('2026-10', 0), '2026-10')
  assert.equal(currentMonthCursor(new Date(2026, 9, 30)), '2026-10')
})
