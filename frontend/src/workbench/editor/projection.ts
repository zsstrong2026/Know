/**
 * 工作台正文投影（P2-2）。
 *
 * 把 BlockSuite store 投影成 Markdown，供两处使用：
 *   - 保存时写入 `text_content`（列表搜索的 LIKE 目标）；
 *   - 归档时作为知识正文。
 *
 * 覆盖范围（按实现方案 §10.14.4 的「隐藏而非删除」原则，投影只读不改块）：
 *   - Page 正文：段落 / 列表 / 代码 / 分割线 / 引用（P1 已有）；
 *   - 画布便签（affine:note）：正文按出现顺序接在 Page 正文之后；
 *   - 画布 Frame：标题作为小节标题，其子元素随后；
 *   - 独立文本块（affine:edgeless-text）：按段落处理并递归子块；
 *   - 数据库（affine:database）：输出 Markdown 表格，行 = 子块，列 = columns。
 *
 * 刻意不做富文本样式还原：`_`/`*` 等行内格式在归档里没有价值，
 * 反而会让 H2 拆分与知识检索产生噪音。
 */

/** BlockSuite store 只保证形状，类型在 TS6 下无法被检查（见 blocksuite-shim.d.ts）。 */
interface BlockLike {
  id?: string
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

/** 表格单元格里的值可能是数字时间戳；统一按本地日历日输出，避免东八区偏移。 */
function formatDate(value: unknown): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  const y = date.getFullYear()
  const m = String(date.getMonth() + 1).padStart(2, '0')
  const d = String(date.getDate()).padStart(2, '0')
  return `${y}-${m}-${d}`
}

/** 单元格值 → 文本：select 存 option id，需要反查 columns[].data.options 拿显示名。 */
function cellText(value: unknown, options: Array<{ id: string; value: string }>): string {
  if (value === null || value === undefined) return ''
  if (Array.isArray(value)) return value.map((item) => cellText(item, options)).join(', ')
  if (typeof value === 'boolean') return value ? '是' : '否'
  if (typeof value === 'string') {
    const match = options.find((opt) => opt.id === value)
    return match ? match.value : value
  }
  if (typeof value === 'number') return String(value)
  // rich-text / link 等对象形态：尽量取 value 或 text 字段，取不到就不输出。
  const record = value as Record<string, unknown>
  if (typeof record.value === 'string') return record.value
  if (typeof record.text === 'string') return record.text
  return ''
}

/**
 * 数据库 → Markdown 表格。
 *
 * 行来自 database 的 children（每行是一个 paragraph，其 text 是标题列）；
 * 列来自 props.columns，值来自 props.cells[rowId][columnId].value。
 * 没有 columns 时退化为「列出每行标题」，避免整块内容丢失。
 */
function projectDatabase(block: BlockLike): string[] {
  const rows = block.children ?? []
  if (rows.length === 0) return []
  const props = block.props ?? {}
  const columns: Array<{ id: string; type: string; name: string; data?: any }> =
    props.columns$?.value ?? props.columns ?? []

  const rowTitle = (row: BlockLike) => row.text?.toString().trim() ?? ''
  if (columns.length === 0) {
    return rows.map(rowTitle).filter(Boolean)
  }

  const cells: Record<string, Record<string, { value: unknown }>> =
    props.cells$?.value ?? props.cells ?? {}
  const header = columns.map((column) => column.name || column.type)
  const lines = [
    `| ${header.join(' | ')} |`,
    `| ${header.map(() => '---').join(' | ')} |`,
  ]
  for (const row of rows) {
    const rowId = row.id ?? ''
    const values = columns.map((column) => {
      // 标题列的值就是行块自身的文本，不是 cells 里的记录。
      if (column.type === 'title') return rowTitle(row)
      const cell = cells[rowId]?.[column.id]
      const options: Array<{ id: string; value: string }> = column.data?.options ?? []
      const text = cellText(cell?.value, options)
      // 日期列在存储里是毫秒时间戳，直接输出会变成一串数字。
      return column.type === 'date' ? formatDate(cell?.value) : text
    })
    lines.push(`| ${values.map((value) => value.replace(/\|/g, '\\|')).join(' | ')} |`)
  }
  return lines
}

/**
 * 从编辑器 store 生成 Markdown。
 *
 * 顺序固定为「Page 正文 → 画布 Frame → 画布独立文本 → 画布便签」，与原型里
 * 归档快照的阅读顺序一致：正文先读，画布元素按类型聚集，避免碎片化。
 */
