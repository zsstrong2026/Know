import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  batchOpFor,
  capBatchSelection,
  countBatchArchive,
  planBatchArchive,
  reasonFromErrorCode,
  summarizeBatchResults,
  type BatchArchiveCandidate,
  type BatchArchiveResult,
} from './batch-archive.ts'

const candidate = (
  key: string,
  status: BatchArchiveCandidate['status'],
  extra: Partial<BatchArchiveCandidate> = {},
): BatchArchiveCandidate => ({
  key,
  sourceType: 'item',
  sourceId: `id-${key}`,
  title: `标题 ${key}`,
  status,
  baseVersion: 3,
  ...extra,
})

test('七态映射到批量动作', () => {
  assert.equal(batchOpFor('none'), 'create')
  assert.equal(batchOpFor('stale'), 'update')
  assert.equal(batchOpFor('partial'), 'retry')
  assert.equal(batchOpFor('deleted'), 'recreate')
  assert.equal(batchOpFor('active'), 'skip')
  assert.equal(batchOpFor('processing'), 'skip')
  assert.equal(batchOpFor('source_deleted'), 'skip')
  assert.equal(batchOpFor(undefined), 'skip')
})

test('预检把选中项拆成可执行与将跳过', () => {
  const plan = planBatchArchive([
    candidate('a', 'none'),
    candidate('b', 'stale'),
    candidate('c', 'partial'),
    candidate('d', 'active'),
    candidate('e', 'processing'),
    candidate('f', 'source_deleted'),
  ])
  assert.deepEqual(
    plan.actionable.map((item) => item.op),
    ['create', 'update', 'retry'],
  )
  assert.deepEqual(
    plan.skipped.map((item) => item.key),
    ['d', 'e', 'f'],
  )
})

test('预检计数与总数', () => {
  const plan = planBatchArchive([
    candidate('a', 'none'),
    candidate('b', 'none'),
    candidate('c', 'stale'),
    candidate('d', 'deleted'),
    candidate('e', 'active'),
  ])
  assert.deepEqual(countBatchArchive(plan), {
    create: 2,
    update: 1,
    retry: 0,
    recreate: 1,
    skip: 1,
    total: 4,
  })
})

test('超过 20 条只保留前 20 并报告丢弃数', () => {
  const many = Array.from({ length: 25 }, (_, i) => candidate(`k${i}`, 'none'))
  const { kept, dropped } = capBatchSelection(many)
  assert.equal(kept.length, 20)
  assert.equal(dropped, 5)
  assert.equal(kept[0].key, 'k0', '按传入顺序保留')

  const small = capBatchSelection(many.slice(0, 3), 20)
  assert.equal(small.dropped, 0)
})

test('结果汇总区分成功/失败/跳过，并只收失败项供重试', () => {
  const results: BatchArchiveResult[] = [
    { key: 'a', title: 'A', status: 'archived' },
    { key: 'b', title: 'B', status: 'failed', reason: 'conflict' },
    { key: 'c', title: 'C', status: 'skipped', reason: 'up_to_date' },
    { key: 'd', title: 'D', status: 'failed', reason: 'too_large' },
  ]
  const summary = summarizeBatchResults(results)
  assert.equal(summary.archived, 1)
  assert.equal(summary.failed, 2)
  assert.equal(summary.skipped, 1)
  assert.deepEqual(
    summary.retryable.map((item) => item.key),
    ['b', 'd'],
  )
})

test('错误码映射到失败原因', () => {
  assert.equal(reasonFromErrorCode(2401), 'conflict')
  assert.equal(reasonFromErrorCode(2402), 'too_large')
  assert.equal(reasonFromErrorCode(2400), 'quota')
  assert.equal(reasonFromErrorCode(2405), 'changed')
  assert.equal(reasonFromErrorCode(1002), 'forbidden')
  assert.equal(reasonFromErrorCode(0), 'network')
  assert.equal(reasonFromErrorCode(500), 'network')
})

test('文档来源也参与同一套判定', () => {
  const plan = planBatchArchive([
    candidate('doc-1', 'none', { sourceType: 'doc' }),
    candidate('doc-2', 'stale', { sourceType: 'doc' }),
  ])
  assert.equal(plan.actionable.length, 2)
  assert.ok(plan.actionable.every((item) => item.sourceType === 'doc'))
})
