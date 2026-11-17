/**
 * Markdown → BlockSuite 块规格。
 *
 * 独立成纯函数模块：只做文本映射，不碰编辑器与 DOM，便于单测。
 */
export interface MarkdownBlockSpec {
  flavour: string
  props?: Record<string, unknown>
  text?: string
}

/** 报告里最多出现到 h4；更深的标题按 h4 处理，避免层级噪音。 */
const HEADING_MAX_DEPTH = 4

/**
 * 把 Markdown 拆成块规格。
 *
 * 只覆盖报告实际用到的语法（标题、列表、引用、代码块、分割线、段落），
 * 不做完整 Markdown 解析：表格等复杂语法按段落写入，保证内容不丢。
 */
export function markdownToBlockSpecs(markdown: string): MarkdownBlockSpec[] {
  const specs: MarkdownBlockSpec[] = []
  let inFence = false
  let fenceLang = ''
  let codeLines: string[] = []

  for (const rawLine of markdown.split(/\r?\n/)) {
    const trimmed = rawLine.trim()

    if (trimmed.startsWith('```')) {
      if (inFence) {
        // 关闭代码块时才写块，块之间不会因为空行被拆散
        const text = codeLines.join('\n')
        if (text.trim()) {
          specs.push({
            flavour: 'affine:code',
            props: { language: fenceLang || 'plain' },
            text,
          })
        }
        inFence = false
        fenceLang = ''
        codeLines = []
      } else {
        inFence = true
        fenceLang = trimmed.slice(3).trim()
        codeLines = []
      }
      continue
    }
    if (inFence) {
      codeLines.push(rawLine)
      continue
    }
    if (!trimmed) continue

    if (/^(-{3,}|\*{3,}|_{3,})$/.test(trimmed)) {
      specs.push({ flavour: 'affine:divider' })
      continue
    }

    const heading = /^(#{1,6})\s+(.*)$/.exec(trimmed)
    if (heading) {
      const level = Math.min(heading[1].length, HEADING_MAX_DEPTH)
      specs.push({
        flavour: 'affine:paragraph',
        props: { type: `h${level}` },
        text: cleanInline(heading[2]),
      })
      continue
    }

    if (/^[-*+]\s+/.test(trimmed)) {
      specs.push({
        flavour: 'affine:list',
        props: { type: 'bulleted' },
        text: cleanInline(trimmed.replace(/^[-*+]\s+/, '')),
      })
      continue
    }

    if (/^\d+[.)]\s+/.test(trimmed)) {
      specs.push({
        flavour: 'affine:list',
        props: { type: 'numbered' },
        text: cleanInline(trimmed.replace(/^\d+[.)]\s+/, '')),
      })
      continue
    }

    if (trimmed.startsWith('>')) {
      specs.push({
        flavour: 'affine:paragraph',
        props: { type: 'quote' },
        text: cleanInline(trimmed.replace(/^>\s?/, '')),
      })
      continue
    }

    specs.push({
      flavour: 'affine:paragraph',
      props: { type: 'text' },
      text: cleanInline(trimmed),
    })
  }

  return specs
}

/** 去掉行内标记：文档里保留纯文本，避免 ** 与反引号混在正文里。 */
function cleanInline(text: string): string {
  return text
    .replace(/\*\*(.+?)\*\*/g, '$1')
    .replace(/__(.+?)__/g, '$1')
    .replace(/`([^`]+)`/g, '$1')
    .trim()
}