export function projectMarkdown(store: any): ProjectionResult {
  const lines: string[] = []
  const root = store?.root as BlockLike | undefined
  if (!root) return { markdown: '', text: '' }

  const surface = collect(root, 'affine:surface')[0]
  const surfaceChildren = surface?.children ?? []

  // 1. Page 正文：root 下直接子级的 note 里的段落/列表/代码等，是归档的主体。
  //    画布便签也挂在 root 下（edgeless 里 note 是 surface 的子元素），因此这里
  //    只取 root 的直接子级，后面再单独处理 surface 上的便签。
  for (const note of collect(root, 'affine:note')) {
    const blocks = note.children ?? []
    // 画布便签里的数据库/图片等不属于正文，交给后面的分支处理。
    walk(blocks.filter((block) => block.flavour !== 'affine:database'), 0)
    // 数据库在正文里按表格输出，位置保持在它原本的段落顺序之后。
    for (const database of blocks.filter((block) => block.flavour === 'affine:database')) {
      walk([database], 0)
    }
  }

  // 2. Frame：标题作为小节，子元素按与便签相同的规则展开。
  const frames = surfaceChildren.filter((block) => block.flavour === 'affine:frame')
  if (frames.length > 0) {
    for (const frame of frames) {
      const title = frame.props?.title?.toString?.().trim() ?? ''
      if (title) {
        lines.push(`## ${title}`)
        lines.push('')
      }
      // Frame 的子元素通过 childElementIds 引用 surface 上的元素，这里按 id 反查。
      const childIds: string[] = Object.keys(frame.props?.childElementIds ?? {})
      const children = surfaceChildren.filter((child) => child.id && childIds.includes(child.id))
      walk(children, 0)
    }
  }

  // 3. 独立文本块：按段落处理，并递归其子块。
  for (const text of surfaceChildren.filter((block) => block.flavour === 'affine:edgeless-text')) {
    walk([text], 0)
  }

  // 4. 画布便签：正文按出现顺序追加，标题用居中的「便签」小标题区分来源。
  const notes = surfaceChildren.filter((block) => block.flavour === 'affine:note')
  for (const note of notes) {
    const before = lines.length
    walk(note.children ?? [], 0)
    // 便签正文为空时不留下空标题，避免归档里出现连续的空小节。
    if (lines.length > before) {
      lines.splice(before, 0, '### 便签', '')
    }
  }

  return {
    markdown: lines.join('\n').trim(),
    text: lines
      .map((line) =>
        line
          .replace(/^#{1,6}\s+/, '')
          .replace(/^>\s?/, '')
          .replace(/^[-*]\s(\[[ x]\]\s)?/, '')
          .replace(/^\d+\.\s+/, '')
          .trim(),
      )
      .filter(Boolean)
      .filter((line) => !line.startsWith('---'))
      .filter((line) => !/^\|[\s|:-]+\|$/.test(line))
      .join('\n'),
  }

  function walk(blocks: BlockLike[], depth: number) {
    for (const block of blocks) {
      switch (block.flavour) {
        case 'affine:paragraph': {
          const type = String(block.props?.type ?? 'text')
          const body = block.text?.toString() ?? ''
          if (!body.trim()) break
          // 引用块与 h4-h6 一样只是段落 type，统一用前缀表达。
          if (type === 'quote') {
            lines.push(`> ${body}`)
          } else {
            lines.push(`${HEADING_BY_TYPE[type] ?? ''}${body}`)
          }
          lines.push('')
          break
        }
        case 'affine:list': {
          const body = block.text?.toString() ?? ''
          if (!body.trim()) break
          const listType = String(block.props?.type ?? 'bulleted')
          const bullet = listType === 'numbered' ? '1. ' : listType === 'todo' ? '- [ ] ' : '- '
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
        case 'affine:database': {
          const table = projectDatabase(block)
          if (table.length > 0) {
            lines.push(...table)
            lines.push('')
          }
          // 数据库行本身就是表格内容，不再递归，避免重复输出行标题。
          break
        }
        case 'affine:edgeless-text': {
          walk(block.children ?? [], depth)
          break
        }
        default:
          break
      }
      // list 支持嵌套；edgeless-text 已在上面递归。
      if (block.flavour === 'affine:list' && block.children?.length) {
        walk(block.children, depth + 1)
      }
    }
  }
}

/** 收集 root 下指定 flavour 的第一层块（note/surface 可能有多个，按数组返回更稳妥）。 */
function collect(root: BlockLike, flavour: string): BlockLike[] {
  return (root.children ?? []).filter((child) => child.flavour === flavour)
}
