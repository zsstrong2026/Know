import { test } from 'node:test'
import assert from 'node:assert/strict'

import { applyDatabasePatch, buildDatabaseContext } from './database-ai'
import type { ResolvedPatch } from '../ai/database-patch'

/** 最小可用的 dataSource 桩：只实现本模块用到的读写方法。 */
function fakeDataSource(columns: Array<{ id: string; type: string; name: string }>) {
  const names = new Map(columns.map((column) => [column.id, column.name]))
  const types = new Map(columns.map((column) => [column.id, column.type]))
  const data = new Map<string, Record<string, unknown>>()
  const values = new Map<string, unknown>()
  const state = {
    columns: columns.map((column) => ({ id: column.id, type: column.type, name: column.name })),
    addedRows: [] as string[],
    rowCounter: 0,
  }
  const dataSource = {
    readonly$: { value: false },
    rows$: {
      value: ['r1', 'r2'],
    },
    propertyTypeGet: (id: string) => types.get(id),
    propertyNameGet: (id: string) => names.get(id) ?? '',
    propertyDataGet: (id: string) => data.get(id) ?? {},
    propertyDataSet: (id: string, next: Record<string, unknown>) => {
      data.set(id, next)
    },
    propertyNameSet: (id: string, name: string) => {
      names.set(id, name)
    },
    propertyMetaGet: () => ({
      config: { rawValue: { fromJson: ({ value }: { value: string }) => ({ text: value }) } },
    }),
    propertyAdd: (_position: string, ops: { type?: string; name?: string }) => {
      const id = `c-new-${state.columns.length}`
      state.columns.push({ id, type: ops.type ?? 'rich-text', name: '' })
      names.set(id, '')
      types.set(id, ops.type ?? 'rich-text')
      return id
    },
    rowAdd: () => {
      state.rowCounter += 1
      const id = `r-new-${state.rowCounter}`
      state.addedRows.push(id)
      return id
    },
    cellValueChange: (rowId: string, columnId: string, value: unknown) => {
      values.set(`${rowId}:${columnId}`, value)
    },
    cellValueGet: (rowId: string, columnId: string) => {
      const key = `${rowId}:${columnId}`
      if (values.has(key)) return values.get(key)
      // 标题列返回 Y.Text 风格对象，其余返回空
      return columnId === 'c-title' ? { toString: () => (rowId === 'r1' ? '登录页改版' : '') } : undefined
    },
  }
  return { dataSource, state, values }
}

const model = {
  props: {
    columns$: {
      value: [{ id: 'c-title' }, { id: 'c-status' }],
    },
  },
}

/** 按给定列造 model：columnsFromModel 只读 props.columns$。 */
function modelOf(columns: Array<{ id: string }>) {
  return { props: { columns$: { value: columns.map((column) => ({ id: column.id })) } } }
}

test('上下文采样：select 反查选项名，空值不占位', () => {
  const { dataSource } = fakeDataSource([
    { id: 'c-title', type: 'title', name: '事项' },
    { id: 'c-status', type: 'select', name: '状态' },
    { id: 'c-date', type: 'date', name: '截止日' },
  ])
  dataSource.propertyDataSet = () => {}
  const withOptions = {
    ...dataSource,
    propertyDataGet: (id: string) =>
      id === 'c-status' ? { options: [{ id: 'opt-1', value: '进行中' }] } : {},
    cellValueGet: (rowId: string, columnId: string) => {
      if (columnId === 'c-title') return { toString: () => (rowId === 'r1' ? '登录页改版' : '') }
      if (columnId === 'c-status') return rowId === 'r1' ? 'opt-1' : undefined
      return undefined
    },
  }
  const context = buildDatabaseContext(modelOf([
    { id: 'c-title' },
    { id: 'c-status' },
    { id: 'c-date' },
  ]), withOptions)
  assert.equal(context.columns.length, 3)
  assert.equal(context.rows.length, 2)
  assert.equal(context.rows[0]?.cells['c-status'], '进行中', '选项 id 必须换成选项名')
  assert.equal('c-date' in (context.rows[0]?.cells ?? {}), false, '空单元格不进上下文')
  assert.equal(context.truncated, false)
})

test('上下文采样：超过上限时截断并标记', () => {
  const { dataSource } = fakeDataSource([
    { id: 'c-title', type: 'title', name: '事项' },
  ])
  const context = buildDatabaseContext(model, dataSource, 1)
  assert.equal(context.rows.length, 1)
  assert.equal(context.truncated, true)
})

