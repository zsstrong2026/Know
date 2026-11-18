/**
 * 洞察助手回答的纯文本化。
 *
 * 右侧对话框是对话气泡而不是文档，所以模型即便仍输出 Markdown，
 * 展示前也统一去掉标记，避免出现 `##`、`**` 这类源码。
 */
export function plainAnswer(source: string): string {
  if (!source) return ''
  const lines = source.replace(/\r\n/g, '\n').split('\n')
  const out: string[] = []
  let inFence = false
  for (const raw of lines) {
    // 代码围栏行本身不是正文，丢弃；围栏内的内容按普通文本保留。
    if (/^\s*```/.test(raw)) {
      inFence = !inFence
      continue
    }
    if (!inFence && isHorizontalRule(raw)) continue
    if (!inFence && isTableDivider(raw)) continue
    out.push(stripLineMarkup(raw, inFence))
  }
  return out
    .join('\n')
    .replace(/\n{3,}/g, '\n\n')
    .trim()
}

/** 单行去掉块级与行内 Markdown 标记。 */
function stripLineMarkup(line: string, inFence: boolean): string {
  if (inFence) return line.replace(/\s+$/, '')
  let text = line
  text = text.replace(/^\s{0,3}>\s?/, '')
  text = text.replace(/^\s{0,3}#{1,6}\s+/, '')
  // 无序列表符号直接去掉；有序列表保留序号，读起来更清楚。
  text = text.replace(/^\s{0,3}[-*+]\s+/, '')
  text = text.replace(/^\s{0,3}(\d+)[.)]\s+/, '$1. ')
  // 表格：竖线换成一个空格，并把多余空白压平。
  if (text.includes('|')) {
    text = text.replace(/\s*\|\s*/g, ' ').replace(/\s+/g, ' ').trim()
  } else {
    text = text.replace(/\|/g, ' ')
  }
  text = text.replace(/!\[([^\]]*)\]\([^)]*\)/g, '$1')
  text = text.replace(/\[([^\]]+)\]\([^)]*\)/g, '$1')
  text = text.replace(/`([^`]+)`/g, '$1')
  text = text.replace(/\*\*([^*]+)\*\*/g, '$1')
  text = text.replace(/__([^_]+)__/g, '$1')
  text = text.replace(/~~([^~]+)~~/g, '$1')
  text = text.replace(/(^|[^*])\*([^*\n]+)\*/g, '$1$2')
  text = text.replace(/(^|[^_\w])_([^_\n]+)_/g, '$1$2')
  return text.replace(/\s+$/, '')
}

/** 是否是 Markdown 分隔线（--- / *** / ___）。 */
function isHorizontalRule(line: string): boolean {
  return /^\s{0,3}((?:-\s*){3,}|(?:\*\s*){3,}|(?:_\s*){3,})$/.test(line)
}

/** 是否是表格的分隔行（| --- | :---: |）。 */
function isTableDivider(line: string): boolean {
  return line.includes('-') && /^[\s|:-]+$/.test(line)
}
