/**
 * 数据库 CSV 导入/导出（P3-4）。
 *
 * 只处理「值 ↔ 文本」的映射，不碰 store：
 *   - 导出：`databaseToCsv` 把列定义 + 行值转成 RFC 4180 文本；
 *   - 导入：`csvToPlan` 把文本转成「行标题 + 单元格值」，并给出需要新增的列、
 *     需要扩充的 select 选项，由调用方一次性写回数据库块。
 *
 * 三条硬约束（开发计划 §6.2 P3-4 的退出证据）：
 *   1. 往返不丢列：表头 = 列名，导入按列名匹配；表头里多出的列会作为新列回传；
 *   2. 公式注入防护：以 `= + - @ Tab CR` 开头的值导出时加 `'` 前缀，导入时剥掉；
 *   3. 日期一律 ISO `YYYY-MM-DD`，不用 `toISOString` 归一化（东八区会偏一天）。
 */
import { dateOnlyToTimestamp, parseDateOnly, timestampToDateOnly } from './database-dates'

/** 参与导入导出的列定义（数据库块 props.columns 的子集）。 */
export interface CsvColumnLike {
  id: string
  type: string
  name: string
  data?: Record<string, unknown> | { options?: Array<{ id: string; value: string; color?: string }> }
}

/** 一行待导出的数据。 */
export interface CsvRowLike {
  rowId: string
  /** 标题列的值（数据库行块的文本） */
  title: string
  /** 列 id → 原始单元格值（日期为毫秒时间戳） */
  cells: Record<string, unknown>
}

/** 导入计划：调用方据此写 store。 */
export interface CsvImportPlan {
  rows: Array<{
    title: string
    /** 已存在列的单元格值（原始值，日期是毫秒时间戳） */
    cells: Record<string, unknown>
    /** 新列的值，key 是列名；调用方建好列后按列名回填 */
    extra: Record<string, string>
  }>
  /** 表头里有、数据库里没有的列（按纯文本列创建） */
  newColumns: string[]
  /** 需要写回的列定义变更（select / multi-select 的选项扩充） */
  columnData: Record<string, { options: Array<{ id: string; value: string; color?: string }> }>
  /** 因整行为空而跳过的行数 */
  skipped: number
}

/** 会被表格软件当成公式的前缀字符。 */
const FORMULA_PREFIX = /^[=+\-@\t\r]/

/** 导出时防公式注入：危险前缀前加一个单引号，表格软件会按文本处理。 */
export function guardFormula(text: string): string {
  return FORMULA_PREFIX.test(text) ? `'${text}` : text
}

/** 导入时还原：只剥掉「单引号 + 危险字符」的那一个引号，用户自己的 `'` 不动。 */
export function unguardFormula(text: string): string {
  return text.startsWith("'") && FORMULA_PREFIX.test(text.slice(1)) ? text.slice(1) : text
}

/** 单个单元格 → CSV 文本（不含引号处理，由 serialize 统一做）。 */
export function cellToCsvText(column: CsvColumnLike, value: unknown): string {
  if (value === null || value === undefined) return ''
  switch (column.type) {
    case 'date':
      // 存储是毫秒时间戳，直接输出数字在表格里是一串无意义的值。
      return timestampToDateOnly(value)
    case 'select':
      return optionLabel(column, value)
    case 'multi-select':
      return Array.isArray(value) ? value.map((item) => optionLabel(column, item)).join(', ') : ''
    case 'checkbox':
      return value ? 'true' : 'false'
    case 'number':
      return typeof value === 'number' ? String(value) : ''
    case 'rich-text': {
      // 富文本单元格的原始值是 Y.Text / Text，直接 String() 会得到 "[object Object]"。
      const text = typeof value === 'string' ? value : value?.toString?.()
      return typeof text === 'string' ? text : ''
    }
    default:
      return typeof value === 'string' ? value : ''
  }
}

