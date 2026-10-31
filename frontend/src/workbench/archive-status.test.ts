import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  archiveBadge,
  canDeleteArchive,
  canReArchive,
  dominantStatus,
  statusRank,
  worstStatus,
} from './archive-status.ts'

test('七态徽标的操作口径与后端状态机一致', () => {
  const cases: Array<[Parameters<typeof archiveBadge>[0], string]> = [
    ['none', 'create'],
    ['stale', 'update'],
    ['partial', 'retry'],
    ['deleted', 'recreate'],
    ['processing', 'progress'],
    ['active', 'none'],
    ['source_deleted', 'none'],
  ]
  for (const [status, action] of cases) {
    const badge = archiveBadge(status, 0)
    assert.equal(badge.action, action, `${status} 的主操作应为 ${action}`)
    assert.equal(badge.key, `workbench.archive.status.${status}`)
  }
})

test('徽标版本：0 与负数都视为“没有版本”', () => {
  assert.equal(archiveBadge('active', 0).version, 0)
  assert.equal(archiveBadge('active', -3).version, 0)
  assert.equal(archiveBadge('active', 7).version, 7)
})

test('状态缺失时按“未归档”处理，不抛出', () => {
  const badge = archiveBadge(undefined, undefined)
  assert.equal(badge.status, 'none')
  assert.equal(badge.action, 'create')
})

test('只有 stale / partial 允许重新归档', () => {
  assert.equal(canReArchive('stale'), true)
  assert.equal(canReArchive('partial'), true)
  for (const status of ['none', 'active', 'processing', 'deleted', 'source_deleted'] as const) {
    assert.equal(canReArchive(status), false, `${status} 不应允许重新归档`)
  }
})

test('只有存在 live 映射时才允许删除归档', () => {
  for (const status of ['active', 'stale', 'partial'] as const) {
    assert.equal(canDeleteArchive(status), true, `${status} 应允许删除归档`)
  }
  for (const status of ['none', 'deleted', 'processing', 'source_deleted'] as const) {
    assert.equal(canDeleteArchive(status), false, `${status} 不应允许删除归档`)
  }
})

test('状态优先级：source_deleted > partial > processing > stale > active > deleted > none', () => {
  const order = ['none', 'deleted', 'active', 'stale', 'processing', 'partial', 'source_deleted'] as const
  for (let i = 1; i < order.length; i += 1) {
    assert.ok(
      statusRank(order[i]) > statusRank(order[i - 1]),
      `${order[i]} 应优先于 ${order[i - 1]}`,
    )
  }
})

test('worstStatus 取更严重的那个，缺失按 none', () => {
  assert.equal(worstStatus('active', 'partial'), 'partial')
  assert.equal(worstStatus(undefined, 'stale'), 'stale')
  assert.equal(worstStatus('source_deleted', 'partial'), 'source_deleted')
  assert.equal(worstStatus(undefined, undefined), 'none')
})

test('dominantStatus 在无 state 时返回 none，否则沿用后端聚合结果', () => {
  assert.equal(dominantStatus(null), 'none')
  assert.equal(
    dominantStatus({
      source_type: 'item',
      source_id: 'x',
      current_version: 3,
      archived_version: 2,
      status: 'stale',
      kb_id: 'kb',
      parts: [],
      updated_at: null,
    }),
    'stale',
  )
})
