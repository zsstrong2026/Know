/**
 * 把 AI 写入计划应用到 BlockSuite（§9.15.3「应用是一次 BlockSuite 事务」）。
 *
 * 只有两个动作：
 *   - replace：用结果替换选区，保留选区外的行内样式与内容；
 *   - below：在锚点块之后插入新块，不覆盖原文。
 *
 * 刻意不碰 DOM、不拼 innerHTML：全部走 store / Text API，Markdown 经
 * markdownToBlocks 转成块，代码围栏按代码块插入。
 */
import type { WritePlan } from './selection-fingerprint'
import { markdownToBlocks, type MarkdownBlock } from './selection-fingerprint'

/** BlockSuite store / block 的最小形状；上游类型在 TS6 下无法被检查。 */
interface TextLike {
  replace(index: number, length: number, content: string): void
  toString(): string
}

interface BlockLike {
  id: string
  flavour: string
  text?: TextLike
  model?: { text?: TextLike }
}

interface StoreLike {
  /** 视图对象：只有 flavour / parent / children，没有 text */
  getBlock?: (id: string) => BlockLike | undefined
  /** 块模型：text 在这里 */
  getModelById?: (id: string) => BlockLike | null | undefined
  addBlock: (
    flavour: string,
    props?: Record<string, unknown>,
    parent?: string,
    parentIndex?: number,
  ) => string
  addSiblingBlocks?: (
    target: BlockLike,
    props: Array<{ flavour: string } & Record<string, unknown>>,
    placement?: 'after' | 'before',
  ) => string[]
  getParent?: (target: BlockLike | string) => BlockLike | null
  /** BlockSuite 的事务入口；跨块写入要靠它保证一次撤销。 */
  transact?: (fn: () => void) => void
  readonly?: boolean
  readOnly?: boolean
}

/** 应用结果：成功返回 true；锚点块不存在或只读返回 false（调用方提示重新生成）。 */
export interface ApplyOutcome {
  applied: boolean
  reason?: 'block-missing' | 'readonly' | 'empty'
}

/** 取块模型（addSiblingBlocks / getParent 需要模型对象，不是视图）。 */
function findModel(store: StoreLike, blockId: string): BlockLike | null {
  return store.getModelById?.(blockId) ?? null
}

/** 取块视图（判存在与 flavour）。 */
function findBlock(store: StoreLike, blockId: string): BlockLike | null {
  if (!store.getBlock) return null
  return store.getBlock(blockId) ?? null
}

/** 取可写的 Text；模型上的 text 才是真正的 Y.Text 代理。 */
function textOf(model: BlockLike | null): TextLike | undefined {
  return model?.model?.text ?? model?.text
}

/** 块正文长度（纯文本字符数），用于跨块整块替换的区间。 */
function blockLength(model: BlockLike | null): number {
  const text = textOf(model)
  return typeof text?.toString === 'function' ? text.toString().length : 0
}

/** 动态取 BlockSuite 的 Text：只有真正写入时才加载，首屏不背这份体积。 */
async function loadTextCtor(): Promise<((input: string) => unknown) | null> {
  try {
    const mod = (await import('@blocksuite/store')) as unknown as {
      Text?: new (input?: string) => unknown
    }
    return mod.Text ? (input: string) => new (mod.Text as new (i?: string) => unknown)(input) : null
  } catch {
    return null
  }
}

/** MarkdownBlock → BlockSuite 块 props；text 必须是 Text 实例，不能是裸字符串。 */
function toBlockProps(block: MarkdownBlock, makeText: (s: string) => unknown): Record<string, unknown> {
  switch (block.kind) {
    case 'heading':
      return { type: block.level === 1 ? 'h1' : block.level === 2 ? 'h2' : 'h3', text: makeText(block.text) }
    case 'code':
      return { language: block.language || 'text', text: makeText(block.text) }
    case 'list':
      return {
        type: block.ordered ? 'numbered' : 'bulleted',
        text: makeText(block.items[0] ?? ''),
      }
    default:
      return { text: makeText(block.text) }
  }
}

/**
 * 应用写入计划。
 *
 * async 是因为要动态加载 Text；调用方应把它当作一次用户动作处理，
 * 期间保持预览卡片处于“应用中”状态。
 */