/** 列定义 + 行 → CSV 文本（含 BOM，Excel 打开不乱码）。 */
export function databaseToCsv(columns: CsvColumnLike[], rows: CsvRowLike[]): string {
  const header = columns.map((column) => guardFormula(column.name))
  const lines = [serializeLine(header)]
  for (const row of rows) {
    const values = columns.map((column) =>
      guardFormula(
        column.type === 'title' ? row.title : cellToCsvText(column, row.cells[column.id]),
      ),
    )
    lines.push(serializeLine(values))
  }
  return `\ufeff${lines.join('\r\n')}\r\n`
}

/**
 * 把 CSV 文本解析成导入计划。
 *
 * `columns` 是数据库现有列；`usedOptionIds` 用于给新建选项生成不冲突的 id，
 * 避免与用户已有选项撞车（选项 id 只是字符串键，不要求可读）。
 */
export function csvToPlan(text: string, columns: CsvColumnLike[]): CsvImportPlan {
  const table = parseCsv(text)
  const plan: CsvImportPlan = { rows: [], newColumns: [], columnData: {}, skipped: 0 }
  if (table.length === 0) return plan

  const [rawHeader, ...body] = table
  const header = rawHeader.map((name) => unguardFormula(name.trim()))
  const byName = new Map(columns.map((column) => [column.name.trim(), column]))
  // 表头里有但数据库里没有的列：记为新增（重名列只报一次）。
  const newColumns = new Set<string>()
  const targets = header.map((name) => {
    const existing = byName.get(name)
    if (existing) return null
    if (name) newColumns.add(name)
    return name
  })
  plan.newColumns = [...newColumns]
  const newColumnNames = new Set(plan.newColumns)

  const optionIds = new Set<string>()
  columns.forEach((column) => {
    optionsOf(column).forEach((option) => optionIds.add(option.id))
  })

  for (const line of body) {
    if (line.every((cell) => !cell.trim())) {
      plan.skipped += 1
      continue
    }
    const cells: Record<string, unknown> = {}
    const extra: Record<string, string> = {}
    let title = ''
    line.forEach((raw, index) => {
      const column = byName.get(header[index] ?? '')
      const value = unguardFormula(raw)
      // 新列：原样存文本，建好列后由调用方按列名回填。
      if (!column) {
        const name = targets[index]
        if (name && newColumnNames.has(name) && value.trim()) extra[name] = value
        return
      }
      if (column.type === 'title') {
        title = value.trim()
        return
      }
      const converted = csvTextToValue(column, value, optionIds, plan)
      if (converted !== undefined) cells[column.id] = converted
    })
    // 文件里没有标题列时，用第一个非空单元格兜底，避免整行只有单元格没有标题。
    if (!header.some((name) => byName.get(name)?.type === 'title')) {
      title = (line.find((cell) => cell.trim()) ?? '').trim()
    }
    plan.rows.push({ title, cells, extra })
  }
  return plan
}

/** 单元格文本 → 原始值；无法转换时返回 undefined（表示不写入该单元格）。 */
function csvTextToValue(
  column: CsvColumnLike,
  text: string,
  optionIds: Set<string>,
  plan: CsvImportPlan,
): unknown {
  const trimmed = text.trim()
  if (!trimmed) return undefined
  switch (column.type) {
    case 'date': {
      const iso = parseDateOnly(trimmed)
      return iso ? dateOnlyToTimestamp(iso) : undefined
    }
    case 'select':
      return resolveOption(column, trimmed, optionIds, plan)
    case 'multi-select': {
      const ids = trimmed
        .split(',')
        .map((part) => resolveOption(column, part.trim(), optionIds, plan))
        .filter((id): id is string => typeof id === 'string')
      return ids
    }
    case 'checkbox':
      return /^(true|1|yes|y|是|对)$/i.test(trimmed)
    case 'number': {
      const num = Number(trimmed.replace(/,/g, ''))
      return Number.isFinite(num) ? num : undefined
    }
    default:
      return text
  }
}

