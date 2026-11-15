/**
 * 文本 diff 与块级对齐的纯逻辑（不依赖 DOM / BlockSuite / Vue）。
 *
 * 分两层：
 *   1. 块内字级 diff：用 fast-diff（Myers），中文按字对比天然成立；
 *   2. 块级对齐：把 AI 返回的 Markdown 按空行切成段，与原选中的块数对齐。
 *
 * 两层都只产出数据结构，渲染由视图层决定（行内标增删 / 块级接受或拒绝）。
 */
import fastDiff from 'fast-diff'

/** 单侧文本超过此长度时放弃字级 diff：O(ND) 在长段落上会明显卡顿。 */
export const DIFF_MAX_CHARS = 4000

export type DiffOp = 'equal' | 'insert' | 'delete'

export interface DiffSegment {
  op: DiffOp
  text: string
}

const OP_INSERT = 1
const OP_DELETE = -1

/**
 * 计算原文 → 新文的字级 diff。
 *
 * 超过长度上限时退化为「整段删除 + 整段新增」：宁可读起来粗一些，
 * 也不要在主线程上算几十万次比较。
 */
export function diffSegments(original: string, next: string): DiffSegment[] {
  const before = original ?? ''
  const after = next ?? ''
  if (before === after) return after ? [{ op: 'equal', text: after }] : []
  if (before.length > DIFF_MAX_CHARS || after.length > DIFF_MAX_CHARS) {
    const fallback: DiffSegment[] = []
    if (before) fallback.push({ op: 'delete', text: before })
    if (after) fallback.push({ op: 'insert', text: after })
    return fallback
  }

  const raw = fastDiff(before, after) as Array<[number, string]>
  const merged: DiffSegment[] = []
  for (const [code, text] of raw) {
    if (!text) continue
    const op: DiffOp = code === OP_INSERT ? 'insert' : code === OP_DELETE ? 'delete' : 'equal'
    // fast-diff 可能给出相邻同类型片段，合并后渲染更稳定（也少一层 DOM）。
    const last = merged[merged.length - 1]
    if (last && last.op === op) last.text += text
    else merged.push({ op, text })
  }
  return merged
}

/**
 * 按空行把 Markdown 切成块。
 *
 * 代码围栏内的空行不切分（否则代码块会被拆散）；这是「块级对齐」的输入。
 */
