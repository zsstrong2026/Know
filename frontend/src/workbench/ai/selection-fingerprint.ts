/**
 * 选区指纹与写入计划（§9.15.3「选区指纹与防覆盖」）。
 *
 * 纯逻辑，不碰 DOM、不碰 BlockSuite：发起请求时记录指纹，应用前重新校验；
 * 变化就拒绝应用并提示重新生成，绝不覆盖用户新输入的文字。
 */

/** 一次生成的选区指纹。 */
export interface SelectionFingerprint {
  /** 目标块 id；插入“下方”时也用它定位锚点块。 */
  blockId: string
  /** 选区起点（块内字符偏移）。 */
  start: number
  /** 选区终点（块内字符偏移，不含）。 */
  end: number
  /** 选中文本的哈希；用于发现“位置没变但文字被改”。 */
  textHash: number
  /** 发起请求时的条目 id；切条目后必须作废。 */
  itemId: string
}

/** 当前编辑器里的选区实况，用于与指纹比对。 */
export interface SelectionSnapshot {
  blockId: string
  start: number
  end: number
  text: string
  itemId: string
}

export type FingerprintVerdict =
  | { ok: true }
  | { ok: false; reason: 'item-changed' | 'block-missing' | 'selection-moved' | 'text-changed' }

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
}

/**
 * 由指纹 + 结果构造写入计划；aside 返回 null（只出卡片，永不写）。
 */
export function buildWritePlan(
  fingerprint: SelectionFingerprint,
  markdown: string,
  mode: InlineWriteMode,
): WritePlan | null {
  if (mode === 'aside') return null
  return {
    mode,
    blockId: fingerprint.blockId,
    start: fingerprint.start,
    end: fingerprint.end,
    markdown,
  }
}

/**
 * 把 AI 返回的 Markdown 拆成块级片段（安全适配器，§9.15.3）。
 *
 * 只识别标题、无序/有序列表与代码围栏；其余按普通段落。刻意不解析行内
 * HTML —— 插入只经过编辑器的块 API，不拼 innerHTML，也不执行脚本。
 */
export type MarkdownBlock =
  | { kind: 'heading'; level: 1 | 2 | 3; text: string }
  | { kind: 'list'; ordered: boolean; items: string[] }
  | { kind: 'code'; language: string; text: string }
  | { kind: 'paragraph'; text: string }

const HEADING_RE = /^(#{1,6})\s+(.*)$/
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
      if (HEADING_RE.test(current) || UL_RE.test(current) || OL_RE.test(current)) break
      if (/^```/.test(current)) break
      paragraph.push(current.trim())
      i += 1
    }
    blocks.push({ kind: 'paragraph', text: paragraph.join(' ') })
  }

  return blocks
}