test('写入补丁：新建列后按列名写行，且只在一个事务里执行', () => {
  const { dataSource, state, values } = fakeDataSource([
    { id: 'c-title', type: 'title', name: '事项' },
    { id: 'c-status', type: 'select', name: '状态' },
  ])
  let transacts = 0
  const store = {
    transact: (fn: () => void) => {
      transacts += 1
      fn()
    },
    getModelById: () => ({ text: { replace: () => undefined, length: 0 } }),
  }
  const patch: ResolvedPatch = {
    errors: [],
    overwriteCount: 0,
    ops: [
      { kind: 'add_property', name: '风险等级', type: 'select', options: ['高', '低'] },
      {
        kind: 'add_row',
        title: '新需求',
        cells: [{ columnId: '', columnName: '风险等级', type: 'select', value: '高' }],
      },
    ],
  }
  const outcome = applyDatabasePatch({
    store,
    dataSource,
    model,
    patch,
    allowOverwrite: false,
  })
  assert.equal(transacts, 1, '整包必须在同一个事务里')
  assert.deepEqual(outcome, { columns: 1, rows: 1, cells: 1, rowIds: ['r-new-1'] })
  assert.equal(state.addedRows.length, 1)
  const newColumn = state.columns.at(-1)!
  assert.equal(newColumn.type, 'select')
  // 新列选项包含「高」；单元格值写的是选项 id。
  const options = dataSource.propertyDataGet(newColumn.id) as { options: Array<{ id: string; value: string }> }
  assert.deepEqual(options.options.map((option) => option.value), ['高', '低'])
  assert.equal(values.get(`r-new-1:${newColumn.id}`), options.options[0]?.id)
})

test('覆盖保护：未勾选允许覆盖时跳过非空单元格', () => {
  const { dataSource, values } = fakeDataSource([
    { id: 'c-title', type: 'title', name: '事项' },
    { id: 'c-status', type: 'select', name: '状态' },
  ])
  const store = { transact: (fn: () => void) => fn(), getModelById: () => null }
  const patch: ResolvedPatch = {
    errors: [],
    overwriteCount: 1,
    ops: [
      {
        kind: 'set_cell',
        rowId: 'r1',
        rowTitle: '登录页改版',
        columnId: 'c-status',
        columnName: '状态',
        value: '已完成',
        overwrite: true,
        oldValue: '进行中',
      },
    ],
  }
  const skipped = applyDatabasePatch({ store, dataSource, model, patch, allowOverwrite: false })
  assert.deepEqual(skipped, { columns: 0, rows: 0, cells: 0, rowIds: [] })
  assert.equal(values.size, 0)

  const applied = applyDatabasePatch({ store, dataSource, model, patch, allowOverwrite: true })
  assert.equal(applied.cells, 1)
})

test('日期与数字按类型转换，富文本走 fromJson', () => {
  const { dataSource, values } = fakeDataSource([
    { id: 'c-title', type: 'title', name: '事项' },
    { id: 'c-date', type: 'date', name: '截止日' },
    { id: 'c-num', type: 'number', name: '工时' },
    { id: 'c-owner', type: 'text', name: '负责人' },
  ])
  const store = { transact: (fn: () => void) => fn(), getModelById: () => null }
  const patch: ResolvedPatch = {
    errors: [],
    overwriteCount: 0,
    ops: [
      {
        kind: 'add_row',
        title: 'A',
        cells: [
          { columnId: 'c-date', columnName: '截止日', type: 'date', value: '2026-10-30' },
          { columnId: 'c-num', columnName: '工时', type: 'number', value: '3.5' },
          { columnId: 'c-owner', columnName: '负责人', type: 'rich-text', value: '张三' },
        ],
      },
    ],
  }
  applyDatabasePatch({ store, dataSource, model, patch, allowOverwrite: false })
  assert.equal(values.get('r-new-1:c-date'), new Date(2026, 9, 30).getTime())
  assert.equal(values.get('r-new-1:c-num'), 3.5)
  assert.deepEqual(values.get('r-new-1:c-owner'), { text: '张三' })
})

test('来源列：AI 建行写「AI 识别」，列缺失时静默跳过', () => {
  const { dataSource, values } = fakeDataSource([
    { id: 'c-title', type: 'title', name: '事项' },
    { id: 'wb-col-origin', type: 'select', name: '来源' },
  ])
  dataSource.propertyDataSet('wb-col-origin', {
    options: [
      { id: 'wb-src-manual', value: '手动录入' },
      { id: 'wb-src-ai', value: 'AI 识别' },
      { id: 'wb-src-archive', value: '文档归档' },
    ],
  })
  const store = { transact: (fn: () => void) => fn(), getModelById: () => null }
  const patch: ResolvedPatch = {
    errors: [],
    overwriteCount: 0,
    ops: [{ kind: 'add_row', title: 'AI 新行', cells: [] }],
  }
  applyDatabasePatch({ store, dataSource, model, patch, allowOverwrite: false })
  assert.equal(values.get('r-new-1:wb-col-origin'), 'wb-src-ai')

  // 来源列没有 options（老条目未补列）时不写入，也不抛错
  const plain = fakeDataSource([{ id: 'c-title', type: 'title', name: '事项' }])
  applyDatabasePatch({
    store,
    dataSource: plain.dataSource,
    model,
    patch,
    allowOverwrite: false,
  })
  assert.equal(plain.values.size, 0)
})
