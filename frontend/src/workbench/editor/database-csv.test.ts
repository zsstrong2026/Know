import { test } from 'node:test'
import assert from 'node:assert/strict'

import {
  columnsFromModel,
  csvToPlan,
  databaseToCsv,
  guardFormula,
  parseCsv,
  rowsFromDataSource,
  unguardFormula,
  type CsvColumnLike,
} from './database-csv'

const columns: CsvColumnLike[] = [
  { id: 'c-title', type: 'title', name: '事项', data: {} },
  {
    id: 'c-status',
    type: 'select',
    name: '状态',
    data: { options: [{ id: 'opt-done', value: '已完成' }] },
  },
  { id: 'c-date', type: 'date', name: '截止日', data: {} },
  { id: 'c-num', type: 'number', name: '工时', data: {} },
  { id: 'c-flag', type: 'checkbox', name: '完成', data: {} },
]

const day = new Date(2026, 9, 30).getTime()

test('导出：日期是 ISO，select 反查选项名，checkbox 用 true/false', () => {
  const csv = databaseToCsv(columns, [
    {
      rowId: 'r1',
      title: '登录页改版',
      cells: { 'c-status': 'opt-done', 'c-date': day, 'c-num': 3, 'c-flag': true },
    },
  ])
  assert.ok(csv.startsWith('\ufeff'), '带 BOM，Excel 打开不乱码')
  const rows = parseCsv(csv)
  assert.deepEqual(rows[0], ['事项', '状态', '截止日', '工时', '完成'])
  assert.deepEqual(rows[1], ['登录页改版', '已完成', '2026-10-30', '3', 'true'])
})

test('导出：含逗号/引号/换行的单元格按 RFC 4180 转义', () => {
  const csv = databaseToCsv([{ id: 'c-title', type: 'title', name: '事项', data: {} }], [
    { rowId: 'r1', title: 'a,b', cells: {} },
    { rowId: 'r2', title: 'say "hi"', cells: {} },
    { rowId: 'r3', title: 'two\nlines', cells: {} },
  ])
  const rows = parseCsv(csv)
  assert.deepEqual(rows[1], ['a,b'])
  assert.deepEqual(rows[2], ['say "hi"'])
  assert.deepEqual(rows[3], ['two\nlines'])
})

