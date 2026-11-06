import { test } from 'node:test'
import assert from 'node:assert/strict'

import { EDGE_FLIP_THRESHOLD, edgeFlipDirection } from './database-calendar'

const rect = { left: 100, right: 900 }

test('边缘判定：左侧翻上一月，右侧翻下一月', () => {
  assert.equal(edgeFlipDirection(rect.left + 1, rect), -1)
  assert.equal(edgeFlipDirection(rect.left + EDGE_FLIP_THRESHOLD, rect), -1)
  assert.equal(edgeFlipDirection(rect.right - 1, rect), 1)
  assert.equal(edgeFlipDirection(rect.right - EDGE_FLIP_THRESHOLD, rect), 1)
})

test('中间区域不翻月，避免正常拖拽被误判', () => {
  assert.equal(edgeFlipDirection((rect.left + rect.right) / 2, rect), 0)
  assert.equal(edgeFlipDirection(rect.left + EDGE_FLIP_THRESHOLD + 1, rect), 0)
})

test('网格太窄时完全不翻月（阈值占满整格）', () => {
  const narrow = { left: 0, right: EDGE_FLIP_THRESHOLD * 2 }
  assert.equal(edgeFlipDirection(0, narrow), 0)
  assert.equal(edgeFlipDirection(EDGE_FLIP_THRESHOLD * 2, narrow), 0)
})
