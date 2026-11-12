import { test } from 'node:test'
import assert from 'node:assert/strict'

import {
  LEGACY_CARD_PROPERTIES,
  PROTOTYPE_CARD_PROPERTIES,
  PROTOTYPE_COLUMN_WIDTHS,
  REQUIRED_VIEW_MODES,
  SEED_COLUMN_WIDTH,
  UNGROUPED_GROUP_KEY,
  planCardProperties,
  planColumnWidths,
  planKanbanUngroupVisible,
  planMissingViews,
  planTableGrouping,
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

test('planColumnWidths：给可视宽度时按原型比例铺满', () => {
  // 六主列基准宽度合计 856；可视宽度 1600 时把剩余空间按
  // 34/14/16/13/11/12 补给六主列，总和应铺满 1600。
  const next = planColumnWidths(seedTable(), 1600)
  assert.ok(next)
  const total = next.reduce((sum, column) => sum + column.width, 0)
  assert.equal(total, 1600)
  // 事项列仍是六主列里最宽的（原型 34%）。
  const title = next.find((column) => column.id === C.title)
  const status = next.find((column) => column.id === C.status)
  assert.ok(title && status && title.width > status.width * 2)
})

test('planColumnWidths：可视宽度不足时压到最小宽度（避免横向溢出）', () => {
  const next = planColumnWidths(seedTable(), 600)
  const widths = next?.map((column) => column.width) ?? []
  assert.deepEqual(widths, [204, 84, 96, 78, 72, 72])
  // 每列都不低于最小宽度：宁可轻微溢出，也不把字段名压成省略号。
  assert.ok(widths.every((width) => width >= 72))
})

test('planColumnWidths：已归一过的比例列宽会按新的可视宽度重排', () => {
  // 上一轮归一在 1410px 下写出的宽度（各列宽度/原型比例 ≈ 1006）。
  const normalized = [
    { id: C.title, width: 342 },
    { id: C.status, width: 141 },
    { id: C.owner, width: 161 },
    { id: C.date, width: 131 },
    { id: C.priority, width: 111 },
    { id: C.relation, width: 121 },
  ]
  const next = planColumnWidths(normalized, 930)
  assert.ok(next)
  assert.equal(
    next.reduce((sum, column) => sum + column.width, 0),
    930,
  )
})

test('planColumnWidths：六主列被压到最小宽度后，容器变宽仍能重新铺满', () => {
  // 首帧布局未展开时 fitToWidth 会把六列全部压成 72（最小宽度）。
  // 这种「我方归一过的全等 72」必须判定为可再归一，否则会永久定格在 72。
  const squashed = [
    { id: C.title, width: 72 },
    { id: C.status, width: 72 },
    { id: C.owner, width: 72 },
    { id: C.date, width: 72 },
    { id: C.priority, width: 72 },
    { id: C.relation, width: 72 },
  ]
  const next = planColumnWidths(squashed, 930)
  assert.ok(next)
  assert.equal(
    next.reduce((sum, column) => sum + column.width, 0),
    930,
  )
})

test('planColumnWidths：首帧窄容器留下的混合 clamp 态仍可重新铺满', () => {
  // 线上真实数据：容器只有约 574px 时按预算 ≈350 分配，标题 119、其余五列被压到 72。
  // 这类「部分列被截断」的状态也是我方归一的结果，不能被误判成用户拖动。
  const mixed = [
    { id: C.title, width: 119 },
    { id: C.status, width: 72 },
    { id: C.owner, width: 72 },
    { id: C.date, width: 72 },
    { id: C.priority, width: 72 },
    { id: C.relation, width: 72 },
  ]
  const next = planColumnWidths(mixed, 930)
  assert.ok(next)
  assert.equal(
    next.reduce((sum, column) => sum + column.width, 0),
    930,
  )
})

test('planTableGrouping：表格不应按状态分组', () => {
  assert.deepEqual(
    planTableGrouping({ columnId: C.status }),
    // 清空 columnId 即取消分组，看板的 groupBy 不受影响
    { type: 'groupBy', columnId: '', name: '' },
  )
  assert.equal(planTableGrouping(undefined), null)
  assert.equal(planTableGrouping({ columnId: '' }), null)
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

test('planKanbanUngroupVisible：清掉上一版写入的未分组隐藏标记', () => {
  assert.deepEqual(
    planKanbanUngroupVisible([{ key: UNGROUPED_GROUP_KEY, hide: true, manuallyCardSort: [] }]),
    [],
  )
})

test('planKanbanUngroupVisible：没有旧标记时不写入视图数据', () => {
  assert.equal(planKanbanUngroupVisible([]), null)
  assert.equal(
    planKanbanUngroupVisible([{ key: 'wb-opt-doing', hide: false, manuallyCardSort: ['r1'] }]),
    null,
  )
})

test('planKanbanUngroupVisible：保留其它分组的手动卡片排序', () => {
  const next = planKanbanUngroupVisible([
    { key: 'wb-opt-doing', hide: false, manuallyCardSort: ['r1'] },
    { key: UNGROUPED_GROUP_KEY, hide: true, manuallyCardSort: [] },
  ])
  assert.deepEqual(next, [{ key: 'wb-opt-doing', hide: false, manuallyCardSort: ['r1'] }])
})

test('planMissingViews：三种视图都在时不写入', () => {
  assert.equal(planMissingViews([...REQUIRED_VIEW_MODES]), null)
  // 顺序不影响判定
  assert.equal(planMissingViews(['calendar', 'table', 'kanban']), null)
})

test('planMissingViews：老条目缺日历时补日历', () => {
  // 组件实测里遇到的真实情况：只有看板与表格两个视图
  assert.deepEqual(planMissingViews(['kanban', 'table']), ['calendar'])
})

test('planMissingViews：只剩一个视图时按固定顺序补齐其余两个', () => {
  assert.deepEqual(planMissingViews(['table']), ['kanban', 'calendar'])
  assert.deepEqual(planMissingViews([]), ['table', 'kanban', 'calendar'])
})
