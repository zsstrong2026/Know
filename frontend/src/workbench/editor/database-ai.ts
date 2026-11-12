/**
 * 数据库 AI 的上下文采样与补丁写入（P3-5）。
 *
 * 与 `ai/database-patch.ts` 的分工：
 *   - 那边是纯逻辑（校验 + 解析），可在 `tsx --test` 里跑；
 *   - 这里持有 `dataSource` / `store`，负责真正的读写，必须在编辑器挂载后调用。
 *
 * 写入使用一个 `store.transact`：任一步抛错整包回滚，避免出现“建了列但没写行”
 * 的半截状态（§11.7 要求一次性写入）。写入后走正常保存链路，AI 不持有独立写连接。
 */
import type { AIColumn, AIRow, AIColumnType, ResolvedPatch } from '../ai/database-patch'
import { AI_CONTEXT_ROW_LIMIT } from '../ai/database-patch'
import { columnsFromModel, rowsFromDataSource, type CsvColumnLike } from './database-csv'
import { dateOnlyToTimestamp, parseDateOnly, timestampToDateOnly } from './database-dates'
import { WORKBENCH_DB_COLUMNS, WORKBENCH_SOURCE_OPTIONS } from './database-columns'

/** 新建选项时循环使用的颜色；与 seed 里的标签色一致。 */
const OPTION_COLORS = [
  'var(--affine-tag-gray)',
  'var(--affine-tag-blue)',
  'var(--affine-tag-green)',
  'var(--affine-tag-orange)',
  'var(--affine-tag-red)',
]

export interface DatabaseAIContext {
  columns: AIColumn[]
  rows: AIRow[]
  /** 行数超过上限时为 true，提示模型“这不是全部数据” */
  truncated: boolean
}

export interface DatabasePatchOutcome {
  columns: number
  rows: number
  cells: number
  /** 本次新建的行 id，顺序与写入顺序一致；用于「撤销本次同步」 */
  rowIds: string[]
}

/**
 * 采样当前数据库上下文。
 *
 * 单元格统一转成展示文本：模型只需要“看得懂的值”，回写时由类型转换负责。
 * select / multi-select 的原始值是选项 id，必须先反查成选项名，否则模型会把
 * 一串随机 id 当成业务值照抄回补丁里。
 */
export function buildDatabaseContext(
  model: any,
  dataSource: any,
  limit = AI_CONTEXT_ROW_LIMIT,
): DatabaseAIContext {
  const columns = columnsFromModel(model, dataSource)
  const allRows = rowsFromDataSource(dataSource, columns)
  const rows: AIRow[] = allRows.slice(0, limit).map((row) => {
    const cells: Record<string, string> = {}
    for (const column of columns) {
      const value = cellToText(column, row.cells[column.id])
      if (value) cells[column.id] = value
    }
    return { id: row.rowId, title: row.title, cells }
  })
  return {
    columns: columns.map((column) => ({ id: column.id, name: column.name, type: column.type })),
    rows,
    truncated: allRows.length > rows.length,
  }
}

/** 单个单元格 → 展示文本；与 CSV 导出同一套规则（日期 ISO、选项反查名称）。 */
function cellToText(column: CsvColumnLike, value: unknown): string {
  if (value === null || value === undefined) return ''
  const options = (column.data as { options?: Array<{ id: string; value: string }> })?.options
  switch (column.type) {
    case 'date':
      return timestampToDateOnly(value)
    case 'checkbox':
      return value ? 'true' : 'false'
    case 'number':
      return typeof value === 'number' ? String(value) : ''
    case 'select':
      return optionLabel(options, value)
    case 'multi-select':
      return Array.isArray(value) ? value.map((item) => optionLabel(options, item)).filter(Boolean).join(', ') : ''
    default: {
      const text = typeof value === 'string' ? value : value?.toString?.()
      return typeof text === 'string' ? text : ''
    }
  }
}

/** 选项 id → 选项名；找不到时返回空串（宁可不展示，也不把 id 当成值）。 */
function optionLabel(
  options: Array<{ id: string; value: string }> | undefined,
  value: unknown,
): string {
  if (typeof value !== 'string') return ''
  return options?.find((option) => option.id === value)?.value ?? ''
}

/**
 * 应用解析后的补丁。
 *
 * `overwrite` 为 false 的 set_cell 只写空单元格；调用方在用户勾选“允许覆盖”
 * 后会把 overwrite 置为 true（由 `allowOverwrite` 参数统一控制）。
 */
