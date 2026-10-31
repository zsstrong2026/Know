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
 */
import type { PageAIEditorAdapter } from './use-page-ai'
import type { SelectionSnapshot } from './selection-fingerprint'
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

/** 取当前的文本选区；多选/非文本/跨块一律返回 null。 */
export function currentTextSelection(handle: CanvasHandleLike | null): any {
  const selections = handle?.getSelections?.() ?? []
  const text = selections.find((item: any) => item?.type === 'text') as any
  if (!text?.from?.blockId) return null
  // 跨块选区不支持：AI 替换只针对单块（§9.15.3）。
  if (text.to && text.to.blockId !== text.from.blockId) return null
  return text
}

/** 取块正文；store.getModelById 才带 text，getBlock 只是视图。 */
function blockText(store: any, blockId: string): string {
  const model = store?.getModelById?.(blockId)
  const text = model?.text
  return typeof text?.toString === 'function' ? text.toString() : ''
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

      const from = selection.from
      const to = selection.to ?? selection.from
      // 反向选择：把起止点对调，保证 start <= end。
      const start = selection.reverse ? to : from
      const end = selection.reverse ? from : to
      const startIndex = Number(start.index ?? 0)
      const endIndex = Number(end.index ?? 0) + Number(end.length ?? 0)
      if (endIndex <= startIndex) return null

      const content = blockText(store, from.blockId)
      // BlockSuite 的 index 是纯文本偏移；越界说明结构已变，宁可当作无选区。
      if (endIndex > content.length) return null
      return {
        blockId: from.blockId,
        start: startIndex,
        end: endIndex,
        text: content.slice(startIndex, endIndex),
        itemId: options.itemId(),
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
      const store = options.handle()?.store
      if (!store) return ''
      // 侧栏页面级命令需要纯文本：只取 note 下的段落与列表，跳过画布元素。
      const lines: string[] = []
      const walk = (blocks: any[]) => {
        for (const block of blocks ?? []) {
          if (block.flavour === 'affine:paragraph' || block.flavour === 'affine:list') {
            const body = (block.model?.text ?? block.text)?.toString?.() ?? ''
            if (body.trim()) lines.push(body.trim())
          }
          if (block.children?.length) walk(block.children)
        }
      }
      const root = store.root
      const notes = (root?.children ?? []).filter((child: any) => child.flavour === 'affine:note')
      notes.forEach((note: any) => walk(note.children ?? []))
      return lines.join('\n')
    },

    async applyWrite(plan: WritePlan): Promise<boolean> {
      const store = options.handle()?.store
      if (!store) return false
      const outcome = await applyWritePlan(store, plan)
      return outcome.applied
    },
  }
}
