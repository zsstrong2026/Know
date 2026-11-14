/**
 * 纠错结果的解析与应用（纯逻辑，可离线单测）。
 *
 * 模型被要求输出 `{"issues":[{"original","suggestion","reason"}]}`，但实际输出
 * 常带 Markdown 代码围栏、前后解释，甚至被截断。这里做三件事：
 *   1. 从脏输出里抠出 JSON 主体并解析；
 *   2. 过滤字段缺失的条目，保证后续渲染不出现空卡片；
 *   3. 把「被接受的问题」按原文定位替换成建议文本。
 *
 * 定位策略：优先按 original 在原文中首次出现的位置替换，且同一位置只替换一次；
 * 找不到就跳过该条（不猜、不模糊匹配），宁可少改也不能把文字改错位置。
 */
import type { ProofreadIssue } from './inline-ai-types'

/** 从模型输出里提取问题列表；解析失败返回空数组。 */
export function parseProofreadIssues(raw: string): ProofreadIssue[] {
  const json = extractJsonObject(raw)
  if (!json) return []
  let parsed: unknown
  try {
    parsed = JSON.parse(json)
  } catch {
    return []
  }
  const list = (parsed as { issues?: unknown })?.issues
  if (!Array.isArray(list)) return []

  const issues: ProofreadIssue[] = []
  for (const item of list) {
    if (!item || typeof item !== 'object') continue
    const record = item as Record<string, unknown>
    const original = String(record.original ?? '').trim()
    const suggestion = String(record.suggestion ?? '').trim()
    if (!original && !suggestion) continue
    issues.push({
      original,
      suggestion,
      reason: String(record.reason ?? '').trim(),
    })
  }
  return issues
}

/**
 * 抠出第一个完整的 JSON 对象。
 *
 * 用括号配对而不是正则：模型把 JSON 包在 ```json 围栏里时正则很容易截错，
 * 而字符串里的 `}` 需要跳过，这也只有逐字符扫描才能正确处理。
 */
function extractJsonObject(raw: string): string | null {
  const text = raw ?? ''
  const start = text.indexOf('{')
  if (start < 0) return null

  let depth = 0
  let inString = false
  let escaped = false
  for (let i = start; i < text.length; i += 1) {
    const char = text[i]
    if (inString) {
      if (escaped) escaped = false
      else if (char === '\\') escaped = true
      else if (char === '"') inString = false
      continue
    }
    if (char === '"') {
      inString = true
      continue
    }
    if (char === '{') depth += 1
    else if (char === '}') {
      depth -= 1
      if (depth === 0) return text.slice(start, i + 1)
    }
  }
  return null
}

/**
 * 把被接受的问题应用到原文。
 *
 * `rejected` 是被用户取消勾选的问题下标；其余按出现顺序依次替换。
 * 替换从后往前做，避免前面的长度变化影响后面的下标。
 */
export function applyIssuesToText(
  original: string,
  issues: ProofreadIssue[],
  rejected: ReadonlySet<number> = new Set(),
): string {
  let result = original ?? ''
  if (!result || !issues.length) return result

  // 先算出每条问题在原文里的位置（各自向后找，保证不重叠错位）。
  const matches: Array<{ index: number; from: number; to: number; text: string }> = []
  let cursor = 0
  issues.forEach((issue, index) => {
    if (rejected.has(index)) return
    const needle = issue.original
    if (!needle) return
    const at = result.indexOf(needle, cursor)
    if (at < 0) return
    matches.push({ index, from: at, to: at + needle.length, text: issue.suggestion })
    cursor = at + needle.length
  })

  // 从后往前替换：后面的区间不受前面长度变化影响。
  for (let i = matches.length - 1; i >= 0; i -= 1) {
    const match = matches[i]
    result = result.slice(0, match.from) + match.text + result.slice(match.to)
  }
  return result
}