/** 选项值 → id；缺了就登记一个新选项（同一批导入里复用同一个 id）。 */
function resolveOption(
  column: CsvColumnLike,
  label: string,
  optionIds: Set<string>,
  plan: CsvImportPlan,
): string | undefined {
  if (!label) return undefined
  // 优先读本批导入已登记的选项，否则同一批里重复出现的值会被登记多次。
  const options = plan.columnData[column.id]?.options ?? optionsOf(column)
  const existing = options.find((option) => option.value === label)
  if (existing) return existing.id
  let id = `csv-opt-${column.id}-${label}`
  let suffix = 1
  while (optionIds.has(id)) id = `csv-opt-${column.id}-${label}-${suffix++}`
  optionIds.add(id)
  const data = (plan.columnData[column.id] ??= { options: [...options] })
  data.options.push({ id, value: label })
  return id
}

export function optionsOf(column: CsvColumnLike): Array<{ id: string; value: string; color?: string }> {
  const options = (column.data as { options?: unknown })?.options
  return Array.isArray(options) ? (options as Array<{ id: string; value: string }>) : []
}

/** 数据库块 model 的列定义（只取用户声明的列）。 */
export function columnsFromModel(model: any, dataSource: any): CsvColumnLike[] {
  // 只取 props.columns：properties$ 还会前置 title / type 这类固定属性，
  // 它们不是用户列，导出会让表头多出无意义的列。
  const declared: Array<{ id: string }> = model?.props?.columns$.value ?? model?.props?.columns ?? []
  return declared.map((column) => ({
    id: column.id,
    type: dataSource.propertyTypeGet(column.id) ?? 'text',
    name: dataSource.propertyNameGet(column.id) || column.id,
    data: dataSource.propertyDataGet(column.id),
  }))
}

/** 行 id + 标题 + 单元格值；标题列取行块自身的文本。 */
export function rowsFromDataSource(dataSource: any, columns: CsvColumnLike[]): CsvRowLike[] {
  const titleColumn = columns.find((column) => column.type === 'title')
  return (dataSource.rows$.value as string[]).map((rowId) => {
    const cells: Record<string, unknown> = {}
    for (const column of columns) {
      if (column.type === 'title') continue
      cells[column.id] = dataSource.cellValueGet(rowId, column.id)
    }
    const rawTitle = titleColumn ? dataSource.cellValueGet(rowId, titleColumn.id) : ''
    // 标题列的原始值是一个 Y.Text；用 toString 拿纯文本，拿不到就当空标题。
    const title = typeof rawTitle?.toString === 'function' ? String(rawTitle.toString()) : ''
    return { rowId, title, cells }
  })
}

function optionLabel(column: CsvColumnLike, value: unknown): string {
  if (typeof value !== 'string') return ''
  return optionsOf(column).find((option) => option.id === value)?.value ?? ''
}

/** RFC 4180：含分隔符、引号、换行时加引号，内部引号翻倍。 */
function serializeLine(values: string[]): string {
  return values
    .map((value) => (/[",\r\n]/.test(value) ? `"${value.replace(/"/g, '""')}"` : value))
    .join(',')
}

/**
 * RFC 4180 解析（不依赖 papaparse：这里只需要二维数组，手写可离线单测）。
 * 支持引号包裹的逗号/换行/双引号，以及 CRLF / LF 混用。
 */
export function parseCsv(text: string): string[][] {
  const input = text.replace(/^\ufeff/, '')
  const rows: string[][] = []
  let row: string[] = []
  let cell = ''
  let quoted = false

  for (let i = 0; i < input.length; i += 1) {
    const char = input[i]
    if (quoted) {
      if (char === '"') {
        if (input[i + 1] === '"') {
          cell += '"'
          i += 1
        } else {
          quoted = false
        }
      } else {
        cell += char
      }
      continue
    }
    if (char === '"') {
      quoted = true
      continue
    }
    if (char === ',') {
      row.push(cell)
      cell = ''
      continue
    }
    if (char === '\r' || char === '\n') {
      // CRLF 只算一次换行
      if (char === '\r' && input[i + 1] === '\n') i += 1
      row.push(cell)
      rows.push(row)
      row = []
      cell = ''
      continue
    }
    cell += char
  }
  if (cell || row.length > 0) {
    row.push(cell)
    rows.push(row)
  }
  return rows
}
