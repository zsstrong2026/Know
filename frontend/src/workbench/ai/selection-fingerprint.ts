/**
 * 选区指纹与写入计划（§9.15.3「选区指纹与防覆盖」）。
 *
 * 纯逻辑，不碰 DOM、不碰 BlockSuite：发起请求时记录指纹，应用前重新校验；
 * 变化就拒绝应用并提示重新生成，绝不覆盖用户新输入的文字。
 */
import { splitResultBlocks } from './text-diff'

/** 跨块选区里单个块的采样结果。 */
export interface BlockSpan {
  blockId: string
  start: number
  end: number
  text: string
}

/** 指纹里的块快照：只留定位与哈希，不保存正文（§11.9 不缓存内容）。 */
export interface FingerprintSpan {
  blockId: string
  start: number
  end: number
  textHash: number
}

/** 一次生成的选区指纹。 */
export interface SelectionFingerprint {
  /** 目标块 id；插入“下方”时也用它定位锚点块。跨块时是首块。 */
  blockId: string
  /** 选区起点（块内字符偏移）。 */
  start: number
  /** 选区终点（块内字符偏移，不含）。 */
  end: number
  /** 选中文本的哈希；用于发现“位置没变但文字被改”。 */
  textHash: number
  /** 发起请求时的条目 id；切条目后必须作废。 */
  itemId: string
  /**
   * 跨块选区的块序列（含首尾块）。
   *
   * 单块选区不填；多块时用它做后置校验，避免用户中途改动了中间某个块
   * 却被误认为“选区没变”。
   */
  spans?: FingerprintSpan[]
  /**
   * true 表示这不是真实选区，而是「插入锚点」（无选区时的文末/选中块末尾）。
   *
   * 校验时只要求锚点块仍然存在，不做文本比对——块的正文变化不影响插入位置。
   */
  anchorOnly?: boolean
}

/** 当前编辑器里的选区实况，用于与指纹比对。 */
export interface SelectionSnapshot {
  blockId: string
  start: number
  end: number
  text: string
  itemId: string
  /** 跨块选区的块序列；单块不填。 */
  spans?: BlockSpan[]
}

export type FingerprintVerdict =
  | { ok: true }
  | {
      ok: false
      reason: 'item-changed' | 'block-missing' | 'selection-moved' | 'text-changed'
    }

/** 轻量字符串哈希（FNV-1a 32 位）。只用于等值判断，不做安全用途。 */
export function hashText(text: string): number {
  let hash = 0x811c9dc5
  for (let i = 0; i < text.length; i += 1) {
    hash ^= text.charCodeAt(i)
    hash = Math.imul(hash, 0x01000193) >>> 0
  }
  return hash >>> 0
}

/** 由当前选区构造指纹。 */
export function fingerprintOf(snapshot: SelectionSnapshot): SelectionFingerprint {
  return {
    blockId: snapshot.blockId,
    start: snapshot.start,
    end: snapshot.end,
    textHash: hashText(snapshot.text),
    itemId: snapshot.itemId,
    spans: snapshot.spans?.map((span) => ({
      blockId: span.blockId,
      start: span.start,
      end: span.end,
      textHash: hashText(span.text),
    })),
  }
}

/**
 * 比对指纹与当前实况。
 *
 * 顺序有意义：先判条目与块是否存在（那是“预览整条作废”），
 * 再判选区位置与文本（那是“提示重新生成”）。两条都会拒绝写入。
 */
export function verifyFingerprint(
  fingerprint: SelectionFingerprint,
  current: SelectionSnapshot | null,
): FingerprintVerdict {
  if (current === null) return { ok: false, reason: 'block-missing' }
  if (current.itemId !== fingerprint.itemId) return { ok: false, reason: 'item-changed' }
  if (current.blockId !== fingerprint.blockId) return { ok: false, reason: 'block-missing' }
  if (current.start !== fingerprint.start || current.end !== fingerprint.end) {
    return { ok: false, reason: 'selection-moved' }
  }
  if (hashText(current.text) !== fingerprint.textHash) return { ok: false, reason: 'text-changed' }
  // 跨块选区再逐个块校验：中间某块被改动时首尾可能仍然一致，只查首块会漏判。
  if (fingerprint.spans?.length) {
    const currentSpans = current.spans ?? []
    if (currentSpans.length !== fingerprint.spans.length) {
      return { ok: false, reason: 'selection-moved' }
    }
    for (let i = 0; i < fingerprint.spans.length; i += 1) {
      const expected = fingerprint.spans[i]
      const actual = currentSpans[i]
      if (!actual || actual.blockId !== expected.blockId) {
        return { ok: false, reason: 'block-missing' }
      }
      if (actual.start !== expected.start || actual.end !== expected.end) {
        return { ok: false, reason: 'selection-moved' }
      }
      if (hashText(actual.text) !== expected.textHash) {
        return { ok: false, reason: 'text-changed' }
      }
    }
  }
  return { ok: true }
}