export async function applyWritePlan(store: StoreLike, plan: WritePlan): Promise<ApplyOutcome> {
  if (store.readOnly || store.readonly) return { applied: false, reason: 'readonly' }
  if (!plan.markdown.trim()) return { applied: false, reason: 'empty' }

  // 存在性用视图判断，写入用模型：两者在上游是两个不同对象。
  const view = findBlock(store, plan.blockId)
  const model = findModel(store, plan.blockId)
  if (!view || !model) return { applied: false, reason: 'block-missing' }

  // 跨块：按块逐段替换，一次 transact 完成，用户一次 Ctrl+Z 即可整体撤销。
  if (plan.mode === 'replace' && plan.spans?.length && plan.segments) {
    const targets = plan.spans.map((span) => ({
      span,
      model: findModel(store, span.blockId),
    }))
    if (targets.some((target) => !target.model)) return { applied: false, reason: 'block-missing' }

    store.transact?.(() => {
      targets.forEach((target, index) => {
        const text = textOf(target.model)
        if (!text) return
        // 首尾块只替换选区范围，中间块整块替换（选区覆盖整块）。
        const isFirst = index === 0
        const isLast = index === targets.length - 1
        const start = isFirst ? target.span.start : 0
        const end = isLast ? target.span.end : blockLength(target.model)
        const next = plan.segments?.[index] ?? ''
        text.replace(start, Math.max(0, end - start), next)
      })
    })
    return { applied: true }
  }

  if (plan.mode === 'replace') {
    const text = textOf(model)
    if (!text) return { applied: false, reason: 'block-missing' }
    // 只替换选区范围：选区外的文字与行内样式都保持原样。
    text.replace(plan.start, plan.end - plan.start, plan.markdown)
    return { applied: true }
  }

  const makeText = (await loadTextCtor()) ?? ((s: string) => s)

  // 纯文本写入：段落按空行拆分，段内换行原样保留（诗歌/多行创作不丢分行）。
  if (plan.plain) {
    const paragraphs = plan.markdown
      .split(/\n{2,}/)
      .map((part) => part.replace(/^\n+|\n+$/g, ''))
      .filter((part) => part.trim())
    if (paragraphs.length === 0) return { applied: false, reason: 'empty' }

    const plainProps: Array<{ flavour: string } & Record<string, unknown>> = paragraphs.map(
      (text) => ({ flavour: 'affine:paragraph', text: makeText(text) }),
    )
    return insertBlocks(store, model, plainProps)
  }

  const blocks = markdownToBlocks(plan.markdown)
  if (blocks.length === 0) return { applied: false, reason: 'empty' }

  // 列表项要展开成多个块：addSiblingBlocks 一次插入，保持顺序。
  const props: Array<{ flavour: string } & Record<string, unknown>> = []
  for (const block of blocks) {
    if (block.kind === 'list') {
      for (const item of block.items) {
        props.push({
          flavour: 'affine:list',
          type: block.ordered ? 'numbered' : 'bulleted',
          text: makeText(item),
        })
      }
      continue
    }
    props.push({
      flavour: block.kind === 'code' ? 'affine:code' : 'affine:paragraph',
      ...toBlockProps(block, makeText),
    })
  }

  if (props.length === 0) return { applied: false, reason: 'empty' }
  return insertBlocks(store, model, props)
}

/** 在锚点块之后插入一批块；Markdown / 纯文本两条路径共用同一套兜底。 */
function insertBlocks(
  store: StoreLike,
  model: BlockLike,
  props: Array<{ flavour: string } & Record<string, unknown>>,
): ApplyOutcome {
  if (store.addSiblingBlocks) {
    store.addSiblingBlocks(model, props, 'after')
    return { applied: true }
  }
  // 兜底：没有 addSiblingBlocks 时逐个插到父块末尾（顺序仍然保持）。
  const parent = store.getParent?.(model)
  if (!parent) return { applied: false, reason: 'block-missing' }
  props.forEach(({ flavour, ...blockProps }) => {
    store.addBlock(flavour, blockProps, parent.id)
  })
  return { applied: true }
}