export function applyDatabasePatch(options: {
  store: any
  dataSource: any
  model: any
  patch: ResolvedPatch
  allowOverwrite: boolean
}): DatabasePatchOutcome {
  const { store, dataSource, model, patch, allowOverwrite } = options
  const outcome: DatabasePatchOutcome = { columns: 0, rows: 0, cells: 0, rowIds: [] }
  // 新建列后要能按列名引用：维护「列名 → id」的本地映射。
  const columnIds = new Map<string, string>()
  for (const column of columnsFromModel(model, dataSource)) {
    columnIds.set(column.name.trim().toLowerCase(), column.id)
  }

  store.transact(() => {
    for (const op of patch.ops) {
      if (op.kind === 'add_property') {
        const id = dataSource.propertyAdd('end', { type: op.type })
        if (!id) throw new Error('[workbench-db-ai] propertyAdd failed')
        dataSource.propertyNameSet(id, op.name)
        if (op.options.length > 0) {
          dataSource.propertyDataSet(id, { options: op.options.map(toOption) })
        }
        columnIds.set(op.name.trim().toLowerCase(), id)
        outcome.columns += 1
        continue
      }

      if (op.kind === 'add_row') {
        const rowId: string = dataSource.rowAdd('end')
        if (!rowId) throw new Error('[workbench-db-ai] rowAdd failed')
        // 来源默认按「手动录入」补过（会话层包装了 rowAdd）；AI 建行显式改回 AI 来源。
        // 必须放在 cells 循环之前：归档补丁自带来源单元格，后写即可覆盖这里。
        markAiSource(dataSource, rowId)
        // 标题列是 spacial property：值就是行块自身的 Y.Text。
        const rowModel = store.getModelById(rowId)
        if (rowModel?.text && op.title) {
          rowModel.text.replace(0, rowModel.text.length, op.title)
        }
        for (const cell of op.cells) {
          const columnId = columnIds.get(cell.columnName.trim().toLowerCase()) ?? cell.columnId
          if (!columnId) continue
          if (writeCell(dataSource, rowId, columnId, cell.type, cell.value)) {
            outcome.cells += 1
          }
        }
        outcome.rows += 1
        outcome.rowIds.push(rowId)
        continue
      }

      // set_cell：非空单元格需要用户显式允许覆盖。
      if (op.overwrite && !allowOverwrite) continue
      const type = dataSource.propertyTypeGet(op.columnId) as string | undefined
      if (type === 'title') {
        // 标题列没有独立单元格：它的值就是行块自身的 Y.Text。
        const rowModel = store.getModelById(op.rowId)
        if (rowModel?.text) {
          rowModel.text.replace(0, rowModel.text.length, op.value)
          outcome.cells += 1
        }
        continue
      }
      if (writeCell(dataSource, op.rowId, op.columnId, normalizeType(type), op.value)) {
        outcome.cells += 1
      }
    }
  })

  return outcome
}

/** AI 建行补「AI 识别」来源；列或选项不存在时跳过（老条目可能没补这一列）。 */
function markAiSource(dataSource: any, rowId: string): void {
  const data = dataSource.propertyDataGet(WORKBENCH_DB_COLUMNS.source) as
    | { options?: Array<{ id: string }> }
    | undefined
  const options = Array.isArray(data?.options) ? data.options : []
  if (!options.some((option) => option.id === WORKBENCH_SOURCE_OPTIONS.ai)) return
  dataSource.cellValueChange(rowId, WORKBENCH_DB_COLUMNS.source, WORKBENCH_SOURCE_OPTIONS.ai)
}

/** 把字符串值按列类型转换成上游期望的原始值并写入；未能转换时返回 false。 */
function writeCell(
  dataSource: any,
  rowId: string,
  columnId: string,
  type: AIColumnType,
  value: string,
): boolean {
  const raw = toRawValue(dataSource, columnId, type, value)
  if (raw === undefined) return false
  dataSource.cellValueChange(rowId, columnId, raw)
  return true
}

/**
 * 文本 → 原始值。
 *
 * 富文本必须走列的 `rawValue.fromJson`（它返回 `Text`），直接传字符串会渲染成空；
 * 日期是毫秒时间戳、多选是数组，按上游契约自己转换。
 */
function toRawValue(
  dataSource: any,
  columnId: string,
  type: AIColumnType,
  value: string,
): unknown {
  switch (type) {
    case 'date': {
      const iso = parseDateOnly(value)
      return iso ? dateOnlyToTimestamp(iso) : undefined
    }
    case 'number': {
      const number = Number(value.replace(/,/g, ''))
      return Number.isFinite(number) ? number : undefined
    }
    case 'checkbox':
      return /^(true|1|yes|y|是|对)$/i.test(value.trim())
    case 'select':
      return ensureOption(dataSource, columnId, value)
    case 'multi-select': {
      const ids = value
        .split(/[,，、]/)
        .map((part) => ensureOption(dataSource, columnId, part.trim()))
        .filter((id): id is string => typeof id === 'string')
      return ids.length > 0 ? ids : undefined
    }
    default: {
      const fromJson = dataSource.propertyMetaGet(type)?.config?.rawValue?.fromJson
      return fromJson ? fromJson({ value }) : value
    }
  }
}

/** select / multi-select：按选项名取 id，缺了就补一个新选项（颜色循环）。 */
function ensureOption(dataSource: any, columnId: string, label: string): string | undefined {
  const text = label.trim()
  if (!text) return undefined
  const data = dataSource.propertyDataGet(columnId) as
    | { options?: Array<{ id: string; value: string }> }
    | undefined
  const options = Array.isArray(data?.options) ? data.options : []
  const existing = options.find((option) => option.value === text)
  if (existing) return existing.id
  // 选项 id 只在列内唯一即可；从序号起找到一个没被占用的，避免删过选项后撞车。
  const used = new Set(options.map((option) => option.id))
  let index = options.length
  while (used.has(`ai-opt-${index}`)) index += 1
  dataSource.propertyDataSet(columnId, {
    ...data,
    options: [...options, toOption(text, index)],
  })
  return `ai-opt-${index}`
}

function toOption(value: string, index: number): { id: string; value: string; color: string } {
  return {
    id: `ai-opt-${index}`,
    value,
    color: OPTION_COLORS[index % OPTION_COLORS.length],
  }
}

/** 上游列类型 → 本模块的窄类型；未知类型按富文本处理（最不容易丢内容）。 */
function normalizeType(type: string | undefined): AIColumnType {
  switch (type) {
    case 'number':
    case 'date':
    case 'select':
    case 'multi-select':
    case 'checkbox':
      return type
    default:
      return 'rich-text'
  }
}