/** 三种写入方式（与 page-actions 的 PageAIWriteMode 同值，这里不 import 以避免循环依赖）。 */
export type InlineWriteMode = 'replace' | 'below' | 'aside'

/** 写入计划：编辑器执行时只看这个结构，不再判断业务规则。 */
export interface WritePlan {
  mode: InlineWriteMode
  blockId: string
  start: number
  end: number
  /** AI 返回的 Markdown；aside 模式不使用。 */
  markdown: string
  /**
   * 跨块写入的块序列。
   *
   * 有值时按块逐段替换，`segments` 与之一一对应：段数少于块数说明模型合并了段落，
   * 多出的块会被清空（不是删除，保留用户原有的块结构）；段数多出时多余的段
   * 合并进最后一块。
   */
  spans?: FingerprintSpan[]
  segments?: string[]
  /**
   * 纯文本写入：摘要框里的「替换选中 / 插入下方」用它。
   * 插入下方时不再把文本当 Markdown 解析，段落按空行拆分、段内换行原样保留。
   */
  plain?: boolean
}

/**
 * 由指纹 + 结果构造写入计划；aside 返回 null（只出卡片，永不写）。
 *
 * 跨块选区在这里完成「结果 → 块」的对齐，编辑器层只按 segments 逐块执行。
 */
export function buildWritePlan(
  fingerprint: SelectionFingerprint,
  markdown: string,
  mode: InlineWriteMode,
  plain = false,
): WritePlan | null {
  if (mode === 'aside') return null
  const base: WritePlan = {
    mode,
    blockId: fingerprint.blockId,
    start: fingerprint.start,
    end: fingerprint.end,
    markdown,
    ...(plain ? { plain: true } : {}),
  }
  if (!fingerprint.spans?.length || fingerprint.spans.length < 2) return base

  // 跨块只支持「整体重写」语义：插入下方仍然落在首块之后，不做逐块插入。
  const parts = splitResultBlocks(markdown)
  const spans = fingerprint.spans
  if (parts.length === spans.length) {
    return { ...base, spans, segments: parts }
  }
  if (parts.length > spans.length) {
    // 多出的段合并进最后一块，保证内容不丢。
    const head = parts.slice(0, spans.length - 1)
    const tail = parts.slice(spans.length - 1).join('\n\n')
    return { ...base, spans, segments: [...head, tail] }
  }
  // 段数不足：按顺序填，缺口补空串（对齐时清空该块）。
  const segments = spans.map((_, index) => parts[index] ?? '')
  return { ...base, spans, segments }
}

/**
 * 行内样式属性，与 BlockSuite delta 的属性同名。
 *
 * 写入时直接当 Text 的 attributes 用，所以这里只留 true（不写 false），
 * 避免把「取消加粗」这种无意义属性带进 delta。
 */
export type InlineAttrs = Partial<Record<'bold' | 'italic' | 'strike' | 'code', true>>

/** 一段带样式的行内文本。 */
export interface InlineRun {
  text: string
  attrs?: InlineAttrs
}

/**
 * 行内标记：链接/图片 → 只留可读文字；加粗、删除线、行内代码、斜体识别成样式。
 *
 * 交替顺序有意义：`**` 必须排在 `*` 前面，否则 `**粗体**` 会被拆成两个斜体。
 */