export function splitResultBlocks(markdown: string): string[] {
  const lines = (markdown ?? '').replace(/\r\n/g, '\n').split('\n')
  const parts: string[] = []
  let current: string[] = []
  let fenceOpen = false

  const flush = () => {
    const text = current.join('\n').trim()
    if (text) parts.push(text)
    current = []
  }

  for (const line of lines) {
    if (/^```/.test(line)) fenceOpen = !fenceOpen
    if (!fenceOpen && !line.trim()) {
      flush()
      continue
    }
    current.push(line)
  }
  flush()
  return parts
}

/**
 * 模型附带的解释/分析段落：这些内容只用于对话，不写进正文。
 *
 * 只认「明确是元信息」的开头：说明：/ 注：/ 分析：/ 如需调整…，
 * 避免误删正文里正常的叙述（例如以「注意」开头的正文段落）。
 */
const ANALYSIS_BLOCK_RE =
  /^[（(【\[]?\s*(?:(?:补充说明|说明|注|备注|附注|分析|解释|提示|免责声明)\s*[：:]|如需(?:调整|修改|更换|其他|要)|以上(?:内容|是)|如果需要)/

/** 单独一行是否是 Markdown 分隔线（--- / *** / ___）。 */
const HORIZONTAL_RULE_RE = /^\s{0,3}((?:-\s*){3,}|(?:\*\s*){3,}|(?:_\s*){3,})$/

/**
 * 去掉单行的**块级**标记（引用 / 标题 / 列表符号），行内标记（`**`、`*`、`` ` ``）原样保留。
 *
 * 富文本写入要把块级结构交给块类型、行内标记交给 parseInlineRuns，
 * 所以这一步只剥块级符号。
 */
export function stripBlockMarkupLine(line: string): string {
  let text = (line ?? '').replace(/\r$/, '')
  text = text.replace(/^\s{0,3}>\s?/, '')
  text = text.replace(/^\s{0,3}#{1,6}\s+/, '')
  text = text.replace(/^\s{0,3}(?:[-*+]|\d+[.)])\s+/, '')
  return text
}

/**
 * 去掉整段 Markdown 的块级标记，保留行内标记与换行。
 *
 * 代码围栏行不是正文，直接丢弃；围栏内的代码行按原样保留。
 */
export function stripBlockMarkup(markdown: string): string {
  const lines = (markdown ?? '').replace(/\r\n/g, '\n').split('\n')
  return lines
    .filter((line) => !/^\s*```/.test(line))
    .map((line) => stripBlockMarkupLine(line))
    .join('\n')
}

/** 去掉单行的全部 Markdown 标记，只保留可读文本。 */
function stripMarkdownLine(line: string): string {
  let text = stripBlockMarkupLine(line)
  // 图片与链接：保留可读文字，丢掉 URL
  text = text.replace(/!\[([^\]]*)\]\([^)]*\)/g, '$1')
  text = text.replace(/\[([^\]]+)\]\([^)]*\)/g, '$1')
  // 行内代码与强调
  text = text.replace(/`([^`]+)`/g, '$1')
  text = text.replace(/\*\*([^*]+)\*\*/g, '$1')
  text = text.replace(/__([^_]+)__/g, '$1')
  text = text.replace(/~~([^~]+)~~/g, '$1')
  text = text.replace(/(^|[^*])\*([^*\n]+)\*/g, '$1$2')
  text = text.replace(/(^|[^_])_([^_\n]+)_/g, '$1$2')
  return text.replace(/\s+$/, '')
}

/**
 * 预览用的「Word 式」纯文本：逐行去掉全部标记，丢弃分隔线与代码围栏。
 *
 * 预览卡片展示的是用户最终会看到的排版效果，所以不能出现 `**`、`##` 这类源码；
 * 保留换行让 diff 仍能按行对齐。
 */
export function stripMarkdownForPreview(markdown: string): string {
  const lines = (markdown ?? '').replace(/\r\n/g, '\n').split('\n')
  return lines
    .filter((line) => !/^\s*```/.test(line) && !HORIZONTAL_RULE_RE.test(line))
    .map((line) => stripMarkdownLine(line))
    .join('\n')
}

/**
 * 把侧栏生成结果整理成可直接写入正文的纯文本。
 *
 * 两条规则：
 *   1. 去掉 Markdown 标记（标题井号、粗体、列表符号、引用、链接、代码围栏），
 *      插入后就是普通文字，不会出现 `##`、`**` 这类源码；
 *   2. 去掉模型附带的说明/分析段落，只保留创作内容本身。
 *
 * 段落之间保留一个空行，段内换行原样保留（例如诗歌的分行）。
 */
export function toPlainDocumentText(markdown: string): string {
  const lines = (markdown ?? '').replace(/\r\n/g, '\n').split('\n')

  /**
   * 按空行切块，逐行去掉 Markdown 标记。
   * `stripAnalysis` 为 true 时，遇到说明/分析行就截断（后面的内容都不属于正文）。
   */
  const collect = (stripAnalysis: boolean): string[] => {
    const parts: string[] = []
    let current: string[] = []
    let fenceOpen = false
    let analysisStarted = false

    const flush = () => {
      const text = current.join('\n').trim()
      current = []
      if (text) parts.push(text)
    }

    for (const line of lines) {
      // 代码围栏只去掉标记本身，围栏里的内容按原样保留
      if (/^\s*```/.test(line)) {
        fenceOpen = !fenceOpen
        continue
      }
      if (!fenceOpen && !line.trim()) {
        flush()
        continue
      }
      // 说明/分析可能紧接正文（没有空行），从这一行起都不写入正文。
      if (stripAnalysis && !analysisStarted && !fenceOpen && ANALYSIS_BLOCK_RE.test(line.trim())) {
        flush()
        analysisStarted = true
        continue
      }
      if (analysisStarted) continue
      if (!fenceOpen && HORIZONTAL_RULE_RE.test(line)) continue
      current.push(fenceOpen ? line : stripMarkdownLine(line))
    }
    flush()
    return parts
  }

  const content = collect(true)
  if (content.length) return content.join('\n\n').trim()
  // 整段回答都是说明/分析（例如纯解释）时退回纯文本，避免用户点了按钮却写不进去。
  return collect(false).join('\n\n').trim()
}
