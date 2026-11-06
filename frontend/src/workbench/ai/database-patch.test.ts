import { test } from 'node:test'
import assert from 'node:assert/strict'

import {
  normalizeColumnType,
  resolvePatch,
  summarizePatch,
  validatePatch,
  type AIColumn,
  type AIPatch,
  type AIRow,
} from './database-patch'

const columns: AIColumn[] = [
  { id: 'c-title', name: '事项', type: 'title' },
  { id: 'c-status', name: '状态', type: 'select' },
  { id: 'c-owner', name: '负责人', type: 'text' },
]

const rows: AIRow[] = [
  { id: 'r1', title: '登录页改版', cells: { 'c-status': '进行中' } },
  { id: 'r2', title: '权限文案', cells: {} },
]

test('校验补丁外壳：空包与超量直接拦下', () => {
  assert.deepEqual(validatePatch(null), ['patch-missing'])
  assert.deepEqual(validatePatch({ ops: [] }), ['patch-empty'])
  assert.deepEqual(validatePatch({ ops: [{}] }), [])
  const tooMany = { ops: Array.from({ length: 201 }, () => ({ op: 'add_row' })) }
  assert.deepEqual(validatePatch(tooMany), ['patch-too-many-ops'])
})

test('解析三类 op：新建列、新建行、写单元格', () => {
  const patch: AIPatch = {
    ops: [
      { op: 'add_property', name: '风险等级', type: 'select', options: ['高', '低'] },
      { op: 'add_row', title: '新需求', cells: { 状态: '待开始', 负责人: '张三' } },
      { op: 'set_cell', row_id: 'r2', column: '负责人', value: '李四' },
    ],
  }
  const resolved = resolvePatch(patch, { columns, rows })
  assert.deepEqual(resolved.errors, [])
  assert.equal(resolved.ops.length, 3)
  // 单元格数包含新建行里写的两格 + set_cell 一格
  assert.deepEqual(summarizePatch(resolved), { columns: 1, rows: 1, cells: 3 })

  const [addColumn, addRow, setCell] = resolved.ops
  assert.equal(addColumn.kind, 'add_property')
  assert.equal((addColumn as { type: string }).type, 'select')
  assert.equal(addRow.kind, 'add_row')
  assert.equal((addRow as { cells: unknown[] }).cells.length, 2)
  assert.equal(setCell.kind, 'set_cell')
  assert.equal((setCell as { overwrite: boolean }).overwrite, false, '空单元格不算覆盖')
})

test('非空单元格被覆盖时计数并要求确认', () => {
  const resolved = resolvePatch(
    { ops: [{ op: 'set_cell', row_id: 'r1', column: '状态', value: '已完成' }] },
    { columns, rows },
  )
  assert.equal(resolved.overwriteCount, 1)
  const op = resolved.ops[0] as { overwrite: boolean; oldValue: string }
  assert.equal(op.overwrite, true)
  assert.equal(op.oldValue, '进行中')
})

test('新建列可被同一包内的后续 op 按列名引用', () => {
  const resolved = resolvePatch(
    {
      ops: [
        { op: 'add_property', name: '风险等级', type: 'select', options: ['高'] },
        { op: 'add_row', title: 'X', cells: { 风险等级: '高' } },
      ],
    },
    { columns, rows },
  )
  assert.deepEqual(resolved.errors, [])
  const row = resolved.ops[1] as { cells: Array<{ columnName: string; value: string }> }
  assert.equal(row.cells[0]?.columnName, '风险等级')
  assert.equal(row.cells[0]?.value, '高')
})

test('无效 op 全部报错且不写半截计划', () => {
  const cases: Array<[string, AIPatch]> = [
    ['未知 op', { ops: [{ op: 'delete_row' } as never] }],
    ['列名缺失', { ops: [{ op: 'add_property', name: '  ', type: 'text' }] }],
    ['列类型不支持', { ops: [{ op: 'add_property', name: '附件', type: 'attachment' }] }],
    ['列重名', { ops: [{ op: 'add_property', name: '状态', type: 'select', options: ['a'] }] }],
    ['select 缺选项', { ops: [{ op: 'add_property', name: '等级', type: 'select' }] }],
    ['行不存在', { ops: [{ op: 'set_cell', row_id: 'r9', column: '负责人', value: 'x' }] }],
    ['列不存在', { ops: [{ op: 'set_cell', row_id: 'r1', column: '不存在', value: 'x' }] }],
  ]
  for (const [name, patch] of cases) {
    const resolved = resolvePatch(patch, { columns, rows })
    assert.ok(resolved.errors.length > 0, `${name} 应当报错`)
  }
})

test('set_cell 缺 row_id 时按行不存在处理，不会写到别的行', () => {
  const resolved = resolvePatch(
    { ops: [{ op: 'set_cell', row_id: '', column: '负责人', value: 'x' }] },
    { columns, rows },
  )
  assert.deepEqual(resolved.ops, [])
  assert.ok(resolved.errors.includes('ops[0]:row-not-found'))
})

test('新建行/列超过上限时不再累积，错误明确', () => {
  const manyRows: AIPatch = {
    ops: Array.from({ length: 101 }, () => ({ op: 'add_row' as const, title: 'x' })),
  }
  const resolved = resolvePatch(manyRows, { columns, rows })
  assert.equal(resolved.ops.length, 100)
  assert.ok(resolved.errors.includes('ops[100]:too-many-rows'))
})

test('列类型归一化：text/string 归为 rich-text', () => {
  assert.equal(normalizeColumnType('text'), 'rich-text')
  assert.equal(normalizeColumnType('string'), 'rich-text')
  assert.equal(normalizeColumnType('MultiSelect'), 'multi-select')
  assert.equal(normalizeColumnType('progress'), null)
})

test('空值不写入：避免把「没填」变成空字符串', () => {
  const resolved = resolvePatch(
    { ops: [{ op: 'add_row', title: 'A', cells: { 负责人: '   ' } }] },
    { columns, rows },
  )
  assert.deepEqual(resolved.errors, [])
  const row = resolved.ops[0] as { cells: unknown[] }
  assert.equal(row.cells.length, 0)
})
