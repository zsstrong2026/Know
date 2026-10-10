// ATX 标题行（# ～ ######，兼容尾部闭合 #）
const atxHeadingRe = /^(#{1,6})\s+(.*?)\s*#*\s*$/
// 手写阿拉伯序号前缀，如「1.」「1、」（空白可选，仅用于同义判定）
const arabicNumPrefixRe = /^\d+[.、]\s*/
// 手写中文序号前缀，如「一、」「十二、」
const chineseNumPrefixRe = /^[一二三四五六七八九十]+、\s*/
// Markdown 行内修饰符号
const mdInlineRe = /[*_`~]/g

// 规范化标题文本用于同义比较：剥离标题/序号/行内修饰并去除全部空白
export const normalizeChapterTitleText = (text: string): string => {
  let s = text.trim()
  const heading = atxHeadingRe.exec(s)
  if (heading) {
    s = heading[2] ?? ''
  }
  s = s.replace(arabicNumPrefixRe, '')
  s = s.replace(chineseNumPrefixRe, '')
  s = s.replace(mdInlineRe, '')
  return s.replace(/\s+/g, '')
}

// 详情页顶部已固定展示章节标题，正文首个非空行若是同义标题行则剥离，避免重复显示
export const stripDuplicateChapterHeading = (content: string, chapterTitle: string): string => {
  const title = normalizeChapterTitleText(chapterTitle)
  if (!title || !content) {
    return content
  }
  const lines = content.split('\n')
  const firstIndex = lines.findIndex((line) => line.trim() !== '')
  if (firstIndex === -1) {
    return content
  }
  const firstLine = lines[firstIndex]!.trim()
  if (!atxHeadingRe.test(firstLine) || normalizeChapterTitleText(firstLine) !== title) {
    return content
  }
  lines.splice(firstIndex, 1)
  // 清掉剥离后残留的前导空行，保持正文顶部整洁
  return lines.join('\n').replace(/^\s*\n/, '')
}
