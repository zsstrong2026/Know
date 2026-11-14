/**
 * 从 BlockSuite 编辑器读取选区、应用写入（§9.15.3）。
 *
 * 放在 workbench/ai 下而不是编辑器模块里，是为了让「选区/写入」这层
 * 与编辑器生命周期分开：编辑器只管渲染与快照，AI 只管取文与落字。
 *
 * 选区来源是 `CanvasHandle.getSelections()`：BlockSuite 把 selection 挂在
 * std（视图作用域）而不是 store 上，且切 mode 时 std 会被重建，所以只有
 * 编辑器模块能给出“当前那一份”。文本读 `getModelById().text`，
 * `getBlock()` 返回的是视图对象，没有 text。
 *
 * 跨块选区：BlockSuite 的 TextSelection 天然带 from/to 两个 range point，
 * 这里按文档顺序把首块、中间块、尾块串成 spans，供上层合并处理并逐块回写。
 * 只收「有 text 字段的块」，图片/分割线等无文本块直接跳过。
 */
import type { PageAIEditorAdapter } from './use-page-ai'
import type { BlockSpan, SelectionSnapshot } from './selection-fingerprint'
import type { WritePlan } from './selection-fingerprint'
import { applyWritePlan } from './apply-write-plan'

/** CanvasHandle 的最小形状（与 workbench/editor/blocksuite.ts 的导出保持一致）。 */
interface CanvasHandleLike {
  store: any
  doc?: any
  getSelections?: () => unknown[]
}

/** 可选的宿主元素：DOM 兜底时需要它限定选区范围。 */
export interface PageAIAdapterOptions {
  handle: () => CanvasHandleLike | null
  itemId: () => string
  host?: () => HTMLElement | null
}

/** 取当前的文本选区；多选/非文本一律返回 null。 */
export function currentTextSelection(handle: CanvasHandleLike | null): any {
  const selections = handle?.getSelections?.() ?? []
  return (selections.find((item: any) => item?.type === 'text') as any) ?? null
}

/** 取块正文；store.getModelById 才带 text，getBlock 只是视图。 */
function blockText(store: any, blockId: string): string {
  const model = store?.getModelById?.(blockId)
  const text = model?.text
  return typeof text?.toString === 'function' ? text.toString() : ''
}

/** 有正文的块（段落/标题/列表/代码）；其余块不参与文本选区。 */
const TEXT_FLAVOURS = new Set([
  'affine:paragraph',
  'affine:list',
  'affine:code',
  'affine:edgeless-text',
])

/** 按文档顺序列出所有可写文本块；只走 note 下的正文，跳过画布元素。 */
function orderedTextBlocks(
  store: any,
): Array<{ blockId: string; text: string; flavour: string }> {
  const result: Array<{ blockId: string; text: string; flavour: string }> = []
  const walk = (blocks: any[]) => {
    for (const block of blocks ?? []) {
      if (!block) continue
      if (TEXT_FLAVOURS.has(block.flavour)) {
        const text = (block.model?.text ?? block.text)?.toString?.() ?? ''
        result.push({ blockId: block.id, text, flavour: block.flavour })
      }
      if (block.children?.length) walk(block.children)
    }
  }
  const root = store?.root
  for (const note of root?.children ?? []) {
    if (note?.flavour !== 'affine:note') continue
    walk(note.children ?? [])
  }
  return result
}

/**
 * 由 TextSelection 还原成跨块 spans。
 *
 * 首块只取 index 之后、尾块只取到 index+length，中间块整块入选。
 * 端点块本身没有文本时返回 null（结构异常，宁可当作无选区）。
 */
export function selectionToSpans(store: any, selection: any): BlockSpan[] | null {
  if (!selection?.from?.blockId) return null
  const startPoint = selection.reverse ? (selection.to ?? selection.from) : selection.from
  const endPoint = selection.reverse ? selection.from : (selection.to ?? selection.from)

  const ordered = orderedTextBlocks(store)
  const startIndex = ordered.findIndex((block) => block.blockId === startPoint.blockId)
  const endIndex = ordered.findIndex((block) => block.blockId === endPoint.blockId)
  if (startIndex < 0 || endIndex < 0 || endIndex < startIndex) return null

  const spans: BlockSpan[] = []
  for (let i = startIndex; i <= endIndex; i += 1) {
    const block = ordered[i]
    const isFirst = i === startIndex
    const isLast = i === endIndex
    const from = isFirst ? Number(startPoint.index ?? 0) : 0
    const to = isLast
      ? Number(endPoint.index ?? 0) + Number(endPoint.length ?? 0)
      : block.text.length
    // 反向选择时首尾倒置：以 min/max 兜底，保证 start <= end。
    const start = Math.max(0, Math.min(from, to))
    const end = Math.max(0, Math.max(from, to))
    if (end <= start) continue
    if (end > block.text.length) return null
    spans.push({ blockId: block.blockId, start, end, text: block.text.slice(start, end) })
  }
  return spans.length ? spans : null
}

/**
 * 判断选区是否落在代码块内（§9.15.1：代码块内只保留「解释选中」）。
 *
 * 沿父块链查找 affine:code：块式代码块自身或祖先命中都算。
 */
export function isCodeBlockSelection(store: any, blockId: string): boolean {
  let current = store?.getBlock?.(blockId)
  // 最多上溯 8 层，避免异常数据结构造成死循环。
  for (let depth = 0; current && depth < 8; depth += 1) {
    if (current.flavour === 'affine:code') return true
    current = current.parent ? store?.getBlock?.(current.parent) : null
  }
  return false
}

/** 构造 Page AI 的编辑器适配器。 */
export function createPageAIAdapter(options: PageAIAdapterOptions): PageAIEditorAdapter {
  return {
    getSelection(): SelectionSnapshot | null {
      const handle = options.handle()
      const store = handle?.store
      if (!handle || !store) return null
      const selection = currentTextSelection(handle)
      if (!selection) return null

      const spans = selectionToSpans(store, selection)
      if (!spans?.length) return null

      // 单块与跨块共用同一个快照结构：text 是各块按顺序拼接的结果。
      const first = spans[0]
      const last = spans[spans.length - 1]
      return {
        blockId: first.blockId,
        start: first.start,
        end: last.end,
        text: spans.map((span) => span.text).join('\n'),
        itemId: options.itemId(),
        spans: spans.length > 1 ? spans : undefined,
      }
    },

    isCodeBlock(): boolean {
      const handle = options.handle()
      const store = handle?.store
      const selection = currentTextSelection(handle)
      if (!store || !selection) return false
      return isCodeBlockSelection(store, selection.from.blockId)
    },

    getPageText(): string {
      // 页面级命令只需要正文纯文本：跳过代码块与画布元素，避免把代码当段落喂给模型。
      return orderedTextBlocks(options.handle()?.store)
        .filter((block) => block.flavour !== 'affine:code')
        .map((block) => block.text.trim())
        .filter(Boolean)
        .join('\n')
    },

    getAnchorBlockId(): string | null {
      // 有选区时以选区首块为锚点；无选区时用正文最后一块，实现「追加到文末」。
      const store = options.handle()?.store
      if (!store) return null
      const selection = currentTextSelection(options.handle())
      if (selection?.from?.blockId) return String(selection.from.blockId)
      const blocks = orderedTextBlocks(store)
      return blocks.length ? blocks[blocks.length - 1].blockId : null
    },

    async applyWrite(plan: WritePlan): Promise<boolean> {
      const store = options.handle()?.store
      if (!store) return false
      const outcome = await applyWritePlan(store, plan)
      return outcome.applied
    },
  }
}
