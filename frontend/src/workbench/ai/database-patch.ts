/**
 * 数据库 AI 的补丁契约与预览解析（P3-5，实现方案 §11.7）。
 *
 * 纯逻辑，不依赖 BlockSuite / axios：`tsx --test` 可以直接加载。
 * 职责只有两件事：
 *   1. 校验模型产出的补丁结构（服务端已校验一次，这里是第二道闸）；
 *   2. 把「列名 / 行 id」解析成可执行的写入计划，并标出会覆盖非空单元格的项。
 *
 * 应用计划由 `editor/database-ai.ts` 执行：它才持有 dataSource，能真正写 Y.Doc。
 */

/** 允许的列类型；与后端 types/workbench_ai_database.go 保持一致。
 *  注意：上游纯文本列的类型名是 `rich-text`（没有 `text`），模型写 text 时由
 *  `normalizeColumnType` 统一归一化，因此这里不列 `text`。 */
export const AI_COLUMN_TYPES = [
  'rich-text',
  'number',
  'date',
  'select',
  'multi-select',
  'checkbox',
] as const

export type AIColumnType = (typeof AI_COLUMN_TYPES)[number]

/** 请求里带的列快照。 */
export interface AIColumn {
  id: string
  name: string
  type: string
}

/** 请求里带的行快照；cells 的 key 是列 id，值是展示文本。 */
export interface AIRow {
  id: string
  title: string
  cells: Record<string, string>
}

export interface AIAddPropertyOp {
  op: 'add_property'
  name: string
  type: string
  options?: string[]
}

export interface AIAddRowOp {
  op: 'add_row'
  title?: string
  cells?: Record<string, string>
}

export interface AISetCellOp {
  op: 'set_cell'
  row_id: string
  column_id?: string
  column?: string
  value?: string
}

export type AIPatchOp = AIAddPropertyOp | AIAddRowOp | AISetCellOp

export interface AIPatch {
  ops: AIPatchOp[]
}

/** 服务端返回体（`POST /workbench/ai/database`）。 */
export interface DatabaseAIResponse {
  patch: AIPatch
  model_id?: string
  agent_id?: string
}

/** 单次可预览的上限；与后端保持一致，超出不应用。 */
export const AI_PATCH_MAX_OPS = 200
export const AI_PATCH_MAX_NEW_ROWS = 100
export const AI_PATCH_MAX_NEW_COLUMNS = 20
export const AI_CONTEXT_ROW_LIMIT = 50

/** 解析后的可执行计划。 */
export type ResolvedOp =
  | { kind: 'add_property'; name: string; type: AIColumnType; options: string[] }
  | {
      kind: 'add_row'
      title: string
      cells: ResolvedCell[]
    }
  | {
      kind: 'set_cell'
      rowId: string
      rowTitle: string
      columnId: string
      columnName: string
      value: string
      /** 目标单元格当前非空：必须勾选「允许覆盖」才写 */
      overwrite: boolean
      oldValue: string
    }

export interface ResolvedCell {
  columnId: string
  columnName: string
  type: AIColumnType
  value: string
}

export interface ResolvedPatch {
  ops: ResolvedOp[]
  /** 会被覆盖的非空单元格数量；>0 时要求用户显式确认 */
  overwriteCount: number
  /** 无法解析的项（列找不到、行不存在、类型不支持…）；非空时整包不可应用 */
  errors: string[]
}

/** 补丁结构的最小校验：返回错误列表，空数组表示可继续解析。 */
export function validatePatch(patch: unknown): string[] {
  const errors: string[] = []
  if (!patch || typeof patch !== 'object') return ['patch-missing']
  const ops = (patch as { ops?: unknown }).ops
  if (!Array.isArray(ops) || ops.length === 0) return ['patch-empty']
  if (ops.length > AI_PATCH_MAX_OPS) errors.push('patch-too-many-ops')
  return errors
}

/**
 * 把补丁解析成写入计划。
 *
 * 顺序很重要：`add_property` 先执行，后面的 `add_row` / `set_cell` 才能引用
 * 同一包里新建的列（模型经常这么写）。
 */
