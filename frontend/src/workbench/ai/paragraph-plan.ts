/**
 * AI 语义分段的请求构造与回复解析（纯函数，便于单测）。
 *
 * 背景：规则分段只看长度与句末标点，读起来仍可能出现「一段讲了两件事」。
 * 这里把待分段的长段落编号后交给模型，让它按语义插入段落分隔；模型只允许
 * 「插空行」，不得改写正文——回复解析后会由 doc-format 再校验一次内容一致性，
 * 对不上就回落到规则分段，绝不让模型改写文档。
 */

/** 交给模型的指令（上限 500 字，与后端 instruction 校验一致）。 */
export const SEGMENT_INSTRUCTION =
  '请只做分段：在不改写、不增删、不调换任何文字的前提下，把每个编号标记（如 [[P1]]）下的文本按语义划分为自然段，段与段之间用一个空行分隔。编号行必须原样保留并独占一行，除编号与正文外不要输出任何说明。'

/** 单次请求的正文预算（字符）：留出余量，保证模型输出不被截断。 */
export const SEGMENT_BATCH_MAX_CHARS = 2400
/** 单次请求最多处理的段落数。 */
export const SEGMENT_BATCH_MAX_PARAGRAPHS = 6
/** 一次排版最多交给模型处理的段落数，超出部分走规则分段。 */
export const SEGMENT_MAX_PARAGRAPHS = 12

export interface SegmentBatch {
  /** 这份请求包含的原段落下标（对应调用方传入的 paragraphs） */
  indexes: number[]
  /** 带编号标记的请求正文 */
  text: string
}

/**
 * 编号标记：按出现位置切分正文。
 *
 * 不做整行匹配——模型偶尔会把标记和正文写在同一行，只要标记本身还在，
 * 内容一致性校验仍能保证不改写正文。
 */
const MARKER_PATTERN = /\[\[\s*P\s*(\d+)\s*\]\]/g

/**
 * 把待分段的段落按预算切成若干批。
 *
 * 单段超过预算时直接跳过（交给规则分段），其余段落按顺序装满一批再开下一批。
 */
export function buildSegmentBatches(paragraphs: string[]): SegmentBatch[] {
  const batches: SegmentBatch[] = []
  let current: SegmentBatch = { indexes: [], text: '' }
  let used = 0

  const flush = () => {
    if (current.indexes.length === 0) return
    batches.push(current)
    current = { indexes: [], text: '' }
    used = 0
  }

  for (let index = 0; index < paragraphs.length; index += 1) {
    if (index >= SEGMENT_MAX_PARAGRAPHS) break
    const text = paragraphs[index]?.trim() ?? ''
    if (!text) continue
    const marker = `[[P${index + 1}]]\n${text}`
    const cost = marker.length + 2
    if (cost > SEGMENT_BATCH_MAX_CHARS) continue
    const full =
      current.indexes.length >= SEGMENT_BATCH_MAX_PARAGRAPHS ||
      used + cost > SEGMENT_BATCH_MAX_CHARS
    if (full) flush()
    current.indexes.push(index)
    current.text = current.text ? `${current.text}\n\n${marker}` : marker
    used += cost
  }
  flush()
  return batches
}

/**
 * 解析模型回复：编号标记 → 分好的段落数组。
 *
 * 只认本批出现的编号；标记之前的开场白、未知编号、空正文一律忽略。
 * 段内按换行切段（原文是单段纯文本，不包含换行）。
 */
export function parseSegmentReply(reply: string, batch: SegmentBatch): Map<number, string[]> {
  const result = new Map<number, string[]>()
  if (!reply) return result

  const allowed = new Set(batch.indexes)
  const matches: Array<{ index: number; start: number; end: number }> = []
  MARKER_PATTERN.lastIndex = 0
  let match: RegExpExecArray | null
  while ((match = MARKER_PATTERN.exec(reply))) {
    matches.push({ index: Number(match[1]) - 1, start: match.index, end: MARKER_PATTERN.lastIndex })
  }

  matches.forEach((current, position) => {
    if (!allowed.has(current.index)) return
    const next = matches[position + 1]
    const body = reply.slice(current.end, next ? next.start : reply.length).trim()
    if (!body) return
    const chunks = body
      .split(/\n+/)
      .map((line) => line.trim())
      .filter(Boolean)
    if (chunks.length === 0) return
    result.set(current.index, chunks)
  })

  return result
}
