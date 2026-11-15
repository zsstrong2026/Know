/**
 * 把 AI 写入计划应用到 BlockSuite（§9.15.3「应用是一次 BlockSuite 事务」）。
 *
 * 只有两个动作：
 *   - replace：用结果替换选区，保留选区外的行内样式与内容；
 *   - below：在锚点块之后插入新块，不覆盖原文。
 *
 * 刻意不碰 DOM、不拼 innerHTML：全部走 store / Text API，Markdown 经
 * markdownToBlocks 转成块，行内标记（`**`、`*`、`` ` ``）转成 Text 的行内属性，
 * 写入后就是「Word 式」富文本，正文里不会残留 Markdown 源码。
 */
import type { WritePlan } from './selection-fingerprint'
import {
  markdownToBlocks,
  parseInlineRuns,
  type InlineRun,
  type MarkdownBlock,
} from './selection-fingerprint'
import { stripBlockMarkup } from './text-diff'

/** BlockSuite store / block 的最小形状；上游类型在 TS6 下无法被检查。 */
interface TextLike {
  replace(index: number, length: number, content: string, attributes?: Record<string, unknown>): void
  insert(content: string, index: number, attributes?: Record<string, unknown>): void
  toString(): string
}

/** delta 输入的最小形状（与 BlockSuite 的 DeltaInsert 同形）。 */
type DeltaInput = { insert: string; attributes?: Record<string, unknown> }

/** 构造 Text 的工厂：字符串或 delta 都要能接（带样式时只能走 delta）。 */
type MakeText = (input: string | DeltaInput[]) => unknown

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
async function loadTextCtor(): Promise<MakeText | null> {
  try {
    const mod = (await import('@blocksuite/store')) as unknown as {
      Text?: new (input?: string | DeltaInput[]) => unknown
    }
    if (!mod.Text) return null
    const TextCtor = mod.Text
    return (input) => new TextCtor(input)
  } catch {
    return null
  }
}

/** 拿不到 Text 时的兜底：delta 拼回纯字符串，保证不丢字。 */
function plainTextFactory(input: string | DeltaInput[]): string {
  return typeof input === 'string' ? input : input.map((part) => part.insert).join('')
}

/** InlineRun[] → Text delta；没有样式的片段不带 attributes 字段。 */
function toDelta(runs: InlineRun[]): DeltaInput[] {
  return runs
    .filter((run) => run.text.length > 0)
    .map((run) => (run.attrs ? { insert: run.text, attributes: { ...run.attrs } } : { insert: run.text }))
}

/** 一段文字 → 带行内样式的 Text；无样式时退回字符串构造，少一层 delta。 */
function makeRichText(text: string, makeText: MakeText): unknown {
  const runs = parseInlineRuns(text)
  return runs.some((run) => run.attrs) ? makeText(toDelta(runs)) : makeText(runs.map((run) => run.text).join(''))
}

/** 一整段 Markdown → 行内 run：先剥块级标记，再解析行内标记。 */
function richRuns(markdown: string): InlineRun[] {
  return parseInlineRuns(stripBlockMarkup(markdown))
}

/**
 * 用带样式的 run 替换 [start, start + length)。
 *
 * 先删后插：BlockSuite 的 Text 没有「带属性替换」的原生语义，
 * 逐段 insert 才能让加粗、斜体、行内代码成为真正的行内样式。
 */
function replaceWithRuns(text: TextLike, start: number, length: number, runs: InlineRun[]): void {
  text.replace(start, length, '')
  let offset = start
  for (const run of runs) {
    if (!run.text) continue
    text.insert(run.text, offset, run.attrs ? { ...run.attrs } : undefined)
    offset += run.text.length
  }
}

/** MarkdownBlock → BlockSuite 块 props；text 必须是 Text 实例，不能是裸字符串。 */
function toBlockProps(block: MarkdownBlock, makeText: MakeText): Record<string, unknown> {
  switch (block.kind) {
    case 'heading':
      return {
        type: block.level === 1 ? 'h1' : block.level === 2 ? 'h2' : 'h3',
        text: makeRichText(block.text, makeText),
      }
    case 'quote':
      return { type: 'quote', text: makeRichText(block.text, makeText) }
    case 'code':
      // 代码块内部不解析 Markdown：原文照写，星号与井号都是代码的一部分。
      return { language: block.language || 'text', text: makeText(block.text) }
    case 'list':
      return {
        type: block.ordered ? 'numbered' : 'bulleted',
        text: makeRichText(block.items[0] ?? '', makeText),
      }
    default:
      return { text: makeRichText(block.text, makeText) }
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
        // 纯文本写入（侧栏问答）不解析 Markdown；内联 AI 转成富文本。
        replaceWithRuns(text, start, Math.max(0, end - start), plan.plain ? [{ text: next }] : richRuns(next))
      })
    })
    return { applied: true }
  }

  if (plan.mode === 'replace') {
    const text = textOf(model)
    if (!text) return { applied: false, reason: 'block-missing' }
    // 只替换选区范围：选区外的文字与行内样式都保持原样。
    const runs = plan.plain ? [{ text: plan.markdown }] : richRuns(plan.markdown)
    // 富文本要逐段 insert，包一层 transact 才能和单次 replace 一样「一次撤销」。
    store.transact?.(() => replaceWithRuns(text, plan.start, plan.end - plan.start, runs))
    return { applied: true }
  }

  const makeText: MakeText = (await loadTextCtor()) ?? plainTextFactory

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
          text: makeRichText(item, makeText),
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