export function resolvePatch(
  patch: AIPatch,
  context: { columns: AIColumn[]; rows: AIRow[] },
): ResolvedPatch {
  const errors = validatePatch(patch)
  if (errors.length > 0) return { ops: [], overwriteCount: 0, errors }

  const columns = new Map<string, AIColumn>()
  for (const column of context.columns) columns.set(column.name.trim().toLowerCase(), column)

  const rows = new Map<string, AIRow>()
  for (const row of context.rows) rows.set(row.id, row)

  const ops: ResolvedOp[] = []
  let overwriteCount = 0
  let newColumns = 0
  let newRows = 0

  for (const [index, op] of patch.ops.entries()) {
    if (!op || typeof op !== 'object') {
      errors.push(`ops[${index}]:invalid`)
      continue
    }
    if (op.op === 'add_property') {
      const name = String(op.name ?? '').trim()
      if (!name) {
        errors.push(`ops[${index}]:column-name-required`)
        continue
      }
      const type = normalizeColumnType(op.type)
      if (!type) {
        errors.push(`ops[${index}]:column-type-unsupported`)
        continue
      }
      if (columns.has(name.toLowerCase())) {
        errors.push(`ops[${index}]:column-exists`)
        continue
      }
      const options = uniqueStrings(op.options ?? [])
      if ((type === 'select' || type === 'multi-select') && options.length === 0) {
        errors.push(`ops[${index}]:select-options-required`)
        continue
      }
      newColumns += 1
      if (newColumns > AI_PATCH_MAX_NEW_COLUMNS) {
        errors.push(`ops[${index}]:too-many-columns`)
        continue
      }
      // 新建列登记进查找表：后续 op 可以按列名引用它。
      columns.set(name.toLowerCase(), { id: '', name, type })
      ops.push({ kind: 'add_property', name, type, options })
      continue
    }

    if (op.op === 'add_row') {
      newRows += 1
      if (newRows > AI_PATCH_MAX_NEW_ROWS) {
        errors.push(`ops[${index}]:too-many-rows`)
        continue
      }
      const cells: ResolvedCell[] = []
      for (const [name, raw] of Object.entries(op.cells ?? {})) {
        const column = columns.get(name.trim().toLowerCase())
        if (!column) {
          errors.push(`ops[${index}]:column-not-found`)
          continue
        }
        // 标题列由 op.title 写入行块文本，不能走单元格写入，否则会写两遍。
        if (column.type === 'title') continue
        const value = String(raw ?? '').trim()
        if (!value) continue
        cells.push({
          columnId: column.id,
          columnName: column.name,
          // 未知类型按富文本处理：即使模型写错类型也不会让整包不可应用。
          type: normalizeColumnType(column.type) ?? 'rich-text',
          value,
        })
      }
      ops.push({ kind: 'add_row', title: String(op.title ?? '').trim(), cells })
      continue
    }

    if (op.op === 'set_cell') {
      const row = rows.get(String(op.row_id ?? '').trim())
      if (!row) {
        errors.push(`ops[${index}]:row-not-found`)
        continue
      }
      const column =
        columns.get(String(op.column ?? '').trim().toLowerCase()) ??
        findColumnById(context.columns, op.column_id)
      if (!column) {
        errors.push(`ops[${index}]:column-not-found`)
        continue
      }
      const value = String(op.value ?? '').trim()
      if (!value) continue
      const oldValue = String(row.cells[column.id] ?? '').trim()
      if (oldValue) overwriteCount += 1
      ops.push({
        kind: 'set_cell',
        rowId: row.id,
        rowTitle: row.title,
        columnId: column.id,
        columnName: column.name,
        value,
        overwrite: Boolean(oldValue),
        oldValue,
      })
      continue
    }

    errors.push(`ops[${index}]:unknown-op`)
  }

  return { ops, overwriteCount, errors }
}

/**
 * 统计各类改动，供预览头部显示「新建 N 列 / M 行 / K 格」。
 *
 * 新建行里带的单元格也计入「格」，否则预览会出现「新建 1 行 · 0 个单元格」
 * 但实际写了两格的自相矛盾数字。
 */
export function summarizePatch(patch: ResolvedPatch): {
  columns: number
  rows: number
  cells: number
} {
  let columns = 0
  let rows = 0
  let cells = 0
  for (const op of patch.ops) {
    if (op.kind === 'add_property') columns += 1
    else if (op.kind === 'add_row') {
      rows += 1
      cells += op.cells.length
    } else cells += 1
  }
  return { columns, rows, cells }
}

/**
 * 列类型归一化。
 *
 * 上游数据库块的可建列类型只有 rich-text / number / date / select /
 * multi-select / checkbox 等；`text` 是模型常写的别名，这里统一映射到
 * `rich-text`，否则 `propertyAdd(type)` 会拿到 undefined 直接抛错。
 */
export function normalizeColumnType(type: unknown): AIColumnType | null {
  const raw = String(type ?? '').trim().toLowerCase()
  // 纯文本列在上游叫 rich-text；模型写 text / string 都按它处理。
  if (raw === 'text' || raw === 'string') return 'rich-text'
  if (raw === 'rich-text' || raw === 'richtext') return 'rich-text'
  if (raw === 'number') return 'number'
  if (raw === 'date') return 'date'
  if (raw === 'select') return 'select'
  if (raw === 'multi-select' || raw === 'multiselect') return 'multi-select'
  if (raw === 'checkbox') return 'checkbox'
  return null
}

function findColumnById(columns: AIColumn[], id: unknown): AIColumn | undefined {
  const key = String(id ?? '').trim()
  if (!key) return undefined
  return columns.find((column) => column.id === key)
}

function uniqueStrings(values: unknown[]): string[] {
  const seen = new Set<string>()
  const result: string[] = []
  for (const value of values) {
    const text = String(value ?? '').trim()
    if (!text || seen.has(text)) continue
    seen.add(text)
    result.push(text)
  }
  return result
}
