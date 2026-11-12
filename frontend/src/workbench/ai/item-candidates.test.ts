import { test } from 'node:test'
import assert from 'node:assert/strict'

import {
  buildExistingFingerprints,
  buildItemPatch,
  candidateFingerprint,
  dedupeCandidates,
  normalizeTitle,
  type ItemWriteSchema,
} from './item-candidates'

const SCHEMA: ItemWriteSchema = {
  statusColumnId: 'c-status',
  statusColumnName: '状态',
  statusTodo: '待开始',
  statusDone: '已完成',
  priorityColumnId: 'c-priority',
  priorityColumnName: '优先级',
  ownerColumnId: 'c-owner',
  ownerColumnName: '负责人',
  dateColumnId: 'c-date',
  dateColumnName: '截止日',
  sourceColumnId: 'c-origin',
  sourceColumnName: '来源',
  sourceLabel: 'AI 识别',
  sourceDocColumnId: 'c-origin-doc',
  sourceDocColumnName: '来源文档',
  sourceDocTitle: '季度计划',
}

test('normalizeTitle：去掉列表符号、压缩空白、去尾标点', () => {
  assert.equal(normalizeTitle('  - 补充  部署文档。 '), '补充 部署文档')
  assert.equal(normalizeTitle('1. 完成评审；'), '完成评审')
  assert.equal(normalizeTitle(''), '')
})

test('candidateFingerprint：标题小写 + 截止日', () => {
  assert.equal(
    candidateFingerprint({ title: 'Deploy Docs', due: '2026-11-06' }),
    'deploy docs|2026-11-06',
  )
  // 同标题不同截止日视为两条不同的安排
  assert.notEqual(
    candidateFingerprint({ title: '部署文档', due: '2026-11-06' }),
    candidateFingerprint({ title: '部署文档', due: '2026-11-07' }),
  )
})

test('dedupeCandidates：批内去重 + 与已有行去重 + 丢弃空标题', () => {
  const existing = buildExistingFingerprints(
    [{ title: '补充部署文档', cells: { 'c-date': '2026-11-06' } }],
    'c-date',
  )
  const { kept, skipped } = dedupeCandidates(
    [
      { title: '补充部署文档', due: '2026-11-06' },
      { title: '补充部署文档', due: '2026-11-06' },
      { title: '   ' },
      { title: '完成评审', due: '2026-11-07' },
      { title: '完成评审', due: '2026-11-07' },
    ],
    existing,
  )
  assert.deepEqual(
    kept.map((item) => item.title),
    ['完成评审'],
  )
  assert.equal(skipped, 4)
})

test('buildItemPatch：状态缺省补待开始，空字段不写', () => {
  const patch = buildItemPatch([{ title: '补充部署文档' }, { title: '完成评审', done: true }], SCHEMA)
  assert.equal(patch.ops.length, 2)
  const [first, second] = patch.ops
  assert.ok(first.kind === 'add_row' && second.kind === 'add_row')
  const status = (op: typeof first) =>
    op.kind === 'add_row' ? op.cells.find((cell) => cell.columnId === 'c-status')?.value : undefined
  assert.equal(status(first), '待开始')
  assert.equal(status(second), '已完成', '正文待办已勾选时用已完成')
  // 没有负责人 / 截止日时不产生空单元格
  assert.equal(first.cells.some((cell) => cell.columnId === 'c-owner'), false)
})

test('buildItemPatch：写入来源与来源文档', () => {
  const patch = buildItemPatch([{ title: '补充部署文档', owner: '李昀', due: '2026-11-06' }], SCHEMA)
  const op = patch.ops[0]
  assert.ok(op.kind === 'add_row')
  const cells = Object.fromEntries(op.cells.map((cell) => [cell.columnId, cell.value]))
  assert.equal(cells['c-origin'], 'AI 识别')
  assert.equal(cells['c-origin-doc'], '季度计划')
  assert.equal(cells['c-owner'], '李昀')
  assert.equal(cells['c-date'], '2026-11-06')
})