const INLINE_RE =
  /(!?)\[([^\]]*)\]\([^)]*\)|\*\*(.+?)\*\*|__([^\n]+?)__|~~([^\n]+?)~~|`([^`\n]+)`|\*([^*\n]+)\*|(?<![A-Za-z0-9])_([^_\n]+)_(?![A-Za-z0-9])/g

/** 两个 run 的属性是否等价（键与值都相同）。 */
function sameAttrs(a?: InlineAttrs, b?: InlineAttrs): boolean {
  const keysA = Object.keys(a ?? {})
  const keysB = Object.keys(b ?? {})
  if (keysA.length !== keysB.length) return false
  return keysA.every((key) => (a as Record<string, unknown>)[key] === (b as Record<string, unknown>)[key])
}

/** 追加 run，与上一段属性相同时合并，减少 delta 片段数。 */
function pushRun(runs: InlineRun[], text: string, attrs: InlineAttrs): void {
  if (!text) return
  const last = runs[runs.length - 1]
  if (last && sameAttrs(last.attrs, attrs)) {
    last.text += text
    return
  }
  runs.push(attrs.bold || attrs.italic || attrs.strike || attrs.code ? { text, attrs } : { text })
}

/**
 * 把一行的 Markdown 行内标记解析成带样式的 run。
 *
 * 目标是「Word 式」：`**粗体**` 变成真正的加粗，而不是把星号写进正文。
 * 嵌套（`**粗体里的 *斜体* 结束**`）靠递归解析选中内容实现；
 * 未闭合的标记原样保留，绝不吞掉用户的文字。
 */
export function parseInlineRuns(source: string, base: InlineAttrs = {}): InlineRun[] {
  const text = source ?? ''
  const runs: InlineRun[] = []
  const re = new RegExp(INLINE_RE.source, 'g')
  let cursor = 0

  for (const match of text.matchAll(re)) {
    const index = match.index ?? 0
    if (index > cursor) pushRun(runs, text.slice(cursor, index), base)

    // 链接/图片：只保留可读文字，URL 不进正文。
    if (match[2] !== undefined) {
      pushRun(runs, match[2], base)
      cursor = index + match[0].length
      continue
    }

    const bold = match[3] !== undefined || match[4] !== undefined
    const strike = match[5] !== undefined
    const code = match[6] !== undefined
    const inner = match[3] ?? match[4] ?? match[5] ?? match[6] ?? match[7] ?? match[8] ?? ''
    const attr: InlineAttrs = bold
      ? { bold: true }
      : strike
        ? { strike: true }
        : code
          ? { code: true }
          : { italic: true }

    if (code) {
      // 行内代码内部的标记不参与解析，整段按字面写入。
      pushRun(runs, inner, { ...base, ...attr })
    } else {
      // 加粗/斜体/删除线可嵌套，递归解析选中内容并合并属性。
      for (const run of parseInlineRuns(inner, { ...base, ...attr })) {
        pushRun(runs, run.text, run.attrs ?? base)
      }
    }
    cursor = index + match[0].length
  }

  if (cursor < text.length) pushRun(runs, text.slice(cursor), base)
  return runs
}

/**
 * 把 AI 返回的 Markdown 拆成块级片段（安全适配器，§9.15.3）。
 *
 * 只识别标题、引用、无序/有序列表与代码围栏；其余按普通段落。刻意不解析
 * 行内 HTML —— 插入只经过编辑器的块 API，不拼 innerHTML，也不执行脚本。
 */
export type MarkdownBlock =
  | { kind: 'heading'; level: 1 | 2 | 3; text: string }
  | { kind: 'quote'; text: string }
  | { kind: 'list'; ordered: boolean; items: string[] }
  | { kind: 'code'; language: string; text: string }
  | { kind: 'paragraph'; text: string }

const HEADING_RE = /^(#{1,6})\s+(.*)$/
const QUOTE_RE = /^\s{0,3}>\s?(.*)$/
const UL_RE = /^\s*[-*+]\s+(.*)$/
const OL_RE = /^\s*\d+[.)]\s+(.*)$/

export function markdownToBlocks(markdown: string): MarkdownBlock[] {
  const blocks: MarkdownBlock[] = []
  const lines = markdown.replace(/\r\n/g, '\n').split('\n')
  let i = 0

  while (i < lines.length) {
    const line = lines[i]

    // 代码围栏：整段原样保留，内部不做任何 Markdown 解析。
    const fence = /^```(.*)$/.exec(line)
    if (fence) {
      const language = fence[1].trim()
      const body: string[] = []
      i += 1
      while (i < lines.length && !/^```/.test(lines[i])) {
        body.push(lines[i])
        i += 1
      }
      i += 1 // 跳过收尾围栏（缺失时也前进一格，避免死循环）
      blocks.push({ kind: 'code', language, text: body.join('\n') })
      continue
    }

    if (!line.trim()) {
      i += 1
      continue
    }

    const heading = HEADING_RE.exec(line)
    if (heading) {
      // 层级夹到 3 层：规格要求 AI 提纲不超过 3 层，超出的降级而不是丢内容。
      const level = Math.min(heading[1].length, 3) as 1 | 2 | 3
      blocks.push({ kind: 'heading', level, text: heading[2].trim() })
      i += 1
      continue
    }

    // 引用：连续的 `>` 行合并成一个引用块（空 `>` 行只是段落分隔）。
    if (QUOTE_RE.test(line)) {
      const parts: string[] = []
      while (i < lines.length) {
        const match = QUOTE_RE.exec(lines[i])
        if (!match) break
        const content = match[1].trim()
        if (content) parts.push(content)
        i += 1
      }
      blocks.push({ kind: 'quote', text: parts.join(' ') })
      continue
    }

    if (UL_RE.test(line) || OL_RE.test(line)) {
      const ordered = OL_RE.test(line)
      const items: string[] = []
      while (i < lines.length) {
        const current = lines[i]
        const match = ordered ? OL_RE.exec(current) : UL_RE.exec(current)
        if (!match) break
        items.push(match[1].trim())
        i += 1
      }
      blocks.push({ kind: 'list', ordered, items })
      continue
    }

    // 普通段落：连续非空、非特殊行合并成一段。
    const paragraph: string[] = []
    while (i < lines.length) {
      const current = lines[i]
      if (!current.trim()) break
      if (HEADING_RE.test(current) || QUOTE_RE.test(current) || UL_RE.test(current) || OL_RE.test(current)) break
      if (/^```/.test(current)) break
      paragraph.push(current.trim())
      i += 1
    }
    blocks.push({ kind: 'paragraph', text: paragraph.join(' ') })
  }

  return blocks
}
