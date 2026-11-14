/**
 * 长文分块的纯逻辑（Q11：按标题边界切分）。
 *
 * 内联车道单次上限是 8000 字，超过就会直接 413。工作台文档天然有标题层级，
 * 按标题切是最稳的边界：既不切断段落，也能让每块的大小可控。
 *
 * 只做切分与大小估算，不发起请求——编排在 use-page-ai 里，便于单测覆盖。
 */

/** 单块目标上限；比后端 8000 略小，给指令与上下文留余量。 */
export const CHUNK_MAX_CHARS = 6000

/** 标题行：`# ` 到 `###### `，行首匹配。 */
const HEADING_RE = /^#{1,6}\s/

/**
 * 按标题边界把长文切块。
 *
 * 规则：
 *   1. 以标题行为界切段，标题归属它下面的正文；
 *   2. 累积到超过 maxChars 就收束成一块；
 *   3. 单节本身超限时按段落二次切分（再不行就硬切），保证每块都不超限。
 */
export function splitLongText(text: string, maxChars = CHUNK_MAX_CHARS): string[] {
  const source = (text ?? '').trim()
  if (!source) return []
  if (source.length <= maxChars) return [source]

  // 第一层：按标题切节。
  const sections: string[] = []
  let current: string[] = []
  for (const line of source.split('\n')) {
    if (HEADING_RE.test(line) && current.length) {
      sections.push(current.join('\n'))
      current = []
    }
    current.push(line)
  }
  if (current.length) sections.push(current.join('\n'))

  // 第二层：按 maxChars 累积；超长单节走段落切分。
  const chunks: string[] = []
  let buffer = ''
  const pushBuffer = () => {
    if (buffer.trim()) chunks.push(buffer.trim())
    buffer = ''
  }
  for (const section of sections) {
    if (section.length > maxChars) {
      pushBuffer()
      chunks.push(...splitByParagraph(section, maxChars))
      continue
    }
    if (buffer.length + section.length + 1 > maxChars) pushBuffer()
    buffer = buffer ? `${buffer}\n${section}` : section
  }
  pushBuffer()
  return chunks.length ? chunks : [source]
}

/** 段落级切分：空行分段，仍超限就按硬长度切。 */
function splitByParagraph(section: string, maxChars: number): string[] {
  const parts: string[] = []
  let buffer = ''
  const flush = () => {
    if (buffer.trim()) parts.push(buffer.trim())
    buffer = ''
  }
  for (const paragraph of section.split(/\n{2,}/)) {
    if (paragraph.length > maxChars) {
      flush()
      for (let i = 0; i < paragraph.length; i += maxChars) {
        parts.push(paragraph.slice(i, i + maxChars))
      }
      continue
    }
    if (buffer.length + paragraph.length + 2 > maxChars) flush()
    buffer = buffer ? `${buffer}\n\n${paragraph}` : paragraph
  }
  flush()
  return parts
}

/**
 * 合并多块结果。
 *
 * 摘要类需要再过一次模型（由调用方决定），这里只做「拼接 + 去重空行」，
 * 用于大纲这类可以线性拼接的动作。
 */
export function joinChunkResults(results: string[]): string {
  return results
    .map((item) => (item ?? '').trim())
    .filter(Boolean)
    .join('\n\n')
}

/** 需要走分块的长文动作；其余动作仍按单次上限处理。 */
export const CHUNKABLE_ACTIONS = new Set(['summarize', 'outline'])

/** 是否应该走分块：文本超限且动作支持分块。 */
export function shouldChunk(action: string, text: string, maxChars = CHUNK_MAX_CHARS): boolean {
  return CHUNKABLE_ACTIONS.has(action) && (text ?? '').length > maxChars
}
