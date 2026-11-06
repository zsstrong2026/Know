import { test } from 'node:test'
import assert from 'node:assert/strict'

import {
  LEGACY_CARD_PROPERTIES,
  PROTOTYPE_CARD_PROPERTIES,
  PROTOTYPE_COLUMN_WIDTHS,
  SEED_COLUMN_WIDTH,
  planCardProperties,
  planColumnWidths,
} from './database-view-defaults'
import { WORKBENCH_DB_COLUMNS as C } from './database-columns'

const seedTable = () => [
  { id: C.title, width: SEED_COLUMN_WIDTH },
  { id: C.status, width: SEED_COLUMN_WIDTH },
  { id: C.owner, width: SEED_COLUMN_WIDTH },
  { id: C.date, width: SEED_COLUMN_WIDTH },
  { id: C.priority, width: SEED_COLUMN_WIDTH },
  { id: C.relation, width: SEED_COLUMN_WIDTH },
]

test('planColumnWidths：六列都还是 seed 值时，按原型比例重排', () => {
  const next = planColumnWidths(seedTable())
  assert.ok(next)
  assert.deepEqual(
    next.map((column) => column.width),
    [260, 104, 116, 128, 96, 152],
  )
  // 顺序不变：视图列顺序由视图数据决定，这里只改宽度
  assert.deepEqual(
    next.map((column) => column.id),
    seedTable().map((column) => column.id),
  )
})

test('planColumnWidths：用户调过任意一列就整体放弃', () => {
  const dragged = seedTable()
  dragged[2].width = 210
  assert.equal(planColumnWidths(dragged), null)
})

test('planColumnWidths：未知列保留 seed 宽度，不产生写入', () => {
  // 只有一列且不在原型表里 → 宽度不变 → null，避免每轮挂载都写一次视图数据
  assert.equal(planColumnWidths([{ id: 'custom-col', width: SEED_COLUMN_WIDTH }]), null)
  // 已知列 + 未知列混排：只改已知列
  const mixed = planColumnWidths([
    { id: C.title, width: SEED_COLUMN_WIDTH },
    { id: 'custom-col', width: SEED_COLUMN_WIDTH },
  ])
  assert.deepEqual(mixed, [
    { id: C.title, width: PROTOTYPE_COLUMN_WIDTHS[C.title] },
    { id: 'custom-col', width: SEED_COLUMN_WIDTH },
  ])
})

test('planColumnWidths：空列或缺少宽度时不动', () => {
  assert.equal(planColumnWidths([]), null)
  assert.equal(planColumnWidths([{ id: C.title }]), null)
})

test('planCardProperties：遗留 [状态, 负责人] 换成原型卡片', () => {
  assert.deepEqual(planCardProperties([...LEGACY_CARD_PROPERTIES]), [...PROTOTYPE_CARD_PROPERTIES])
  assert.deepEqual([...PROTOTYPE_CARD_PROPERTIES], [C.priority, C.date, C.owner])
})

test('planCardProperties：用户自定义过就保持原样', () => {
  assert.equal(planCardProperties([C.owner]), null)
  assert.equal(planCardProperties([C.status, C.owner, C.date]), null)
  assert.equal(planCardProperties([]), null)
})
