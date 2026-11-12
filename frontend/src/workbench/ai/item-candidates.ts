/**
 * 文档 → 事项候选的纯逻辑（AI 识别与归档自动抽取共用）。
 *
 * 只做三件事，全部可离线单测：
 *   1. 归一候选（去空白、裁掉空标题、按标题去重）；
 *   2. 与事项表已有行去重（标题 + 截止日指纹）；
 *   3. 把候选编译成 `ResolvedPatch.add_row`，交给 editor 层一次性写入。
 *
 * 不依赖 BlockSuite：列 id / 列名 / 选项文案都由调用方按真实列配置传进来，
 * 因为建库语言不同，状态与来源的取值文案会不一样。
 */
import type { ResolvedCell, ResolvedOp, ResolvedPatch } from './database-patch'

/** 识别出来的一条事项；字段都可缺省，写入时按列配置补。 */
export interface ItemCandidate {
  title: string
  status?: string
  owner?: string
  due?: string
  priority?: string
  relation?: string
  /** 原文里的支撑句，用于让用户在确认前判断真假 */
  evidence?: string
  /** 0–1 的置信度；缺省按 1 处理 */
  confidence?: number
  /** 规则路径专用：正文待办已勾选，写入时用「已完成」而不是「待开始」 */
  done?: boolean
}

/** 候选来源：手动识别 / 归档自动抽取 / 规则路径（正文待办）。 */
export type ItemCandidateOrigin = 'ai' | 'archive' | 'todo'

/** 单次写入上限；与后端 WorkbenchAIItemsMaxLimit 保持一致。 */
export const ITEM_CANDIDATE_LIMIT = 50

/** 标题归一：压缩空白、去掉列表符号与结尾标点。 */
export function normalizeTitle(raw: string): string {
  return String(raw ?? '')
    .replace(/^[-*+•\d.、)\s]+/, '')
    .replace(/\s+/g, ' ')
    .replace(/[。；;，,、\s]+$/, '')
    .trim()
}

/** 去重指纹：标题 + 截止日；同标题不同截止日视为两条不同的安排。 */
export function candidateFingerprint(candidate: ItemCandidate): string {
  return `${normalizeTitle(candidate.title).toLowerCase()}|${(candidate.due ?? '').trim()}`
}

/**
 * 过滤 + 去重。
 *
 * `existing` 传事项表现有行的指纹集合（用 `buildExistingFingerprints` 生成），
 * 同批里重复出现的候选只保留第一条（通常是置信度更高的那条）。
 */
export function dedupeCandidates(
  candidates: ItemCandidate[],
  existing: ReadonlySet<string> = new Set(),
): { kept: ItemCandidate[]; skipped: number } {
  const seen = new Set<string>()
  const kept: ItemCandidate[] = []
  let skipped = 0
  for (const candidate of candidates) {
    const title = normalizeTitle(candidate.title)
    if (!title) {
      skipped += 1
      continue
    }
    const fingerprint = candidateFingerprint({ ...candidate, title })
    if (seen.has(fingerprint) || existing.has(fingerprint)) {
      skipped += 1
      continue
    }
    seen.add(fingerprint)
    kept.push({ ...candidate, title })
    if (kept.length >= ITEM_CANDIDATE_LIMIT) break
  }
  return { kept, skipped }
}

/** 事项表已有行的指纹集合；`cells` 的 key 是列 id。 */
export function buildExistingFingerprints(
  rows: Array<{ title: string; cells: Record<string, string> }>,
  dateColumnId: string,
): Set<string> {
  const result = new Set<string>()
  for (const row of rows) {
    result.add(candidateFingerprint({ title: row.title, due: row.cells[dateColumnId] ?? '' }))
  }
  return result
}

/** 写入所需的列配置：列名与取值文案都来自事项表本身。 */
export interface ItemWriteSchema {
  statusColumnId: string
  statusColumnName: string
  /** 「待开始」选项文案；空表示不写状态列 */
  statusTodo?: string
  /** 「已完成」选项文案：正文待办已勾选时用它 */
  statusDone?: string
  priorityColumnId?: string
  priorityColumnName?: string
  ownerColumnId?: string
  ownerColumnName?: string
  dateColumnId?: string
  dateColumnName?: string
  relationColumnId?: string
  relationColumnName?: string
  sourceColumnId?: string
  sourceColumnName?: string
  /** 来源列的取值文案（手动录入 / AI 识别 / 文档归档） */
  sourceLabel?: string
  sourceDocColumnId?: string
  sourceDocColumnName?: string
  /** 来源文档标题；写入「来源文档」列 */
  sourceDocTitle?: string
}

/**
 * 候选 → 写入补丁。
 *
 * 状态缺省补「待开始」：识别出来的都是还没做的事，落在待开始列才符合看板直觉。
 * 其余字段只写有值的那几项，空值交给用户在表里补，不臆造负责人或日期。
 */
export function buildItemPatch(candidates: ItemCandidate[], schema: ItemWriteSchema): ResolvedPatch {
  const ops: ResolvedOp[] = []
  for (const candidate of candidates) {
    const cells: ResolvedCell[] = []
    const push = (
      columnId: string | undefined,
      columnName: string | undefined,
      type: ResolvedCell['type'],
      value: string | undefined,
    ) => {
      const text = (value ?? '').trim()
      if (!columnId || !columnName || !text) return
      cells.push({ columnId, columnName, type, value: text })
    }
    push(
      schema.statusColumnId,
      schema.statusColumnName,
      'select',
      candidate.status || (candidate.done ? schema.statusDone : schema.statusTodo),
    )
    push(schema.priorityColumnId, schema.priorityColumnName, 'select', candidate.priority)
    push(schema.ownerColumnId, schema.ownerColumnName, 'rich-text', candidate.owner)
    push(schema.dateColumnId, schema.dateColumnName, 'date', candidate.due)
    push(schema.relationColumnId, schema.relationColumnName, 'rich-text', candidate.relation)
    push(schema.sourceColumnId, schema.sourceColumnName, 'select', schema.sourceLabel)
    push(
      schema.sourceDocColumnId,
      schema.sourceDocColumnName,
      'rich-text',
      schema.sourceDocTitle,
    )
    ops.push({ kind: 'add_row', title: candidate.title, cells })
  }
  return { ops, overwriteCount: 0, errors: [] }
}
