/**
 * 最小 Markdown 投影（P1 版本）。
 *
 * 只把 Page 形态下的常见块转成 Markdown，供两处使用：
 *   - 保存时写入 `text_content`（列表搜索的 LIKE 目标）；
 *   - 归档时作为知识正文（P2-2 会补齐便签/Frame/数据库行）。
 *
 * 刻意不做富文本样式还原：`_`/`*` 等行内格式在归档里没有价值，
 * 反而会让 H2 拆分与知识检索产生噪音。
 */

/** BlockSuite store 只保证形状，类型在 TS6 下无法被检查（见 blocksuite-shim.d.ts）。 */
interface BlockLike {
  flavour: string
  text?: { toString(): string }
  props?: Record<string, any>
  children?: BlockLike[]
}

export interface ProjectionResult {
  markdown: string
  /** 纯文本（去掉 Markdown 标记），用于标题/摘要等场景 */
  text: string
}

const HEADING_BY_TYPE: Record<string, string> = {
  h1: '# ',
  h2: '## ',
  h3: '### ',
  h4: '#### ',
  h5: '##### ',
  h6: '###### ',
}

/**
 * 从编辑器 store 生成 Markdown。
 *
 * 只遍历 note 内的子块：surface 上的便签/形状在 P2-2 才投影，
 * 现在把它们误当正文会让归档出现一堆无语境的碎片。
 */
export function projectMarkdown(store: any): ProjectionResult {
  const lines: string[] = []
  const root = store?.root as BlockLike | undefined
  if (!root) return { markdown: '', text: '' }

  for (const note of collect(root, 'affine:note')) {
    walk(note.children ?? [], 0)
  }

  return {
    markdown: lines.join('\n').trim(),
    text: lines
      .map((line) => line.replace(/^#{1,6}\s+/, '').replace(/^[-*]\s+/, '').trim())
      .filter(Boolean)
      .join('\n'),
  }

  function walk(blocks: BlockLike[], depth: number) {
    for (const block of blocks) {
      switch (block.flavour) {
        case 'affine:paragraph': {
          const type = String(block.props?.type ?? 'text')
          const body = block.text?.toString() ?? ''
          if (!body.trim()) break
          lines.push(`${HEADING_BY_TYPE[type] ?? ''}${body}`)
          lines.push('')
          break
        }
        case 'affine:list': {
          const body = block.text?.toString() ?? ''
          if (!body.trim()) break
          const bullet = block.props?.type === 'numbered' ? '1. ' : '- '
          lines.push(`${'  '.repeat(depth)}${bullet}${body}`)
          break
        }
        case 'affine:code': {
          const body = block.text?.toString() ?? ''
          if (!body.trim()) break
          lines.push('```' + String(block.props?.language ?? ''))
          lines.push(body)
          lines.push('```')
          lines.push('')
          break
        }
        case 'affine:divider':
          lines.push('---')
          lines.push('')
          break
        default:
          break
      }
      // list 支持嵌套；其余块暂不递归，避免把数据库行当正文。
      if (block.flavour === 'affine:list' && block.children?.length) {
        walk(block.children, depth + 1)
      }
    }
  }
}

/** 收集 root 下指定 flavour 的第一层块（note 可能只有一个，但按数组返回更稳妥）。 */
function collect(root: BlockLike, flavour: string): BlockLike[] {
  return (root.children ?? []).filter((child) => child.flavour === flavour)
}
