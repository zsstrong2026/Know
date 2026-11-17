/**
 * 导出层脱敏：只处理即将复制 / 下载 / 存为文档的 Markdown。
 *
 * 页面展示与报告追问不复用它 —— 那些场景需要认出原文里的自己和同事。
 */
import type { InsightPrivacy } from '@/api/insight'

type MaskTerms = Pick<InsightPrivacy, 'persons' | 'organizations' | 'projects'>

interface MaskGroup {
  words: string[]
  prefix: string
}

/**
 * 按词表替换可识别信息。
 *
 * - 长词优先：「张三丰」不会先被「张三」截断；
 * - 同类多词按**输入顺序**编号（某人1、某人2），结果可预期；
 * - 只做精确文本替换，不猜人名，避免误伤普通词。
 */
export function maskSensitiveTerms(text: string, terms: MaskTerms): string {
  const groups: MaskGroup[] = [
    { words: terms.persons, prefix: '某人' },
    { words: terms.organizations, prefix: '组织' },
    { words: terms.projects, prefix: '项目' },
  ]
  let result = text
  for (const group of groups) {
    result = replaceGroup(result, group)
  }
  return result
}

function replaceGroup(text: string, group: MaskGroup): string {
  const ordered = dedupe(group.words)
  if (!ordered.length) return text
  const maskOf = new Map<string, string>()
  ordered.forEach((word, index) => {
    maskOf.set(word, ordered.length > 1 ? `${group.prefix}${index + 1}` : group.prefix)
  })
  let result = text
  for (const word of [...ordered].sort((a, b) => b.length - a.length)) {
    result = result.split(word).join(maskOf.get(word) ?? '')
  }
  return result
}

/** 去空、去重，保留首次出现的顺序。 */
function dedupe(words: string[]): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const raw of words) {
    const word = raw.trim()
    if (!word || seen.has(word)) continue
    seen.add(word)
    out.push(word)
  }
  return out
}