test('公式注入防护：导出加前缀，导入剥前缀', () => {
  assert.equal(guardFormula('=SUM(A1)'), "'=SUM(A1)")
  assert.equal(guardFormula('+1'), "'+1")
  assert.equal(guardFormula('normal'), 'normal')
  assert.equal(unguardFormula("'=SUM(A1)"), '=SUM(A1)')
  // 用户自己写的普通引号不动
  assert.equal(unguardFormula("'quoted'"), "'quoted'")

  const csv = databaseToCsv([{ id: 'c-title', type: 'title', name: '事项', data: {} }], [
    { rowId: 'r1', title: '=cmd|calc', cells: {} },
  ])
  assert.match(csv, /'=cmd\|calc/)
  const plan = csvToPlan(csv, [{ id: 'c-title', type: 'title', name: '事项', data: {} }])
  assert.equal(plan.rows[0]?.title, '=cmd|calc')
})

test('导入：日期字符串转本地 0 点时间戳，number/checkbox 按类型转换', () => {
  const csv = '事项,状态,截止日,工时,完成\r\n登录页改版,已完成,2026-10-30,3.5,true\r\n'
  const plan = csvToPlan(csv, columns)
  assert.equal(plan.rows.length, 1)
  const row = plan.rows[0]!
  assert.equal(row.title, '登录页改版')
  assert.equal(row.cells['c-status'], 'opt-done')
  assert.equal(row.cells['c-date'], new Date(2026, 9, 30).getTime())
  assert.equal(row.cells['c-num'], 3.5)
  assert.equal(row.cells['c-flag'], true)
})

test('导入：select 出现新值时登记新选项，同批复用同一个 id', () => {
  const csv = '事项,状态\r\nA,阻塞\r\nB,阻塞\r\n'
  const plan = csvToPlan(csv, columns)
  const options = plan.columnData['c-status']?.options ?? []
  assert.equal(options.length, 2, '原有 1 个 + 新增 1 个')
  const added = options.find((option) => option.value === '阻塞')
  assert.ok(added)
  assert.equal(plan.rows[0]?.cells['c-status'], added!.id)
  assert.equal(plan.rows[1]?.cells['c-status'], added!.id)
})

test('导入：表头里没见过的列记为新增列，值进 extra 不丢', () => {
  const csv = '事项,备注\r\nA,随手记\r\n'
  const plan = csvToPlan(csv, columns)
  assert.deepEqual(plan.newColumns, ['备注'])
  assert.equal(plan.rows[0]?.extra['备注'], '随手记')
  assert.equal(plan.rows[0]?.title, 'A')
})

test('导入：整行为空计入 skipped；没有标题列时用第一个非空单元格兜底', () => {
  const csv = '工时,完成\r\n3,true\r\n,\r\n'
  const plan = csvToPlan(csv, columns)
  assert.equal(plan.skipped, 1)
  assert.equal(plan.rows.length, 1)
  assert.equal(plan.rows[0]?.title, '3')
  assert.equal(plan.rows[0]?.cells['c-num'], 3)
})

test('往返不丢列：导出再导入，列与值都不变', () => {
  const rows = [
    {
      rowId: 'r1',
      title: '登录页改版',
      cells: { 'c-status': 'opt-done', 'c-date': day, 'c-num': 3, 'c-flag': true },
    },
    { rowId: 'r2', title: '权限文案', cells: { 'c-status': 'opt-done', 'c-flag': false } },
  ]
  const csv = databaseToCsv(columns, rows)
  const plan = csvToPlan(csv, columns)
  assert.deepEqual(plan.newColumns, [], '往返不应产生新列')
  assert.equal(plan.rows.length, 2)
  assert.equal(plan.rows[0]?.cells['c-date'], day)
  assert.equal(plan.rows[1]?.cells['c-flag'], false)
  // 空单元格不写入：避免把「没填」变成 0 / false
  assert.equal('c-num' in (plan.rows[1]?.cells ?? {}), false)
})

test('parseCsv：CRLF / LF 混用，引号内的逗号与换行保留', () => {
  const rows = parseCsv('a,b\r\n"x,y",z\n"multi\nline",w\n')
  assert.deepEqual(rows, [
    ['a', 'b'],
    ['x,y', 'z'],
    ['multi\nline', 'w'],
  ])
})

test('columnsFromModel 只取 props.columns，跳过 title/type 这类固定属性', () => {
  const model = {
    props: {
      // properties$ 的顺序：固定属性在前，用户列在后
      columns$: { value: [{ id: 'c-status' }, { id: 'c-date' }] },
    },
  }
  const dataSource = {
    propertyTypeGet: (id: string) => (id === 'c-status' ? 'select' : 'date'),
    propertyNameGet: (id: string) => (id === 'c-status' ? '状态' : '截止日'),
    propertyDataGet: () => ({ options: [] }),
  }
  const result = columnsFromModel(model, dataSource)
  assert.deepEqual(
    result.map((column) => [column.id, column.type, column.name]),
    [
      ['c-status', 'select', '状态'],
      ['c-date', 'date', '截止日'],
    ],
  )
})

test('rowsFromDataSource 跳过标题列，标题取行块文本', () => {
  const dataSource = {
    rows$: { value: ['r1', 'r2'] },
    cellValueGet: (rowId: string, columnId: string) => {
      if (columnId === 'c-title') return { toString: () => (rowId === 'r1' ? '登录页改版' : '') }
      if (columnId === 'c-status') return 'opt-done'
      return undefined
    },
  }
  const rows = rowsFromDataSource(dataSource, columns)
  assert.equal(rows[0]?.title, '登录页改版')
  assert.equal(rows[1]?.title, '')
  assert.deepEqual(rows[0]?.cells, { 'c-status': 'opt-done', 'c-date': undefined, 'c-num': undefined, 'c-flag': undefined })
})

test('富文本单元格导出为纯文本，不输出 [object Object]', () => {
  const richColumns: CsvColumnLike[] = [
    { id: 'c-title', type: 'title', name: '事项', data: {} },
    { id: 'c-note', type: 'rich-text', name: '备注', data: {} },
  ]
  const csv = databaseToCsv(richColumns, [
    { rowId: 'r1', title: 'A', cells: { 'c-note': { toString: () => '第一行' } } },
  ])
  const rows = parseCsv(csv)
  assert.deepEqual(rows[1], ['A', '第一行'])
})
